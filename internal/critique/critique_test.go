package critique

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/testprov"
)

const testJobID = "job1"

func fixtureClaims() []report.Claim {
	return []report.Claim{
		{ID: "c0", Text: "The cache key is sha256.", Grounding: report.Grounded,
			Evidence: []report.Evidence{{Path: "key.go", Quote: "sha256(", Line: 4}}},
		{ID: "c1", Text: "Cache entries expire.", Grounding: report.NotInContext,
			Evidence: []report.Evidence{}},
	}
}

func TestCritiqueAgree(t *testing.T) {
	prov := testprov.NewSeq(`{"verdict":"agree","notes":"refutation failed","disputed_claim_ids":[]}`)
	c, warnings, err := Run(context.Background(), prov, testJobID, "prompt", "output", fixtureClaims(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.Verdict != report.VerdictAgree || c.Notes != "refutation failed" {
		t.Fatalf("critique = %+v", c)
	}
	if c.DisputedClaimIDs == nil || len(c.DisputedClaimIDs) != 0 {
		t.Errorf("disputed = %#v, want empty non-nil", c.DisputedClaimIDs)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	if prov.Requests[0].Stage != provider.StageCritique || prov.Requests[0].JobID != testJobID {
		t.Errorf("request = %+v", prov.Requests[0])
	}
}

func TestCritiqueMixedWithKnownDisputed(t *testing.T) {
	prov := testprov.NewSeq(`{"verdict":"mixed","notes":"one claim shaky","disputed_claim_ids":["c1"]}`)
	c, _, err := Run(context.Background(), prov, testJobID, "p", "o", fixtureClaims(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.Verdict != report.VerdictMixed || len(c.DisputedClaimIDs) != 1 || c.DisputedClaimIDs[0] != "c1" {
		t.Fatalf("critique = %+v", c)
	}
}

func TestCritiqueUnknownDisputedFiltered(t *testing.T) {
	prov := testprov.NewSeq(`{"verdict":"mixed","notes":"n","disputed_claim_ids":["c1","c99"]}`)
	c, warnings, err := Run(context.Background(), prov, testJobID, "p", "o", fixtureClaims(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(c.DisputedClaimIDs) != 1 || c.DisputedClaimIDs[0] != "c1" {
		t.Fatalf("disputed = %v", c.DisputedClaimIDs)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "c99") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the dropped id: %v", warnings)
	}
}

func TestCritiqueUnknownVerdictGoesThroughRepair(t *testing.T) {
	prov := testprov.NewSeq(
		`{"verdict":"maybe","notes":"","disputed_claim_ids":[]}`,
		`{"verdict":"disagree","notes":"found a flaw","disputed_claim_ids":["c0"]}`,
	)
	c, _, err := Run(context.Background(), prov, testJobID, "p", "o", fixtureClaims(), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.Verdict != report.VerdictDisagree || prov.Calls() != 2 {
		t.Fatalf("critique = %+v, calls = %d", c, prov.Calls())
	}
}

func TestCritiqueUnknownVerdictHardFailsAfterRepair(t *testing.T) {
	prov := testprov.NewSeq(
		`{"verdict":"maybe"}`,
		`{"verdict":"unsure"}`,
	)
	_, _, err := Run(context.Background(), prov, testJobID, "p", "o", fixtureClaims(), nil)
	if err == nil {
		t.Fatal("expected hard error after repair")
	}
	if prov.Calls() != 2 {
		t.Errorf("calls = %d, want exactly 2", prov.Calls())
	}
}

func TestCritiquePromptCarriesAuditContext(t *testing.T) {
	prov := testprov.NewSeq(`{"verdict":"agree","notes":"","disputed_claim_ids":[]}`)
	steps := []report.Step{{ID: "s1", Text: "derive key", DependsOn: []string{"c0"}}}
	_, _, err := Run(context.Background(), prov, testJobID, "the prompt", "the output", fixtureClaims(), steps)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	u := prov.Requests[0].User
	for _, want := range []string{"the prompt", "the output", "c0 [supported]", "evidence key.go:4", "s1: derive key"} {
		if !strings.Contains(u, want) {
			t.Errorf("user prompt missing %q", want)
		}
	}
	sys := prov.Requests[0].System
	if !strings.Contains(sys, "MUST attempt to refute") {
		t.Errorf("system prompt must force a refute attempt: %q", sys)
	}
}

func TestCritiqueFakeTranscript(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"verdict":"disagree","notes":"quote does not hold","disputed_claim_ids":["c0"]}`
	path := filepath.Join(dir, testJobID+".critique.json")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := provider.NewFake(dir)
	c, _, err := Run(context.Background(), prov, testJobID, "p", "o", fixtureClaims(), nil)
	if err != nil {
		t.Fatalf("Run via fake: %v", err)
	}
	if c.Verdict != report.VerdictDisagree || len(c.DisputedClaimIDs) != 1 {
		t.Fatalf("critique = %+v", c)
	}
}
