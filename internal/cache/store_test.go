package cache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aegis-verify/aegis/internal/report"
)

func sampleReport(id string) *report.Report {
	return &report.Report{
		ID:               id,
		OK:               false,
		ExitReason:       "assertions_failed",
		Prompt:           "p",
		Output:           "o",
		Claims:           []report.Claim{{ID: "c0", Text: "t", Stance: "asserted", Grounding: "ungrounded"}},
		Steps:            []report.Step{{ID: "s0", Text: "x", DependsOn: []string{"c0"}}},
		Critique:         report.Critique{Verdict: "disagree", Notes: "n", DisputedClaimIDs: []string{"c0"}},
		CodeChecks:       []report.CodeCheck{{Kind: "go", Language: "go", OK: false, Detail: "parse error"}},
		FailedAssertions: []report.FailedAssertion{{Type: "grounded", Expected: "true", Actual: "false", Detail: "claim c0 ungrounded"}},
		Warnings:         []string{"w1"},
		Cache:            report.CacheInfo{Hit: false, Key: "feed"},
	}
}

func TestStorePutGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	key := ok64(t)

	if err := s.Put(key, sampleReport("j1")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// The file must be named <key>.json.
	if _, err := os.Stat(filepath.Join(dir, key+".json")); err != nil {
		t.Fatalf("entry file missing: %v", err)
	}

	got, when, err := s.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "j1" || got.OK || got.ExitReason != "assertions_failed" {
		t.Fatalf("scalar fidelity: %+v", got)
	}
	if len(got.FailedAssertions) != 1 || got.FailedAssertions[0].Type != "grounded" {
		t.Fatalf("failed_assertions fidelity: %+v", got.FailedAssertions)
	}
	if got.Claims[0].Stance != "asserted" || got.Critique.Verdict != "disagree" {
		t.Fatalf("nested fidelity: %+v", got)
	}
	if when.IsZero() {
		t.Fatal("cached_at missing")
	}
	if age := time.Since(when); age > time.Minute {
		t.Fatalf("cached_at stale: %v", age)
	}
	if s.Dir() != dir {
		t.Fatalf("Dir(): %q", s.Dir())
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := NewStore(t.TempDir())
	_, _, err := s.Get(ok64(t))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestStoreInvalidKey(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Put("not-a-key", sampleReport("j")); err == nil {
		t.Fatal("Put with invalid key should fail")
	}
	if _, _, err := s.Get("not-a-key"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Get with invalid key should be missing, got %v", err)
	}
}

// corrupted JSON and a bad cached_at stamp both surface as entry errors,
// not as silent misses-with-success.
func TestStoreGetCorruptedEntry(t *testing.T) {
	key := ok64(t)
	cases := []string{
		"{not json",
		`{"id":"j1"}`,
		`{"id":"j1","cached_at":"yesterday"}`,
	}
	for i, bad := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, key+".json"), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		s := NewStore(dir)
		_, _, err := s.Get(key)
		if err != nil && errors.Is(err, os.ErrNotExist) {
			t.Fatalf("case %d: corrupted entry must not masquerade as missing: %v", i, err)
		}
		if err == nil {
			t.Fatalf("case %d: corrupted entry read succeeded", i)
		}
	}
}

func TestStoreGCRoundsDotCommands(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	old, fresh := ok64(t), ok64v(t, "other output")
	if err := s.Put(old, sampleReport("old")); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(fresh, sampleReport("fresh")); err != nil {
		t.Fatal(err)
	}
	// Age the first entry manually (+72h).
	_, when, err := s.Get(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := rewriteCachedAt(filepath.Join(dir, old+".json"), when.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}

	n, err := s.GC(24 * time.Hour)
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if n != 1 {
		t.Fatalf("GC removed %d, want 1", n)
	}
	if _, _, err := s.Get(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old entry should be gone, got %v", err)
	}
	if _, _, err := s.Get(fresh); err != nil {
		t.Fatalf("fresh entry should survive: %v", err)
	}

	// GC on an absent dir is a no-op.
	if n, err := NewStore(filepath.Join(dir, "absent")).GC(time.Hour); err != nil || n != 0 {
		t.Fatalf("GC absent dir: n=%d err=%v", n, err)
	}

	// Stray entries and subdirs are ignored, not errors.
	if err := os.WriteFile(filepath.Join(dir, "stray.json"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := s.GC(time.Hour); err != nil {
		t.Fatalf("GC with strays: %v", err)
	} else if n != 0 {
		t.Fatalf("strays removed: %d", n)
	}
}

func ok64(t *testing.T) string {
	t.Helper()
	return ok64v(t, "out")
}

// ok64v derives a key with a caller-chosen output so tests can create
// two distinct entries from one job fixture.
func ok64v(t *testing.T, output string) string {
	t.Helper()
	// A valid 64-hex key derived from the real key function so tests move
	// together with the key alphabet.
	j := baseJob(t, jobYAML)
	return Key(j, output, nil)
}

func rewriteCachedAt(path string, when time.Time) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	m["cached_at"] = when.UTC().Format(time.RFC3339)
	nb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, nb, 0o644)
}
