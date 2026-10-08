// Package provider defines the LLM I/O seam used by all Aegis stages
// (wrap, claims, ground, reason, critique) plus the OpenAI-compatible
// HTTP client and the in-process fake provider.
//
// The Provider interface is intentionally transport-level: it returns the
// raw assistant message content. Stage-level JSON decoding and the
// per-stage "repair this JSON" retry live in Phase 3 stage packages.
package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Stage identifies which Aegis stage a call belongs to. The fake provider
// uses it to locate the "{job-id}.{stage}.json" fixture.
type Stage string

const (
	StageWrap     Stage = "wrap"
	StageClaims   Stage = "claims"
	StageGround   Stage = "ground"
	StageReason   Stage = "reason"
	StageCritique Stage = "critique"
)

// Valid reports whether s is one of the five known stages.
func (s Stage) Valid() bool {
	switch s {
	case StageWrap, StageClaims, StageGround, StageReason, StageCritique:
		return true
	}
	return false
}

// Request describes one stage call. System and User are the two prompt
// messages (one system message plus one user message, per the spec).
// JobID is required by the fake provider and ignored by the live client.
type Request struct {
	Stage  Stage
	System string
	User   string
	JobID  string
}

// Response is the raw result of one stage call. Content is the assistant
// message text exactly as returned. Warnings carry non-fatal transport
// notes (e.g. response_format unsupported) that the pipeline threads into
// the report.
type Response struct {
	Content  string
	Warnings []string
}

// Error kind values. The CLI prints errors as "error: <kind>: <message>"
// on exit 2; the "error: " prefix is added by the CLI, not here.
type Kind string

const (
	// KindMissingEnv: a required USER_LLM_* variable is absent.
	KindMissingEnv Kind = "missing-env"
	// KindBadRequest: the request itself is malformed (unknown stage,
	// missing job id for the fake provider).
	KindBadRequest Kind = "bad-request"
	// KindProvider4xx: the endpoint returned a non-429 4xx. Hard error.
	KindProvider4xx Kind = "provider-4xx"
	// KindProviderTransient: 429 or 5xx; retried up to MaxAttempts then
	// reported with the last status in the message.
	KindProviderTransient Kind = "provider-transient"
	// KindBadResponse: a 2xx reply that is not a well-formed chat
	// completion (unparseable JSON, no choices).
	KindBadResponse Kind = "provider-bad-response"
	// KindTimeout: the per-stage context deadline expired.
	KindTimeout Kind = "provider-timeout"
	// KindFakeMissing: the fake fixture file for a required stage is absent.
	KindFakeMissing Kind = "fake-fixture-missing"
	// KindFakeUnreadable: the fake fixture exists but cannot be read.
	KindFakeUnreadable Kind = "fake-fixture-unreadable"
)

// Error is the structured error used across the provider layer.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Msg }

// Env var names required for live calls. The key's value is never logged,
// written to reports, or embedded in error messages.
const (
	EnvAPIKey  = "USER_LLM_API_KEY"
	EnvBaseURL = "USER_LLM_BASE_URL"
	EnvModel   = "USER_LLM_MODEL"
)

// FromEnv builds a live OpenAI-compatible client from the three required
// USER_LLM_* variables. Missing variables produce a single error naming
// each absent variable (never its value). There is no default base URL or
// model.
func FromEnv() (*OpenAI, error) {
	var missing []string
	for _, name := range []string{EnvAPIKey, EnvBaseURL, EnvModel} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, &Error{
			Kind: KindMissingEnv,
			Msg:  fmt.Sprintf("missing required environment variable(s) for live calls: %s", strings.Join(missing, ", ")),
		}
	}
	return NewOpenAI(os.Getenv(EnvBaseURL), os.Getenv(EnvAPIKey), os.Getenv(EnvModel)), nil
}

// Provider is the LLM I/O seam. Implementations must be safe for
// sequential use; stage orchestration (Phase 3+) drives the calls.
type Provider interface {
	Call(ctx context.Context, req Request) (Response, error)
}
