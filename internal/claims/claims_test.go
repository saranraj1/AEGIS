package claims

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/testprov"
)

const testJobID = "job1"

// writeFake stores a canned fixture under dir with the fake-provider
// file naming convention {job-id}.{stage}.json.
func writeFake(t *testing.T, dir, jobID, stage, content string) {
	t.Helper()
	path := filepath.Join(dir, jobID+"."+stage+".json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func extract(t *testing.T, prov provider.Provider, output string) ([]report.Claim, []string, error) {
	t.Helper()
	return Extract(context.Background(), prov, testJobID, output)
}

func TestExtractHappyPath(t *testing.T) {
	output := "The cache key is sha256 of prompt, output, and spec. Cache entries may expire after 24 hours."
	content := `[
		{"text":"The cache key is sha256 of prompt, output, and spec.","stance":"asserted"},
		{"text":"Cache entries may expire after 24 hours.","stance":"hedged"}
	]`
	prov := testprov.NewSeq(content)

	claims, warnings, err := extract(t, prov, output)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(claims))
	}
	if claims[0].ID != "c0" || claims[1].ID != "c1" {
		t.Errorf("ids = %q, %q", claims[0].ID, claims[1].ID)
	}
	if claims[0].Stance != report.StanceAsserted || claims[1].Stance != report.StanceHedged {
		t.Errorf("stances = %q, %q", claims[0].Stance, claims[1].Stance)
	}
	if claims[0].Evidence == nil || len(claims[0].Evidence) != 0 {
		t.Errorf("evidence should be non-nil and empty")
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
	// The stage call must be routed to the claims stage with the job id.
	if prov.Requests[0].Stage != provider.StageClaims || prov.Requests[0].JobID != testJobID {
		t.Errorf("request = %+v", prov.Requests[0])
	}
}

func TestExtractDropInventedClaims(t *testing.T) {
	output := "Aegis hashes the cache key with sha256."
	content := `[
		{"text":"Aegis hashes the cache key with sha256.","stance":"asserted"},
		{"text":"The cache is stored in git.","stance":"asserted"},
		{"text":"aegis HASHES the cache key with   sha256.","stance":"hedged"}
	]`
	prov := testprov.NewSeq(content)

	claims, warnings, err := extract(t, prov, output)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// Claim 1 is invented (dropped). Claim 2 is a normalized-substring
	// variant of claim 0, so it merges with it (asserted preferred over
	// hedged anyway, and both merge into the first occurrence).
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(claims))
	}
	if claims[0].Stance != report.StanceAsserted {
		t.Errorf("stance = %q, want asserted kept", claims[0].Stance)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "invented") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the invented drop: %v", warnings)
	}
}

func TestExtractInventedRequiresNormalizedSubstring(t *testing.T) {
	// Normalization (NFKC, lowercase, whitespace collapse) keeps a claim
	// whose text differs only in case/spacing.
	output := "Cache keys use SHA256."
	content := `[{"text":"cache keys use  sha256","stance":"asserted"}]`
	prov := testprov.NewSeq(content)
	claims, _, err := extract(t, prov, output)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(claims))
	}
}

func TestExtractMergeDuplicatesKeepsAsserted(t *testing.T) {
	output := "The sky is blue. The sky is blue."
	content := `[
		{"text":"The sky is blue","stance":"hedged"},
		{"text":"the sky is blue","stance":"asserted"}
	]`
	prov := testprov.NewSeq(content)
	claims, _, err := extract(t, prov, output)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1 after merge", len(claims))
	}
	if claims[0].Stance != report.StanceAsserted {
		t.Errorf("stance = %q, want asserted", claims[0].Stance)
	}
}

func TestExtractUnknownStanceCoerced(t *testing.T) {
	output := "Go compiles to native binaries."
	content := `[{"text":"Go compiles to native binaries.","stance":"certain"}]`
	prov := testprov.NewSeq(content)
	claims, warnings, err := extract(t, prov, output)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(claims) != 1 || claims[0].Stance != report.StanceAsserted {
		t.Fatalf("claims = %+v", claims)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unknown stance") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the coercion: %v", warnings)
	}
}

func TestExtractRepairRetry(t *testing.T) {
	output := "Aegis is fast."
	good := `[{"text":"Aegis is fast.","stance":"asserted"}]`
	prov := testprov.NewSeq("}broken{", good)
	claims, _, err := extract(t, prov, output)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(claims) != 1 || prov.Calls() != 2 {
		t.Fatalf("claims = %d, calls = %d, want 1 claim after one repair", len(claims), prov.Calls())
	}
}

func TestExtractRepairFails(t *testing.T) {
	prov := testprov.NewSeq("}broken{", "still broken")
	_, _, err := extract(t, prov, "Aegis is fast.")
	if err == nil {
		t.Fatal("expected hard error after failed repair")
	}
	if prov.Calls() != 2 {
		t.Errorf("calls = %d, want exactly 2", prov.Calls())
	}
}

func TestExtractEmptyOutputYieldsNoClaims(t *testing.T) {
	prov := testprov.NewSeq("[]")
	claims, _, err := extract(t, prov, "")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(claims) != 0 || claims == nil {
		t.Fatalf("claims = %#v, want empty non-nil", claims)
	}
}

// TestExtractFakeTranscript pins the fake-provider transcript path: the
// stage consumes the fixture file {job}.{stage}.json verbatim.
func TestExtractFakeTranscript(t *testing.T) {
	dir := t.TempDir()
	fixture := `[{"text":"Aegis uses sha256.","stance":"asserted"}]`
	writeFake(t, dir, testJobID, "claims", fixture)
	prov := provider.NewFake(dir)
	claims, _, err := extract(t, prov, "Aegis uses sha256.")
	if err != nil {
		t.Fatalf("Extract via fake: %v", err)
	}
	if len(claims) != 1 || claims[0].ID != "c0" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestSeedSentences(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []string
	}{
		{"sentences", "One. Two! Three?", []string{"One.", "Two!", "Three?"}},
		{"bullets", "- alpha\n* beta\n1. gamma\n2) delta", []string{"alpha", "beta", "gamma", "delta"}},
		{"mixed", "Intro line.\n- bullet one", []string{"Intro line.", "bullet one"}},
		{"empty", "  \n\n", nil},
		{"decimal stays", "Pi is 3.14 exactly.", []string{"Pi is 3.14 exactly."}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SeedSentences(tt.output)
			if len(got) != len(tt.want) {
				t.Fatalf("seeds = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("seed[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBlankEntriesDroppedSilently(t *testing.T) {
	output := "Aegis is fast."
	content := `[{"text":"  ","stance":"asserted"},{"text":"Aegis is fast.","stance":"asserted"}]`
	prov := testprov.NewSeq(content)
	claims, warnings, err := extract(t, prov, output)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(claims))
	}
	for _, w := range warnings {
		if strings.Contains(w, "invented") {
			t.Errorf("blank entry must not count as invented: %v", warnings)
		}
	}
}

// TestProviderErrorPassthrough pins that provider-level errors (e.g.
// timeouts) are never swallowed by the stage.
func TestProviderErrorPassthrough(t *testing.T) {
	perr := &provider.Error{Kind: provider.KindTimeout, Msg: "slow"}
	prov := testprov.NewSeq("").ThenErr(perr)
	_, _, err := extract(t, prov, "Aegis is fast.")
	var got *provider.Error
	if !errors.As(err, &got) || got.Kind != provider.KindTimeout {
		t.Fatalf("got %v, want provider timeout passthrough", err)
	}
}
