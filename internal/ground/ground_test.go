package ground

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/testprov"
)

const testJobID = "job1"

var keyGo = ContextFile{
	Path: "internal/cache/key.go",
	Text: "package main\n\nfunc key() string {\n\treturn sha256(prompt, output, spec)\n}\n",
}

func claims2() []report.Claim {
	return []report.Claim{
		{ID: "c0", Text: "The cache key is sha256 of prompt, output, and spec."},
		{ID: "c1", Text: "The cache is stored in git."},
	}
}

func runGround(t *testing.T, prov provider.Provider, claims []report.Claim, files []ContextFile, opts Options) ([]report.Claim, []string, error) {
	t.Helper()
	return Run(context.Background(), prov, testJobID, claims, files, opts)
}

func TestGroundHappyPathWithClaimIDs(t *testing.T) {
	content := `[
		{"claim_id":"c0","grounding":"supported","quote":"return sha256(prompt, output, spec)"},
		{"claim_id":"c1","grounding":"not-in-context"}
	]`
	claims, warnings, err := runGround(t, testprov.NewSeq(content), claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Grounded || claims[0].GroundingReason != "" {
		t.Errorf("c0 = %s/%s", claims[0].Grounding, claims[0].GroundingReason)
	}
	if len(claims[0].Evidence) != 1 {
		t.Fatalf("c0 evidence = %+v", claims[0].Evidence)
	}
	ev := claims[0].Evidence[0]
	if ev.Path != keyGo.Path || ev.Line != 4 || ev.Quote != "return sha256(prompt, output, spec)" {
		t.Errorf("evidence = %+v", ev)
	}
	if claims[1].Grounding != report.NotInContext || len(claims[1].Evidence) != 0 {
		t.Errorf("c1 = %s, evidence %+v", claims[1].Grounding, claims[1].Evidence)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestGroundOrderBindingWithoutClaimID(t *testing.T) {
	// A11: records without claim_id bind to the next unassigned claim in order.
	content := `[
		{"grounding":"supported","quote":"return sha256(prompt, output, spec)"},
		{"grounding":"not-in-context"}
	]`
	claims, _, err := runGround(t, testprov.NewSeq(content), claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Grounded {
		t.Errorf("c0 = %s, want supported via order binding", claims[0].Grounding)
	}
	if claims[1].Grounding != report.NotInContext {
		t.Errorf("c1 = %s", claims[1].Grounding)
	}
}

func TestGroundContradictedRequiresValidQuote(t *testing.T) {
	content := `[{"claim_id":"c0","grounding":"contradicted","quote":"the cache lives in .git"}]`
	claims, warnings, err := runGround(t, testprov.NewSeq(content), claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Ungrounded || claims[0].GroundingReason != report.ReasonInvalid {
		t.Errorf("c0 = %s/%s, want ungrounded/invalid-evidence", claims[0].Grounding, claims[0].GroundingReason)
	}
	if len(claims[0].Evidence) != 0 {
		t.Errorf("evidence must be discarded: %+v", claims[0].Evidence)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "invalid-evidence") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the rejection: %v", warnings)
	}
}

func TestGroundSupportedWithoutQuoteIsInvalidEvidence(t *testing.T) {
	content := `[{"claim_id":"c0","grounding":"supported","quote":""}]`
	claims, _, err := runGround(t, testprov.NewSeq(content), claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Ungrounded || claims[0].GroundingReason != report.ReasonInvalid {
		t.Errorf("c0 = %s/%s", claims[0].Grounding, claims[0].GroundingReason)
	}
}

func TestGroundQuoteChecksFullFilesBeyondWindows(t *testing.T) {
	// The quote lives far outside the selected windows; verification must
	// still search full files.
	big := ContextFile{Path: "big.txt", Text: strings.Repeat("filler line\n", 200) + "the zebra quote\n" + strings.Repeat("filler line\n", 200)}
	content := `[{"claim_id":"c0","grounding":"supported","quote":"the zebra quote"}]`
	claims, _, err := runGround(t, testprov.NewSeq(content), []report.Claim{{ID: "c0", Text: "unrelated claim text"}}, []ContextFile{big}, Options{TokenBudget: 30, WindowSize: 10, Stride: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Grounded {
		t.Fatalf("c0 = %s, want supported (full-file quote check)", claims[0].Grounding)
	}
	if claims[0].Evidence[0].Line != 201 {
		t.Errorf("line = %d, want 201", claims[0].Evidence[0].Line)
	}
}

func TestGroundWindowRankingSelectsRelevantWindows(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		if i >= 80 && i <= 90 {
			fmt.Fprintf(&b, "line %d mentions zebra habitat\n", i)
		} else {
			fmt.Fprintf(&b, "line %d ordinary filler\n", i)
		}
	}
	big := ContextFile{Path: "zoo.txt", Text: b.String()}
	claims := []report.Claim{{ID: "c0", Text: "Zebras live in habitat zones."}}
	prov := testprov.NewSeq(`[{"claim_id":"c0","grounding":"not-in-context"}]`)
	_, _, err := runGround(t, prov, claims, []ContextFile{big}, Options{TokenBudget: 60, WindowSize: 10, Stride: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	u := prov.Requests[0].User
	if !strings.Contains(u, "zebra habitat") {
		t.Errorf("highest-overlap window should be selected: %q", u)
	}
	if strings.Contains(u, "lines 1-10") {
		t.Errorf("irrelevant early window should not be selected: %q", u)
	}
}

func TestGroundUnknownClaimIDIgnored(t *testing.T) {
	content := `[
		{"claim_id":"cX","grounding":"supported","quote":"whatever"},
		{"claim_id":"c0","grounding":"supported","quote":"return sha256(prompt, output, spec)"}
	]`
	claims, warnings, err := runGround(t, testprov.NewSeq(content), claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Grounded {
		t.Errorf("c0 = %s", claims[0].Grounding)
	}
	if claims[1].Grounding != report.NotInContext {
		t.Errorf("c1 should stay at the default: %s", claims[1].Grounding)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "cX") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the unknown claim id: %v", warnings)
	}
}

func TestGroundTimeoutMarksOnlyThatBatch(t *testing.T) {
	timeout := &provider.Error{Kind: provider.KindTimeout, Msg: "stage timeout"}
	seq := testprov.NewSeq().ThenErr(timeout).
		Then(`[{"claim_id":"c1","grounding":"supported","quote":"return sha256(prompt, output, spec)"}]`)
	claims, warnings, err := runGround(t, seq, claims2(), []ContextFile{keyGo}, Options{BatchSize: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Ungrounded || claims[0].GroundingReason != report.ReasonTimeout {
		t.Errorf("c0 = %s/%s, want ungrounded/timeout", claims[0].Grounding, claims[0].GroundingReason)
	}
	if claims[1].Grounding != report.Grounded {
		t.Errorf("c1 = %s, want supported (later batch must still run)", claims[1].Grounding)
	}
	if seq.Calls() != 2 {
		t.Errorf("calls = %d, want 2", seq.Calls())
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "timeout") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the timeout: %v", warnings)
	}
}

func TestGroundBatchesByTwentyFive(t *testing.T) {
	var many []report.Claim
	for i := 0; i < 30; i++ {
		many = append(many, report.Claim{ID: fmt.Sprintf("c%d", i), Text: fmt.Sprintf("claim number %d", i)})
	}
	prov := testprov.NewSeq(`[]`, `[]`)
	_, _, err := runGround(t, prov, many, nil, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.Calls() != 2 {
		t.Errorf("calls = %d, want 2 (25 + 5)", prov.Calls())
	}
}

func TestGroundUnknownGroundingTriggersRepair(t *testing.T) {
	good := `[{"claim_id":"c0","grounding":"supported","quote":"return sha256(prompt, output, spec)"}]`
	prov := testprov.NewSeq(`[{"claim_id":"c0","grounding":"probably"}]`, good)
	claims, _, err := runGround(t, prov, claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if claims[0].Grounding != report.Grounded || prov.Calls() != 2 {
		t.Fatalf("c0 = %s, calls = %d", claims[0].Grounding, prov.Calls())
	}
}

func TestGroundEmptyClaimsNoCalls(t *testing.T) {
	prov := testprov.NewSeq()
	_, _, err := runGround(t, prov, nil, []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if prov.Calls() != 0 {
		t.Errorf("calls = %d, want 0", prov.Calls())
	}
}

func TestGroundFakeTranscript(t *testing.T) {
	dir := t.TempDir()
	fixture := `[{"claim_id":"c0","grounding":"supported","quote":"return sha256(prompt, output, spec)"}]`
	path := filepath.Join(dir, testJobID+".ground.json")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	claims, _, err := runGround(t, provider.NewFake(dir), claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run via fake: %v", err)
	}
	if claims[0].Grounding != report.Grounded || claims[1].Grounding != report.NotInContext {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestGroundPromptCarriesContextAndClaims(t *testing.T) {
	prov := testprov.NewSeq(`[]`)
	_, _, err := runGround(t, prov, claims2(), []ContextFile{keyGo}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	u := prov.Requests[0].User
	for _, want := range []string{"internal/cache/key.go", "c0: The cache key is sha256", "not-in-context"} {
		if !strings.Contains(u, want) {
			t.Errorf("user prompt missing %q", want)
		}
	}
}

func TestGroundWindowRankingMatchesPunctuationTokens(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		if i >= 80 && i <= 90 {
			// Attached punctuation: sha256(prompt, output)
			fmt.Fprintf(&b, "line %d code sha256(prompt, output, spec)\n", i)
		} else {
			fmt.Fprintf(&b, "line %d filler irrelevant\n", i)
		}
	}
	big := ContextFile{Path: "code.txt", Text: b.String()}
	claims := []report.Claim{{ID: "c0", Text: "Function sha256 hashes the prompt and output."}}
	prov := testprov.NewSeq(`[{"claim_id":"c0","grounding":"not-in-context"}]`)
	_, _, err := runGround(t, prov, claims, []ContextFile{big}, Options{TokenBudget: 60, WindowSize: 10, Stride: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	u := prov.Requests[0].User
	if !strings.Contains(u, "sha256(prompt") {
		t.Errorf("window containing punctuation-attached identifier should be selected: %q", u)
	}
}
