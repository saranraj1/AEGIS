// Package engine orchestrates the audit pipeline: load, wrap (output
// freeze), claims, ground, reason, critique, code checks, and assertion
// evaluation, plus the replay cache get/put.
//
// Exit-code discipline (CLI maps these): nil error + report.OK → 0,
// nil error + !report.OK → 1, error → 2. Grounding timeouts are NOT engine
// errors — they mark claims ungrounded and let assertions decide.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aegis-verify/aegis/internal/assert"
	"github.com/aegis-verify/aegis/internal/cache"
	"github.com/aegis-verify/aegis/internal/claims"
	"github.com/aegis-verify/aegis/internal/codechk"
	"github.com/aegis-verify/aegis/internal/critique"
	"github.com/aegis-verify/aegis/internal/ground"
	"github.com/aegis-verify/aegis/internal/job"
	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/reason"
	"github.com/aegis-verify/aegis/internal/report"
)

// Defaults per the spec.
const (
	DefaultRunTimeout   = 120 * time.Second
	DefaultStageTimeout = 45 * time.Second
	// InlinePath is the synthetic context path for context.inline.
	InlinePath = "<inline>"
)

// CacheMode selects the replay-cache behavior (spec flags: --cache
// default true, --no-cache, --cache-only, --record).
type CacheMode int

const (
	// CacheDefault reads and writes the cache (flags --cache, --record).
	CacheDefault CacheMode = iota
	// CacheOff neither reads nor writes (--no-cache).
	CacheOff
	// CacheOnly reads only; a missing key is a hard error and NO
	// provider calls are made (--cache-only).
	CacheOnly
)

// DefaultCacheDir is the CLI's --cache-dir default.
const DefaultCacheDir = ".aegis/cache"

// Options tunes one run.
type Options struct {
	RunTimeout   time.Duration // whole-run deadline, default 120s
	StageTimeout time.Duration // per LLM call, default 45s
	Cache        CacheMode     // default CacheDefault (read + write)
	CacheDir     string        // store dir, default DefaultCacheDir
}

// Error is an engine-level hard failure (exit 2). Kind values follow the
// D2 stderr convention; provider and stage errors pass through unchanged.
type Error struct {
	Kind string
	Msg  string
}

func (e *Error) Error() string { return e.Kind + ": " + e.Msg }

// Engine error kinds.
const (
	KindContextMissing    = "missing-context-file"
	KindContextUnreadable = "context-file-unreadable"
	KindWrapEmpty         = "empty-wrap-output"
	KindRunTimeout        = "run-timeout"
	KindCacheMiss         = "cache-miss"
	KindCacheStore        = "cache-store-error"
)

// WrapSystemPrompt is the fixed system message for the wrap call.
const WrapSystemPrompt = "You are a helpful assistant. Answer the user's prompt directly."

