package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunGoldenRegen is not part of CI: with AEGIS_REGEN_GOLDEN=1 it
// reruns the golden job and rewrites the committed golden report so
// intentional pipeline/report changes can refresh it deliberately.
func TestRunGoldenRegen(t *testing.T) {
	if os.Getenv("AEGIS_REGEN_GOLDEN") != "1" {
		t.Skip("set AEGIS_REGEN_GOLDEN=1 to regenerate testdata/reports/explain-cache-key.golden.json")
	}
	root, _ := os.Getwd()
	jobPath := filepath.Join(root, "..", "..", "testdata", "jobs", "explain-cache-key.yaml")
	fakeDir := filepath.Join(root, "..", "..", "testdata", "fixtures")
	goldenPath := filepath.Join(root, "..", "..", "testdata", "reports", "explain-cache-key.golden.json")

	dir := t.TempDir()
	code, out, errOut := runCLI(t, "run", jobPath,
		"--fake-provider", "--fake-dir", fakeDir,
		"--cache-dir", filepath.Join(dir, "cache"), "--no-cache")
	if code != 0 {
		t.Fatalf("regen exit=%d stderr=%s", code, errOut)
	}
	if err := os.WriteFile(goldenPath, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}
