package reason

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

func sampleClaims() []report.Claim {
	return []report.Claim{
		{ID: "c0", Text: "The cache key is sha256 of prompt, output, and spec."},
		{ID: "c1", Text: "Cache entries expire after 24 hours."},
	}
}

func runReason(t *testing.T, prov provider.Provider, content string, claims []report.Claim) ([]report.Step, []string, error) {
	t.Helper()
	if content != "" {
		prov = testprov.NewSeq(content)
	}
	return Run(context.Background(), prov, testJobID, claims)
}

func TestReasonHappyPath(t *testing.T) {
	prov := testprov.NewSeq(`[
		{"id":"hash","text":"Key derivation step","depends_on":["c0"]},
		{"id":"expiry","text":"Expiry step","depends_on":["c1","hash"]}
	]`)
	steps, warnings, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
	if steps[0].ID != "hash" || steps[0].DependsOn[0] != "c0" {
		t.Errorf("step 0 = %+v", steps[0])
	}
	// depends_on may cite earlier kept steps.
	if steps[1].DependsOn[1] != "hash" {
		t.Errorf("step 1 = %+v", steps[1])
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestReasonUnknownDependsOnDropsStep(t *testing.T) {
	prov := testprov.NewSeq(`[
		{"id":"good","text":"Fine","depends_on":["c0"]},
		{"id":"bad","text":"Broken","depends_on":["c0","nope"]},
		{"id":"cascade","text":"Depends on bad","depends_on":["bad"]}
	]`)
	steps, warnings, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(steps) != 1 || steps[0].ID != "good" {
		t.Fatalf("steps = %+v, want only good", steps)
	}
	// Both the unknown-id drop and the cascade drop must be warned.
	dropped := 0
	for _, w := range warnings {
		if strings.Contains(w, "nope") || strings.Contains(w, `"bad"`) {
			dropped++
		}
	}
	if dropped != 2 {
		t.Errorf("warnings = %v, want drops for bad and cascade", warnings)
	}
}

func TestReasonForwardReferenceDropped(t *testing.T) {
	prov := testprov.NewSeq(`[
		{"id":"first","text":"Forward","depends_on":["second"]},
		{"id":"second","text":"Later"}
	]`)
	steps, _, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(steps) != 1 || steps[0].ID != "second" {
		t.Fatalf("steps = %+v, want only second", steps)
	}
}

func TestReasonInvalidIDDropped(t *testing.T) {
	prov := testprov.NewSeq(`[
		{"id":"1bad","text":"Starts with digit"},
		{"id":"has space","text":"Contains space"},
		{"id":"ok","text":"Fine"}
	]`)
	steps, warnings, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(steps) != 1 || steps[0].ID != "ok" {
		t.Fatalf("steps = %+v", steps)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v, want two id drops", warnings)
	}
}

func TestReasonDuplicateIDDropped(t *testing.T) {
	prov := testprov.NewSeq(`[
		{"id":"dup","text":"First"},
		{"id":"dup","text":"Second"}
	]`)
	steps, _, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(steps) != 1 || steps[0].Text != "First" {
		t.Fatalf("steps = %+v, want only the first dup", steps)
	}
}

func TestReasonEmptyStepListOK(t *testing.T) {
	prov := testprov.NewSeq(`[]`)
	steps, warnings, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(steps) != 0 || steps == nil {
		t.Fatalf("steps = %#v, want empty non-nil", steps)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestReasonNilDependsOnMarshalsAsEmptyArray(t *testing.T) {
	prov := testprov.NewSeq(`[{"id":"solo","text":"No deps"}]`)
	steps, _, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if steps[0].DependsOn == nil {
		t.Fatal("depends_on should be an empty slice, not nil")
	}
}

func TestReasonRepairRetry(t *testing.T) {
	prov := testprov.NewSeq("{bad", `[{"id":"s1","text":"Recovered","depends_on":["c0"]}]`)
	steps, _, err := runReason(t, prov, "", sampleClaims())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(steps) != 1 || prov.Calls() != 2 {
		t.Fatalf("steps = %d, calls = %d", len(steps), prov.Calls())
	}
}

func TestReasonFakeTranscript(t *testing.T) {
	dir := t.TempDir()
	fixture := `[{"id":"s1","text":"Step one","depends_on":["c0"]}]`
	path := filepath.Join(dir, testJobID+".reason.json")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	prov := provider.NewFake(dir)
	steps, _, err := Run(context.Background(), prov, testJobID, sampleClaims())
	if err != nil {
		t.Fatalf("Run via fake: %v", err)
	}
	if len(steps) != 1 || steps[0].ID != "s1" {
		t.Fatalf("steps = %+v", steps)
	}
}

func TestReasonPromptListsClaims(t *testing.T) {
	p := UserPrompt(sampleClaims())
	if !strings.Contains(p, "c0: The cache key is sha256") || !strings.Contains(p, "c1: Cache entries expire") {
		t.Errorf("prompt should list claims with ids: %q", p)
	}
}
