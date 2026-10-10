package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aegis-verify/aegis/internal/job"
	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/testprov"
)

func writeFake(t *testing.T, dir, jobID, stage, content string) {
	t.Helper()
	path := filepath.Join(dir, jobID+"."+stage+".json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadJob(t *testing.T, src string) *job.Job {
	t.Helper()
	j, err := job.Load([]byte(src))
	if err != nil {
		t.Fatalf("job.Load: %v", err)
	}
	return j
}

const fullJobYAML = `
id: explain-cache-key
prompt: How does aegis hash a cache key?
output: |
  The cache key is sha256 of prompt, output, and spec.
context:
  inline: |
    cache key = sha256(prompt + output + spec)
assertions:
  must_claim:
    - cache key is sha256
  grounded: true
  max_ungrounded: 0
`

func fullFixtures(t *testing.T, dir, jobID string) {
	t.Helper()
	writeFake(t, dir, jobID, "claims",
		`[{"text":"The cache key is sha256 of prompt, output, and spec.","stance":"asserted"}]`)
	writeFake(t, dir, jobID, "ground",
		`[{"claim_id":"c0","grounding":"supported","quote":"cache key = sha256(prompt + output + spec)"}]`)
	writeFake(t, dir, jobID, "reason",
		`[{"id":"hash","text":"Derive the key.","depends_on":["c0"]}]`)
	writeFake(t, dir, jobID, "critique",
		`{"verdict":"agree","notes":"evidence holds","disputed_claim_ids":[]}`)
}

func TestRunHappyPathFake(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	j := loadJob(t, fullJobYAML)

	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK || rep.ExitReason != "ok" {
		t.Fatalf("ok=%v reason=%s failed=%+v", rep.OK, rep.ExitReason, rep.FailedAssertions)
	}
	if len(rep.Claims) != 1 || rep.Claims[0].ID != "c0" || rep.Claims[0].Grounding != report.Grounded {
		t.Fatalf("claims = %+v", rep.Claims)
	}
	if len(rep.Claims[0].Evidence) != 1 || rep.Claims[0].Evidence[0].Path != InlinePath {
		t.Errorf("evidence = %+v", rep.Claims[0].Evidence)
	}
	if len(rep.Steps) != 1 || rep.Steps[0].ID != "hash" {
		t.Errorf("steps = %+v", rep.Steps)
	}
	if rep.Critique.Verdict != report.VerdictAgree {
		t.Errorf("critique = %+v", rep.Critique)
	}
	if rep.Cache.Hit || len(rep.Cache.Key) != 64 {
		t.Errorf("cache = %+v", rep.Cache)
	}
	if len(rep.FailedAssertions) != 0 {
		t.Errorf("failed = %+v", rep.FailedAssertions)
	}
}

func TestRunAssertionFailure(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "j1")
	j := loadJob(t, `
id: j1
prompt: p
output: The cache key is sha256 of prompt, output, and spec.
assertions:
  must_claim:
    - cache is stored in git
`)
	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.OK || rep.ExitReason != "assertions_failed" {
		t.Fatalf("ok=%v reason=%s", rep.OK, rep.ExitReason)
	}
	if len(rep.FailedAssertions) != 1 || rep.FailedAssertions[0].Type != report.ATMustClaim {
		t.Fatalf("failed = %+v", rep.FailedAssertions)
	}
}

