package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aegis-verify/aegis/internal/report"
)

func TestFakeLoadFixture(t *testing.T) {
	dir := t.TempDir()
	want := `[{"id":"c1","text":"claim one"}]`
	if err := os.WriteFile(filepath.Join(dir, "job1.claims.json"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	f := NewFake(dir)
	resp, err := f.Call(context.Background(), Request{Stage: StageClaims, JobID: "job1"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content != want {
		t.Errorf("content = %q, want %q", resp.Content, want)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", resp.Warnings)
	}
}

func TestFakeMissingFixture(t *testing.T) {
	dir := t.TempDir()
	f := NewFake(dir)
	_, err := f.Call(context.Background(), Request{Stage: StageGround, JobID: "job9"})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindFakeMissing {
		t.Fatalf("got %v, want kind %s", err, KindFakeMissing)
	}
	for _, want := range []string{"job9", "ground"} {
		if !strings.Contains(perr.Msg, want) {
			t.Errorf("error should mention %q: %q", want, perr.Msg)
		}
	}
}

func TestFakeUnreadableFixture(t *testing.T) {
	dir := t.TempDir()
	// A directory at the fixture path makes ReadFile fail without ENOENT.
	if err := os.Mkdir(filepath.Join(dir, "job1.wrap.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := NewFake(dir)
	_, err := f.Call(context.Background(), Request{Stage: StageWrap, JobID: "job1"})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindFakeUnreadable {
		t.Fatalf("got %v, want kind %s", err, KindFakeUnreadable)
	}
}

func TestFakeNeedsJobID(t *testing.T) {
	f := NewFake(t.TempDir())
	_, err := f.Call(context.Background(), Request{Stage: StageWrap})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindBadRequest {
		t.Fatalf("got %v, want kind %s", err, KindBadRequest)
	}
}

func TestFakeInvalidStage(t *testing.T) {
	f := NewFake(t.TempDir())
	_, err := f.Call(context.Background(), Request{Stage: "nope", JobID: "j"})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindBadRequest {
		t.Fatalf("got %v, want kind %s", err, KindBadRequest)
	}
}

// TestFakeIgnoresEnv pins the contract that the fake provider works with
// USER_LLM_* unset (it must never require live-call credentials).
func TestFakeIgnoresEnv(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "j.wrap.json"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := NewFake(dir)
	if _, err := f.Call(context.Background(), Request{Stage: StageWrap, JobID: "j"}); err != nil {
		t.Fatalf("fake call should not need USER_LLM_*: %v", err)
	}
}

func TestDecodeGroundValid(t *testing.T) {
	data := `[
		{"claim_id":"c1","grounding":"supported","quote":"the cache key is sha256"},
		{"claim_id":"c2","grounding":"not-in-context"},
		{"grounding":"contradicted","quote":"git is never used for storage"}
	]`
	recs, err := DecodeGround([]byte(data))
	if err != nil {
		t.Fatalf("DecodeGround: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("records = %d, want 3", len(recs))
	}
	if recs[0].ClaimID != "c1" || recs[0].Grounding != report.Grounded || recs[0].Quote == "" {
		t.Errorf("record 0 = %+v", recs[0])
	}
	if recs[1].ClaimID != "c2" || recs[1].Quote != "" {
		t.Errorf("record 1 = %+v", recs[1])
	}
	// ClaimID-less record (A11 order binding) parses fine.
	if recs[2].ClaimID != "" || recs[2].Grounding != report.Contradicted {
		t.Errorf("record 2 = %+v", recs[2])
	}
}

func TestDecodeGroundSupportedRequiresQuote(t *testing.T) {
	data := `[{"claim_id":"c1","grounding":"supported"}]`
	_, err := DecodeGround([]byte(data))
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindBadResponse {
		t.Fatalf("got %v, want kind %s", err, KindBadResponse)
	}
	if !strings.Contains(perr.Msg, "requires a quote") {
		t.Errorf("message = %q", perr.Msg)
	}
}

func TestDecodeGroundUnknownGrounding(t *testing.T) {
	data := `[{"claim_id":"c1","grounding":"maybe","quote":"q"}]`
	_, err := DecodeGround([]byte(data))
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindBadResponse {
		t.Fatalf("got %v, want kind %s", err, KindBadResponse)
	}
	if !strings.Contains(perr.Msg, "unknown grounding") {
		t.Errorf("message = %q", perr.Msg)
	}
}

func TestDecodeGroundRejectsUnknownFields(t *testing.T) {
	data := `[{"claim_id":"c1","grounding":"not-in-context","confidence":0.9}]`
	_, err := DecodeGround([]byte(data))
	if err == nil {
		t.Fatal("expected unknown-field rejection")
	}
}

func TestDecodeGroundBadJSON(t *testing.T) {
	_, err := DecodeGround([]byte(`{not an array`))
	if err == nil {
		t.Fatal("expected decode error")
	}
}
