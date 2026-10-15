package reason

// Wrapper-acceptance tests: object-topped responses ("steps" key) decode
// identically to the bare-array fixture shape.

import (
	"context"
	"testing"

	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/testprov"
)

func TestRunAcceptsWrapperObject(t *testing.T) {
	seq := testprov.NewSeq(`{"steps": [{"id": "s1", "text": "Derive the key.", "depends_on": ["c0"]}]}`)
	steps, warnings, err := Run(context.Background(), seq, "job-wrap", []report.Claim{
		{ID: "c0", Text: "The cache key is sha256 of prompt, output, and spec."},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seq.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", seq.Calls())
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(steps) != 1 || steps[0].ID != "s1" || len(steps[0].DependsOn) != 1 {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestRunMissingWrapperKeyRepairs(t *testing.T) {
	seq := testprov.NewSeq(`{"items": []}`, `{"steps": [{"id": "s1", "text": "Derive the key.", "depends_on": ["c0"]}]}`)
	steps, _, err := Run(context.Background(), seq, "job-repair", []report.Claim{
		{ID: "c0", Text: "The cache key is sha256 of prompt, output, and spec."},
	})
	if err != nil {
		t.Fatalf("repair path should have recovered: %v", err)
	}
	if seq.Calls() != 2 {
		t.Fatalf("calls = %d, want 2", seq.Calls())
	}
	if len(steps) != 1 || steps[0].ID != "s1" {
		t.Fatalf("steps = %+v", steps)
	}
}
