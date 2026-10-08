// Package engine orchestrates the audit pipeline: load, wrap (output
// freeze), claims, ground, reason, critique, code checks, and assertion
// evaluation. Cache get/put wiring arrives in a later phase; every run
// computes its cache key now.
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

// Options tunes one run.
type Options struct {
	RunTimeout   time.Duration // whole-run deadline, default 120s
	StageTimeout time.Duration // per LLM call, default 45s
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

	// Stage 1: resolve context.
	files, ctxWarns, err := resolveContext(j)
	if err != nil {
		return nil, err
	}
	rep.Warnings = append(rep.Warnings, ctxWarns...)

	// Stage 1 continued: output freeze or wrap.
	output := ""
	if j.Output != nil && !j.Model.Wrap {
		output = *j.Output
	} else {
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

	// Cache key (store lookup/put is a later phase).
	rep.Cache = report.CacheInfo{Hit: false, Key: cacheKey(j, output, files)}

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
