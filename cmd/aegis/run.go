package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aegis-verify/aegis/internal/engine"
	"github.com/aegis-verify/aegis/internal/job"
	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
)

// exitError formats an exit-2 message printed as "error: <msg>".
type exitError struct{ msg string }

func (e *exitError) Error() string { return e.msg }

// runCmd implements `aegis run`. It returns the process exit code; a
// non-nil error means exit 2 with the message printed by the caller as
// "error: <msg>" (kind-first per the D2 stderr convention).
func runCmd(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // flag parse errors get our own "error:" line
	var (
		outputPath   = fs.String("o", "", "report JSON path (default stdout)")
		outputPath2  = fs.String("output", "", "report JSON path (default stdout)")
		junitPath    = fs.String("junit", "", "also write a JUnit XML testsuite to this path")
		cacheDir     = fs.String("cache-dir", engine.DefaultCacheDir, "replay cache directory")
		cache        = fs.Bool("cache", true, "read + write the replay cache")
		noCache      = fs.Bool("no-cache", false, "neither read nor write the cache")
		cacheOnly    = fs.Bool("cache-only", false, "replay only, exit 2 on missing key, no provider calls")
		offline      = fs.Bool("offline", false, "alias of --cache-only")
		record       = fs.Bool("record", false, "alias of --cache (refresh the cache on main)")
		timeout      = fs.Duration("timeout", engine.DefaultRunTimeout, "whole-run timeout")
		stageTimeout = fs.Duration("stage-timeout", engine.DefaultStageTimeout, "per-LLM-call timeout")
		fakeFlag     = fs.Bool("fake-provider", false, "serve canned JSON instead of HTTP")
		fakeDirFlag  = fs.String("fake-dir", "", "directory of canned stage responses")
	)
	if err := fs.Parse(args); err != nil {
		return 2, &exitError{msg: fmt.Sprintf("invalid-args: %v", err)}
	}
	// Flags after the positional (the spec's `aegis run <job> [flags]`):
	// flag.StopArg0 terminates at the first non-flag token, so re-parse
	// the remainder with the leading positional skipped. Exactly one
	// positional may appear, and it must be the first leftover token.
	rest := fs.Args()
	if len(rest) == 0 {
		return 2, &exitError{msg: "invalid-args: run takes exactly one job file (or - for stdin)"}
	}
	jobPath := rest[0]
	if len(rest) > 1 {
		if err := fs.Parse(rest[1:]); err != nil {
			return 2, &exitError{msg: fmt.Sprintf("invalid-args: %v", err)}
		}
		// Pass 2 re-parsed the trailing flags; anything still leftover is
		// a second positional. Pass 1 leftovers have already been
		// consumed into jobPath + the re-parse, so only check pass 2.
		if leftovers := fs.Args(); len(leftovers) != 0 {
			return 2, &exitError{msg: "invalid-args: run takes exactly one job file (or - for stdin)"}
		}
	}

	mode := engine.CacheDefault
	if *noCache {
		mode = engine.CacheOff
	}
	if *cacheOnly || *offline {
		if *noCache {
			return 2, &exitError{msg: "invalid-args: --cache-only/--offline is mutually exclusive with --no-cache"}
		}
		mode = engine.CacheOnly
	}
	if *record {
		mode = engine.CacheDefault // spec: --record == default read+write
	}
	_ = *cache // readability; default value of the flag pair mirrors mode

	// Job source: file or stdin (stdin helper injected for tests via
	// the package-level runStdin override).
	var src []byte
	var err error
	if jobPath == "-" {
		src, err = io.ReadAll(os.Stdin)
		if err != nil {
			return 2, &exitError{msg: fmt.Sprintf("job-read-failed: stdin: %v", err)}
		}
	} else {
		src, err = os.ReadFile(jobPath)
		if err != nil {
			return 2, &exitError{msg: fmt.Sprintf("job-read-failed: %s: %v", jobPath, err)}
		}
	}
	j, err := job.Load(src)
	if err != nil {
		return 2, &exitError{msg: fmt.Sprintf("invalid-job: %v", err)}
	}

	// Provider: fake (flag or env), else live from USER_LLM_*.
	fake := *fakeFlag || os.Getenv("AEGIS_FAKE_PROVIDER") == "1"
	var prov provider.Provider
	if mode == engine.CacheOnly && !fake {
		// Cache-only must never touch the provider (spec: zero calls, no
		// env requirement). The engine never invokes it on a cache hit and
		// fails KindCacheMiss before any call, so a placeholder is safe.
		prov = provider.NewFake("")
	} else if fake {
		dir := *fakeDirFlag
		if dir == "" {
			return 2, &exitError{msg: "invalid-args: --fake-provider requires --fake-dir"}
		}
		prov = provider.NewFake(dir)
	} else {
		live, perr := provider.FromEnv()
		if perr != nil {
			return 2, &exitError{msg: perr.Error()}
		}
		prov = live
	}

	rep, err := engine.Run(context.Background(), j, prov, engine.Options{
		RunTimeout:   *timeout,
		StageTimeout: *stageTimeout,
		Cache:        mode,
		CacheDir:     *cacheDir,
	})
	if err != nil {
		// A1/A14 exit-2 stdout contract: when the job parsed and the
		// failure is RESOLVED-class (inside the pipeline — cache, wrap,
		// or any stage call), a best-effort one-line JSON with
		// exit_reason "engine_error" goes to stdout for tooling. Load-
		// stage failures (nil report: bad args, unreadable/invalid job,
		// missing context file) leave stdout empty and unparseable.
		if rep != nil {
			re := *rep
			re.OK = false
			re.ExitReason = "engine_error"
			re.Cache = report.CacheInfo{} // replay metadata is meaningless here
			if b, merr := json.Marshal(re); merr == nil {
				fmt.Fprintln(stdout, string(b))
			}
		}
		return 2, &exitError{msg: err.Error()}
	}

	// Write the report: stdout (default) or -o/--output.
	out := io.Writer(stdout)
	if p := firstNonEmpty(*outputPath, *outputPath2); p != "" {
		f, cerr := os.Create(p)
		if cerr != nil {
			return 2, &exitError{msg: fmt.Sprintf("report-write-failed: %s: %v", p, cerr)}
		}
		defer f.Close()
		out = f
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return 2, &exitError{msg: fmt.Sprintf("report-write-failed: %v", err)}
	}

	// Optional JUnit suite.
	if p := *junitPath; p != "" {
		if werr := os.WriteFile(p, report.JUnitSuite([]*report.Report{rep}), 0o644); werr != nil {
			return 2, &exitError{msg: fmt.Sprintf("junit-write-failed: %s: %v", p, werr)}
		}
	}

	if !rep.OK {
		// Assertion failures are already on the report; stderr stays
		// silent per the exit-code contract.
		return 1, nil
	}
	return 0, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

var _ = os.Stdin
