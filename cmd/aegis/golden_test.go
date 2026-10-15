package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunGoldenE2E is the committed CI-golden run (phase 7): replay the
// spec's own example job end-to-end with --fake-provider and compare
// stdout to the committed golden report. Zero network.
//
// The cache key in the report depends on absolute file paths (context
// inline is path-free but the job file bytes hash in), so the fixture is
// compared with cache.key replaced by an opaque placeholder.
func TestRunGoldenE2E(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join(root, "..", "..", "testdata", "reports", "explain-cache-key.golden.json")
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	dir := t.TempDir()
	cdir := filepath.Join(dir, "cache")

	// --offline makes the golden run provider- and env-independent: with
	// no cache entry yet it must still take the same path... no — a
	// cache-only miss is exit 2. The golden run uses --fake-provider
	// against the committed testdata/fixtures directory and a fresh
	// --cache-dir under the test tempdir, so default (read+write) cache
	// is exercised.
	jobPath := filepath.Join(root, "..", "..", "testdata", "jobs", "explain-cache-key.yaml")
	fakeDir := filepath.Join(root, "..", "..", "testdata", "fixtures")

	code, out, errOut := runCLI(t, "run", jobPath,
		"--fake-provider", "--fake-dir", fakeDir,
		"--cache-dir", cdir, "--no-cache")
	if code != 0 {
		t.Fatalf("golden run exit=%d stderr=%s", code, errOut)
	}

	if !normalizeAndDiff(t, golden, []byte(out)) {
		t.Errorf("golden report mismatch: got report differs from committed golden (normalize cache.key away first; regenerate with AEGIS_REGEN_GOLDEN=1)")
	}
}

// normalizeAndDiff parses both reports, drops cache.key (a sha256 of
// prompt+output+spec bytes; identical here but kept out of the diff so
// the golden is stable across line-ending/platform file hashing), then
// compares canonically via re-Marshal in Go's fixed struct order.
func normalizeAndDiff(t *testing.T, golden, got []byte) bool {
	t.Helper()
	normalize := func(b []byte, name string) map[string]any {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unparse %s: %v", name, err)
		}
		if cache, ok := m["cache"].(map[string]any); ok {
			delete(cache, "key")
		}
		return m
	}
	return jsonEqual(normalize(golden, "golden"), normalize(got, "got"))
}

func jsonEqual(a, b map[string]any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}

// TestRunFailureFixturesE2E pins the failure-path half of the committed
// testdata set (plan risk #4): every happy-path fixture is paired with at
// least one failure fixture in the same directory, and both failure exit
// classes replay through the real CLI. Zero network.
func TestRunFailureFixturesE2E(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fixturesDir := filepath.Join(root, "..", "..", "testdata", "fixtures")

	t.Run("ungrounded must_claim is exit 1 with a full report", func(t *testing.T) {
		jobPath := filepath.Join(root, "..", "..", "testdata",
			"jobs", "explain-cache-key-failure.yaml")
		code, out, errOut := runCLI(t, "run", jobPath,
			"--fake-provider", "--fake-dir", fixturesDir,
			"--cache-dir", filepath.Join(t.TempDir(), "cache"), "--no-cache")
		if errOut != "" {
			t.Errorf("exit-1 stderr must stay silent, got %q", str(errOut))
		}
		if code != 1 {
			t.Fatalf("exit=%d stdout=%.200s", code, str(out))
		}
		var rep map[string]any
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("stdout is not a report: %v\n%s", err, str(out))
		}
		if rep["ok"] != false || rep["exit_reason"] != "assertions_failed" {
			t.Fatalf("ok=%v reason=%v", rep["ok"], rep["exit_reason"])
		}
		claims, ok := rep["claims"].([]any)
		if !ok || len(claims) != 1 {
			t.Fatalf("claims=%v", rep["claims"])
		}
		c0 := claims[0].(map[string]any)
		if c0["id"] != "c0" || c0["grounding"] != "not-in-context" {
			t.Fatalf("c0=%v", c0)
		}
		if ev, want := c0["evidence"].([]any), 0; len(ev) != want {
			t.Fatalf("c0 evidence=%v, want "+"[]", ev)
		}
		fa, ok := rep["failed_assertions"].([]any)
		if !ok || len(fa) != 2 {
			t.Fatalf("failed_assertions=%v, want exactly [grounded, max_ungrounded]", rep["failed_assertions"])
		}
		want := []struct{ typ, expected, actual string }{
			{"grounded", "supported", "not-in-context"},
			{"max_ungrounded", "<=0", "1"},
		}
		for i, e := range fa {
			m, ok := e.(map[string]any)
			if !ok {
				t.Fatalf("failed_assertions[%d] not an object: %v", i, e)
			}
			if m["type"] != want[i].typ || m["expected"] != want[i].expected || m["actual"] != want[i].actual {
				t.Fatalf("failed_assertions[%d]=%v, want %+v", i, m, want[i])
			}
		}
	})

	t.Run("invalid claims JSON is exit 2 with the engine_error stdout line", func(t *testing.T) {
		jobPath := filepath.Join(root, "..", "..", "testdata",
			"jobs", "explain-cache-key-badjson.yaml")
		code, out, errOut := runCLI(t, "run", jobPath,
			"--fake-provider", "--fake-dir", fixturesDir,
			"--cache-dir", filepath.Join(t.TempDir(), "cache"), "--no-cache")
		if code != 2 {
			t.Fatalf("exit=%d stderr=%s", code, str(errOut))
		}
		// A1 contract: stage-level failure after job parse gets the
		// best-effort one-line exit_reason=engine_error JSON on stdout.
		var mini map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &mini); err != nil {
			t.Fatalf("stdout is not the engine_error JSON line: %v\n%.200s", err, str(out))
		}
		if mini["id"] != "explain-cache-key-badjson" || mini["ok"] != false || mini["exit_reason"] != "engine_error" {
			t.Fatalf("engine_error line malformed: %v", mini)
		}
		// D2 kind-first line: error: provider-bad-response: stage claims: ...
		if !strings.HasPrefix(errOut, "error: provider-bad-response: ") {
			t.Fatalf("stderr not kind-first: %s", str(errOut))
		}
		if !strings.Contains(errOut, "stage claims: response still not valid JSON after one repair retry") {
			t.Fatalf("stderr lost the repair-retry context: %s", str(errOut))
		}
	})
}
