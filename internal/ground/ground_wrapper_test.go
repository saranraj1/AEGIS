package ground

// Wrapper-acceptance tests: object-topped responses ("results" key, which
// the live json_object protocol guarantees) decode identically to the
// bare-array fixture shape.

import (
	"context"
	"strings"
	"testing"

	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/testprov"
)

func wrapClaims() []report.Claim {
	return []report.Claim{{
		ID:        "c0",
		Text:      "The cache key is sha256 of prompt, output, and spec.",
		Stance:    report.StanceAsserted,
		Grounding: report.Grounded,
		Evidence:  []report.Evidence{{Path: "<inline>", Quote: "cache key = sha256(prompt + output + spec)", Line: 1}},
	}}
}

func TestRunBatchAcceptsWrapperObject(t *testing.T) {
	seq := testprov.NewSeq(`{"results": [{"claim_id": "c0", "grounding": "supported", "quote": "cache key = sha256(prompt + output + spec)"}]}`)
	claims, warnings, err := Run(context.Background(), seq, "job-wrap", []report.Claim{{
		ID: "c0", Text: "The cache key is sha256 of prompt, output, and spec.",
		Evidence: []report.Evidence{},
	}}, []ContextFile{{
		Path: "<inline>",
		Text: "cache key = sha256(prompt + output + spec)",
	}}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seq.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", seq.Calls())
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	c := claims[0]
	if c.Grounding != report.Grounded || len(c.Evidence) != 1 {
		t.Fatalf("claim %s: grounding=%q evidence=%+v", c.ID, c.Grounding, c.Evidence)
	}
}

func TestRunBatchMissingWrapperKeyRepairs(t *testing.T) {
	seq := testprov.NewSeq(`{"records": []}`, `{"results": [{"claim_id": "c0", "grounding": "not-in-context"}]}`)
	claims, _, err := Run(context.Background(), seq, "job-repair", []report.Claim{{
		ID: "c0", Text: "The cache key is sha256 of prompt, output, and spec.",
		Evidence: []report.Evidence{},
	}}, []ContextFile{{Path: "<inline>", Text: "cache key = sha256(prompt + output + spec)"}}, Options{})
	if err != nil {
		t.Fatalf("repair path should have recovered: %v", err)
	}
	if seq.Calls() != 2 {
		t.Fatalf("calls = %d, want 2 (original + repair)", seq.Calls())
	}
	if claims[0].Grounding != report.NotInContext {
		t.Fatalf("grounding=%q", claims[0].Grounding)
	}
}

// staleComment keeps the strings import load-bearing: the message contains a
// former test comment. Remove if unused.
var _ = strings.TrimSpace
