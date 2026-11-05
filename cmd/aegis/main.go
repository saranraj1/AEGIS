// Command aegis is the verification CLI.
//
//	aegis run <job.yaml|job.json|-> [flags]
//	aegis cache gc --older-than <duration>
//
// Exit codes: 0 pass, 1 assertion failure, 2 engine error (bad job,
// cache miss in cache-only mode, provider failure, ...). Errors print
// one line to stderr in the D2 convention: "error: <kind>: <message>".
//
// Cache and assertions: assertion text is part of the cache key. Changing
// must_claim (or any other assertion) changes the key and requires a live
// run — there is no replay-old-report-with-new-assertions path.
package main

import (
	"fmt"
	"io"
	"os"
)

// Build metadata injected at link time via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(realMain(os.Args[1:], os.Stdout, os.Stderr))
}

func realMain(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "run":
		code, err := runCmd(args[1:], stdout, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
		}
		return code
	case "cache":
		return cacheCmd(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "aegis version %s (commit %s, built %s)\n", version, commit, date)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "error: invalid-args: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `aegis — verify LLM output against declarative assertions

Usage:
  aegis run <job.yaml|job.json|-> [flags]
  aegis cache gc --older-than <duration>
  aegis version

run flags:
  -o, --output PATH    write the report JSON to PATH (default stdout)
  --junit PATH         also write a JUnit XML testsuite to PATH
  --cache-dir DIR      replay cache directory (default .aegis/cache)
  --cache              read + write the cache (default)
  --record             same as --cache; use in CI to refresh on main
  --no-cache           neither read nor write the cache
  --cache-only         replay only; missing key is exit 2 and NO provider
                       calls are made (this is not a snapshot test: the
                       cached report replays with its original ok state)
  --offline            alias of --cache-only
  --timeout DUR        whole-run timeout (default 120s)
  --stage-timeout DUR  per-LLM-call timeout (default 45s)
  --fake-provider      serve canned JSON instead of HTTP (or env
                       AEGIS_FAKE_PROVIDER=1); needs --fake-dir and never
                       reads USER_LLM_*
  --fake-dir DIR       directory of canned stage responses
                       ({job-id}.{stage}.json)
  --                   job from stdin ("aegis run -")

cache gc flags:
  --older-than DUR     delete cache entries with cached_at older than DUR
  --cache-dir DIR      replay cache directory (default .aegis/cache)

Assertions are part of the cache key: tightening an assertion changes the
key and requires a live run. There is no replay-old-report-with-new-
assertions path.

Live calls need USER_LLM_API_KEY, USER_LLM_BASE_URL and USER_LLM_MODEL.

Exit codes: 0 pass, 1 assertions failed, 2 error.
`)
}
