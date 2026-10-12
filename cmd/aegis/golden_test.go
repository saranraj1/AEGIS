package main

import (
	"encoding/json"
	"os"
	"path/filepath"
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
