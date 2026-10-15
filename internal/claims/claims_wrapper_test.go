package claims

// Wrapper-acceptance tests: object-topped responses (the live shape under
// response_format json_object) decode identically to the bare-array
// fixture shape.

import (
	"context"
	"testing"

	"github.com/aegis-verify/aegis/internal/testprov"
)

const wobOutput = "The cache key is sha256 of prompt, output, and spec."

func TestExtractAcceptsWrapperObject(t *testing.T) {
	seq := testprov.NewSeq(`{"claims": [{"text": "The cache key is sha256 of prompt, output, and spec.", "stance": "asserted"}]}`)
	claims, warnings, err := Extract(context.Background(), seq, "job-wrapper", wobOutput)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if seq.Calls() != 1 {
		t.Fatalf("calls = %d, want 1 (no repair retry needed)", seq.Calls())
	}
	if len(claims) != 1 || claims[0].ID != "c0" || claims[0].Stance != "asserted" {
		t.Fatalf("claims = %+v", claims)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestExtractWrapperObjectMissingKeyUsesRepairThenFails(t *testing.T) {
	seq := testprov.NewSeq(`{"models": [{"text": "wrong"}]}`, `{"claims": [{"text": "The cache key is sha256 of prompt, output, and spec.", "stance": "asserted"}]}`)
	claims, _, err := Extract(context.Background(), seq, "job-repair", wobOutput)
	if err != nil {
		t.Fatalf("repair path should have recovered: %v", err)
	}
	if seq.Calls() != 2 {
		t.Fatalf("calls = %d, want 2 (original + repair)", seq.Calls())
	}
	if len(claims) != 1 || claims[0].ID != "c0" {
		t.Fatalf("claims = %+v", claims)
	}
}