func TestRunWrapWhenOutputMissing(t *testing.T) {
	seq := testprov.NewSeq(
		"Aegis hashes the cache key with sha256.",                                  // wrap
		`[{"text":"Aegis hashes the cache key with sha256.","stance":"asserted"}]`, // claims
		`[{"claim_id":"c0","grounding":"not-in-context"}]`,                         // ground
		`[]`, // reason
		`{"verdict":"agree","notes":"","disputed_claim_ids":[]}`, // critique
	)
	j := loadJob(t, `
id: j1
prompt: How does aegis hash a cache key?
`)
	rep, err := Run(context.Background(), j, seq, Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Output != "Aegis hashes the cache key with sha256." {
		t.Errorf("output = %q", rep.Output)
	}
	if seq.Calls() != 5 {
		t.Fatalf("calls = %d, want 5 (wrap, claims, ground, reason, critique)", seq.Calls())
	}
	stages := []provider.Stage{}
	for _, r := range seq.Requests {
		stages = append(stages, r.Stage)
	}
	want := []provider.Stage{provider.StageWrap, provider.StageClaims, provider.StageGround, provider.StageReason, provider.StageCritique}
	for i := range want {
		if stages[i] != want[i] {
			t.Errorf("stage %d = %s, want %s", i, stages[i], want[i])
		}
	}
	// No assertions declared: the audit still runs and passes.
	if !rep.OK || rep.ExitReason != "ok" {
		t.Errorf("ok=%v reason=%s failed=%+v", rep.OK, rep.ExitReason, rep.FailedAssertions)
	}
}

func TestRunWrapFlagIgnoresOutput(t *testing.T) {
	seq := testprov.NewSeq(
		"wrapped answer",
		`[{"text":"wrapped answer","stance":"asserted"}]`,
		`[]`,
		`[]`,
		`{"verdict":"agree","notes":"","disputed_claim_ids":[]}`,
	)
	j := loadJob(t, `
id: j1
prompt: p
output: provided answer
model:
  wrap: true
`)
	rep, err := Run(context.Background(), j, seq, Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Output != "wrapped answer" {
		t.Errorf("output = %q, want wrap result", rep.Output)
	}
}

func TestRunWrapEmptyIsEngineError(t *testing.T) {
	seq := testprov.NewSeq("   ")
	j := loadJob(t, "id: j1\nprompt: p\n")
	_, err := Run(context.Background(), j, seq, Options{Cache: CacheOff})
	var eerr *Error
	if !errors.As(err, &eerr) || eerr.Kind != KindWrapEmpty {
		t.Fatalf("got %v, want kind %s", err, KindWrapEmpty)
	}
}

func TestRunMissingContextFile(t *testing.T) {
	j := loadJob(t, `
id: j1
prompt: p
output: out
context:
  files:
    - nope/missing.go
`)
	_, err := Run(context.Background(), j, testprov.NewSeq(), Options{Cache: CacheOff})
	var eerr *Error
	if !errors.As(err, &eerr) || eerr.Kind != KindContextMissing {
		t.Fatalf("got %v, want kind %s", err, KindContextMissing)
	}
	if !strings.Contains(eerr.Msg, "nope/missing.go") {
		t.Errorf("msg = %q, should name the file", eerr.Msg)
	}
}

func TestRunGroundTimeoutMarksUngroundedNotError(t *testing.T) {
	timeout := &provider.Error{Kind: provider.KindTimeout, Msg: "stage timeout"}
	seq := testprov.NewSeq(
		`[{"text":"The cache key is sha256.","stance":"asserted"}]`, // claims
	).ThenErr(timeout). // ground batch times out
				Then(`[]`).                                                    // reason
				Then(`{"verdict":"agree","notes":"","disputed_claim_ids":[]}`) // critique
	j := loadJob(t, `
id: j1
prompt: p
output: The cache key is sha256.
assertions:
  must_claim:
    - cache key is sha256
`)
	rep, err := Run(context.Background(), j, seq, Options{StageTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("ground timeout must not be an engine error, got: %v", err)
	}
	if len(rep.Claims) != 1 || rep.Claims[0].Grounding != report.Ungrounded || rep.Claims[0].GroundingReason != report.ReasonTimeout {
		t.Fatalf("claims = %+v", rep.Claims)
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "timeout") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings should note the timeout: %v", rep.Warnings)
	}
}

func TestRunCodeParseFailureAppendsAssertion(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "j1")
	j := loadJob(t, "{\"id\":\"j1\",\"prompt\":\"p\",\"output\":\"text before\\n```go\\nfunc broken( {\\n```\\ntext after\"}")
	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.CodeChecks) != 1 || rep.CodeChecks[0].OK {
		t.Fatalf("checks = %+v", rep.CodeChecks)
	}
	var codeFail bool
	for _, fa := range rep.FailedAssertions {
		if fa.Type == report.ATCodeParse {
			codeFail = true
		}
	}
	if !codeFail {
		t.Fatalf("failed = %+v, want code.parse", rep.FailedAssertions)
	}
}

func TestRunCodeParseForcedWithNoCodePasses(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "j1")
	j := loadJob(t, `
id: j1
prompt: p
output: The cache key is sha256 of prompt, output, and spec.
assertions:
  code:
    parse: true
`)
	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK || len(rep.CodeChecks) != 0 {
		t.Fatalf("ok=%v checks=%+v", rep.OK, rep.CodeChecks)
	}
}

func TestRunInventedClaimWarning(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	writeFake(t, dir, "explain-cache-key", "claims",
		`[{"text":"The cache key is sha256 of prompt, output, and spec.","stance":"asserted"},{"text":"totally invented text here","stance":"asserted"}]`)
	j := loadJob(t, fullJobYAML)
	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "invented") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want invented-claim note", rep.Warnings)
	}
}

// slowProvider blocks until its context is done, to exercise the
// whole-run timeout path.
type slowProvider struct{}

func (slowProvider) Call(ctx context.Context, _ provider.Request) (provider.Response, error) {
	<-ctx.Done()
	// Match the real provider's classification of a ctx deadline.
	return provider.Response{}, &provider.Error{Kind: provider.KindTimeout, Msg: ctx.Err().Error()}
}

func TestRunWholeRunTimeout(t *testing.T) {
	j := loadJob(t, "id: j1\nprompt: p\n")
	_, err := Run(context.Background(), j, slowProvider{}, Options{RunTimeout: 20 * time.Millisecond})
	var eerr *Error
	if !errors.As(err, &eerr) || eerr.Kind != KindRunTimeout {
		t.Fatalf("got %v, want kind %s", err, KindRunTimeout)
	}
}

func TestRunStageTimeoutOnClaimsIsEngineError(t *testing.T) {
	j := loadJob(t, "id: j1\nprompt: p\noutput: out\n")
	_, err := Run(context.Background(), j, slowProvider{}, Options{StageTimeout: 20 * time.Millisecond})
	var got *provider.Error
	if !errors.As(err, &got) || got.Kind != provider.KindTimeout {
		t.Fatalf("got %v, want provider timeout", err)
	}
}

func TestRunContextFileRead(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "j1")
	ctxFile := filepath.Join(dir, "key.go")
	if err := os.WriteFile(ctxFile, []byte("cache key = sha256(prompt + output + spec)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	j := loadJob(t, `
id: j1
prompt: p
output: The cache key is sha256 of prompt, output, and spec.
context:
  files:
    - `+ctxFile+`
assertions:
  grounded: true
`)
	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{Cache: CacheOff})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.OK {
		t.Fatalf("ok=%v failed=%+v", rep.OK, rep.FailedAssertions)
	}
	if rep.Claims[0].Evidence[0].Path != ctxFile {
		t.Errorf("evidence path = %q", rep.Claims[0].Evidence[0].Path)
	}
}