// Run executes the full pipeline for one job and returns the audit report.
// The provider comes pre-resolved (live client or fake); it is never nil.
func Run(ctx context.Context, j *job.Job, prov provider.Provider, opts Options) (*report.Report, error) {
	if opts.RunTimeout <= 0 {
		opts.RunTimeout = DefaultRunTimeout
	}
	if opts.StageTimeout <= 0 {
		opts.StageTimeout = DefaultStageTimeout
	}
	if opts.CacheDir == "" {
		opts.CacheDir = DefaultCacheDir
	}
	store := cache.NewStore(opts.CacheDir)

	runCtx, cancelRun := context.WithTimeout(ctx, opts.RunTimeout)
	defer cancelRun()

	rep := &report.Report{
		ID:               j.ID,
		Prompt:           j.Prompt,
		Claims:           []report.Claim{},
		Steps:            []report.Step{},
		CodeChecks:       []report.CodeCheck{},
		FailedAssertions: []report.FailedAssertion{},
		Warnings:         []string{},
	}

	// Stage 1: resolve context; the context is part of the cache key.
	files, ctxWarns, err := resolveContext(j)
	if err != nil {
		return nil, err
	}
	rep.Warnings = append(rep.Warnings, ctxWarns...)

	// Provisional key material: the output freezes before hashing.
	output := ""
	if j.Output != nil && !j.Model.Wrap {
		output = *j.Output
	}
	key := cacheKey(j, output, files)

	// Cache lookup (default and cache-only modes). On a hit the stored
	// report is returned as-is — including its original failed_assertions
	// and ok — with only cache.hit set to distinguish the replay.
	if opts.Cache != CacheOff {
		stored, _, err := store.Get(key)
		switch {
		case err == nil:
			stored.Cache = report.CacheInfo{Hit: true, Key: key}
			return stored, nil
		case errors.Is(err, os.ErrNotExist):
			if opts.Cache == CacheOnly {
				return nil, &Error{Kind: KindCacheMiss, Msg: fmt.Sprintf("no cached report for key %s (cache-only mode makes no provider calls)", key)}
			}
		default:
			// Corrupt or unreadable: in cache-only mode that is fatal;
			// otherwise fall through to a live run (the provider is still
			// the source of truth, a bad entry must not block it).
			if opts.Cache == CacheOnly {
				return nil, &Error{Kind: KindCacheStore, Msg: err.Error()}
			}
			rep.Warnings = append(rep.Warnings, "cache entry unreadable, proceeding live: "+err.Error())
		}
	}

	// Output freeze or wrap: with a provided output this is already
	// resolved above; otherwise the wrap call happens now.
	wrapUsed := false
	if j.Output == nil || j.Model.Wrap {
		wrapUsed = true
		callCtx, cancelCall := context.WithTimeout(runCtx, opts.StageTimeout)
		resp, err := prov.Call(callCtx, provider.Request{
			Stage:  provider.StageWrap,
			System: WrapSystemPrompt,
			User:   j.Prompt,
			JobID:  j.ID,
		})
		cancelCall()
		if err != nil {
			return nil, stageErr(runCtx, err)
		}
		rep.Warnings = append(rep.Warnings, resp.Warnings...)
		if strings.TrimSpace(resp.Content) == "" {
			return nil, &Error{Kind: KindWrapEmpty, Msg: fmt.Sprintf("wrap returned empty output for job %q", j.ID)}
		}
		output = resp.Content
	}
	rep.Output = output

	// With wrap, the output exists only after the wrap call, so the
	// final key is computed there (a provided output already has it).
	if wrapUsed {
		key = cacheKey(j, output, files)
	}
	rep.Cache = report.CacheInfo{Hit: false, Key: key}

	// Stage 3: claims.
	{
		callCtx, cancelCall := context.WithTimeout(runCtx, opts.StageTimeout)
		jobClaims, warns, err := claims.Extract(callCtx, prov, j.ID, output)
		cancelCall()
		if err != nil {
			return nil, stageErr(runCtx, err)
		}
		rep.Warnings = append(rep.Warnings, warns...)
		rep.Claims = jobClaims
	}

	// Stage 4: grounding. Timeouts mark batches ungrounded instead of failing.
	{
		jobClaims, warns, err := ground.Run(runCtx, prov, j.ID, rep.Claims, files, ground.Options{
			CallTimeout: opts.StageTimeout,
		})
		if err != nil {
			return nil, stageErr(runCtx, err)
		}
		rep.Warnings = append(rep.Warnings, warns...)
		rep.Claims = jobClaims
	}
	if runCtx.Err() != nil {
		return nil, &Error{Kind: KindRunTimeout, Msg: "whole-run timeout exceeded: " + runCtx.Err().Error()}
	}

	// Stage 5: reasoning.
	{
		callCtx, cancelCall := context.WithTimeout(runCtx, opts.StageTimeout)
		steps, warns, err := reason.Run(callCtx, prov, j.ID, rep.Claims)
		cancelCall()
		if err != nil {
			return nil, stageErr(runCtx, err)
		}
		rep.Warnings = append(rep.Warnings, warns...)
		rep.Steps = steps
	}

	// Stage 6: critique.
	{
		callCtx, cancelCall := context.WithTimeout(runCtx, opts.StageTimeout)
		crit, warns, err := critique.Run(callCtx, prov, j.ID, j.Prompt, rep.Output, rep.Claims, rep.Steps)
		cancelCall()
		if err != nil {
			return nil, stageErr(runCtx, err)
		}
		rep.Warnings = append(rep.Warnings, warns...)
		rep.Critique = crit
	}

	// Stage 7: deterministic code checks.
	force := j.Assertions.Code != nil && j.Assertions.Code.Parse
	checks, codeWarns := codechk.Run(runCtx, rep.Output, force, codechk.Options{})
	rep.Warnings = append(rep.Warnings, codeWarns...)
	rep.CodeChecks = checks
	if rep.CodeChecks == nil {
		rep.CodeChecks = []report.CodeCheck{}
	}

	// Stage 8: evaluate assertions and finalize.
	in := assert.Input{
		MustClaim:         j.Assertions.MustClaim,
		MustNotClaim:      j.Assertions.MustNotClaim,
		Grounded:          j.Assertions.Grounded != nil && *j.Assertions.Grounded,
		MaxUngrounded:     j.Assertions.MaxUngrounded,
		CritiqueMustAgree: j.Assertions.CritiqueMustAgree != nil && *j.Assertions.CritiqueMustAgree,
	}
	for _, c := range rep.Claims {
		in.Claims = append(in.Claims, assert.Claim{
			ID: c.ID, Text: c.Text, Stance: c.Stance, Grounding: c.Grounding,
		})
	}
	in.Critique = &assert.Critique{Verdict: rep.Critique.Verdict, Notes: rep.Critique.Notes}
	for _, cc := range rep.CodeChecks {
		in.CodeChecks = append(in.CodeChecks, assert.CodeCheck{
			Kind: cc.Kind, Language: cc.Language, OK: cc.OK, Detail: cc.Detail,
		})
	}
	rep.FailedAssertions = assert.Evaluate(in)
	if len(rep.FailedAssertions) == 0 {
		rep.OK = true
		rep.ExitReason = "ok"
	} else {
		rep.OK = false
		rep.ExitReason = "assertions_failed"
	}

	if runCtx.Err() != nil {
		return nil, &Error{Kind: KindRunTimeout, Msg: "whole-run timeout exceeded: " + runCtx.Err().Error()}
	}

	// Cache put (default mode only). A failed write is a warning, not
	// an audit failure: the report is valid even if replay later misses.
	if opts.Cache == CacheDefault {
		if err := store.Put(key, rep); err != nil {
			rep.Warnings = append(rep.Warnings, "cache write failed: "+err.Error())
		}
	}
	return rep, nil
}

// stageErr classifies a stage failure: if the WHOLE-RUN deadline expired,
// the run could not complete and reports the run-timeout kind; otherwise
// the underlying error (already structured for provider failures) passes
// through for the CLI's exit-2 formatting.
func stageErr(runCtx context.Context, err error) error {
	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return &Error{Kind: KindRunTimeout, Msg: "whole-run timeout exceeded: " + runCtx.Err().Error()}
	}
	return err
}

// resolveContext reads context files and the inline blob. Files are read
// relative to the process working directory; a missing file is a hard error.
func resolveContext(j *job.Job) ([]ground.ContextFile, []string, error) {
	var files []ground.ContextFile
	for _, path := range j.Context.Files {
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil, &Error{Kind: KindContextMissing, Msg: fmt.Sprintf("context file not found: %s", path)}
			}
			return nil, nil, &Error{Kind: KindContextUnreadable, Msg: fmt.Sprintf("context file unreadable: %s: %v", path, err)}
		}
		files = append(files, ground.ContextFile{Path: path, Text: string(data)})
	}
	if j.Context.Inline != nil && strings.TrimSpace(*j.Context.Inline) != "" {
		files = append(files, ground.ContextFile{Path: InlinePath, Text: *j.Context.Inline})
	}
	return files, nil, nil
}

// cacheKey builds the cache.File list (paths plus raw bytes) and delegates
// to the cache package.
func cacheKey(j *job.Job, output string, files []ground.ContextFile) string {
	keyFiles := make([]cache.File, 0, len(files))
	for _, f := range files {
		keyFiles = append(keyFiles, cache.File{Path: f.Path, Bytes: []byte(f.Text)})
	}
	return cache.Key(j, output, keyFiles)
}
