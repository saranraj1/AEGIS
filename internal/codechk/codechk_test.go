package codechk

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// pythonPath reports a usable python binary for subprocess tests, or "".
func pythonPath(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no python interpreter available; skipping subprocess tests")
	return ""
}

func TestGoFenceParses(t *testing.T) {
	checks, warnings := Run(context.Background(),
		"```go\npackage main\n\nfunc main() {}\n```\n", false, Options{})
	if len(checks) != 1 || !checks[0].OK || checks[0].Language != "go" {
		t.Fatalf("checks = %+v", checks)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestGoFenceFragmentWrapped(t *testing.T) {
	checks, _ := Run(context.Background(),
		"```golang\nx := 1\ny := x + 2\n```\n", false, Options{})
	if len(checks) != 1 || !checks[0].OK {
		t.Fatalf("checks = %+v, want ok via function wrapper", checks)
	}
}

func TestNamedGoFenceFailureCounts(t *testing.T) {
	checks, _ := Run(context.Background(),
		"```go\nfunc broken( {\n```\n", false, Options{})
	if len(checks) != 1 || checks[0].OK {
		t.Fatalf("checks = %+v, want failing go check", checks)
	}
	if checks[0].Detail == "" {
		t.Error("failing check should carry a parse detail")
	}
}

func TestPythonFenceWithRealPython(t *testing.T) {
	py := pythonPath(t)
	checks, warnings := Run(context.Background(),
		"```python\nx = [1, 2, 3]\nprint(sum(x))\n```\n", false, Options{PythonBin: py})
	if len(checks) != 1 || !checks[0].OK || checks[0].Language != "python" {
		t.Fatalf("checks = %+v", checks)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestPythonFenceSyntaxErrorFails(t *testing.T) {
	py := pythonPath(t)
	checks, _ := Run(context.Background(),
		"```python\ndef broken(:\n```\n", false, Options{PythonBin: py})
	if len(checks) != 1 || checks[0].OK {
		t.Fatalf("checks = %+v, want failing python check", checks)
	}
}

func TestMissingPythonForcedRecordsFailure(t *testing.T) {
	checks, warnings := Run(context.Background(),
		"```python\nx = 1\n```\n", true, Options{PythonBin: "definitely-not-python-xyz"})
	if len(checks) != 1 || checks[0].OK || checks[0].Language != "python" {
		t.Fatalf("checks = %+v, want failing python check", checks)
	}
	if checks[0].Detail != "python3 not found" {
		t.Errorf("detail = %q", checks[0].Detail)
	}
	if len(warnings) != 0 {
		t.Errorf("forced mode must not warn: %v", warnings)
	}
}

func TestMissingPythonAutoDetectWarns(t *testing.T) {
	checks, warnings := Run(context.Background(),
		"```python\nx = 1\n```\n", false, Options{PythonBin: "definitely-not-python-xyz"})
	if len(checks) != 0 {
		t.Fatalf("checks = %+v, want none in auto-detect mode", checks)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "python3 not found") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestThreeLineRunDetectedAsGo(t *testing.T) {
	output := "Here is the fix:\n\nfunc helper() int {\n\treturn 42\n}\n\nDone."
	checks, _ := Run(context.Background(), output, false, Options{})
	if len(checks) != 1 || !checks[0].OK || checks[0].Language != "go" {
		t.Fatalf("checks = %+v, want one ok go check", checks)
	}
}

func TestTwoLineRunNotDetected(t *testing.T) {
	// Two lines that WOULD parse if the threshold were lower — the run
	// heuristic requires 3+ consecutive lines.
	output := "package p\nfunc f() {}"
	checks, _ := Run(context.Background(), output, false, Options{})
	if len(checks) != 0 {
		t.Fatalf("checks = %+v, want none (fewer than 3 lines)", checks)
	}
}

func TestUnknownLanguageFenceIgnored(t *testing.T) {
	checks, warnings := Run(context.Background(),
		"```ruby\ndef foo; end\n```\n", false, Options{})
	if len(checks) != 0 || len(warnings) != 0 {
		t.Fatalf("checks = %+v, warnings = %v, want none", checks, warnings)
	}
}

func TestEmptyLanguageFenceAutoDetectedAsGo(t *testing.T) {
	checks, _ := Run(context.Background(),
		"```\npackage p\n\nfunc f() {}\n```\n", false, Options{})
	if len(checks) != 1 || !checks[0].OK || checks[0].Language != "go" {
		t.Fatalf("checks = %+v", checks)
	}
}

func TestProseOnlyOutputNoChecks(t *testing.T) {
	output := "Aegis hashes the cache key with sha256.\nIt stores entries on disk.\nThis is just prose, not code."
	checks, warnings := Run(context.Background(), output, false, Options{})
	if len(checks) != 0 || len(warnings) != 0 {
		t.Fatalf("checks = %+v, warnings = %v, want none", checks, warnings)
	}
}

func TestForceWithNoCodePasses(t *testing.T) {
	checks, _ := Run(context.Background(), "Just prose.\nStill prose.\nMore prose.", true, Options{})
	if len(checks) != 0 {
		t.Fatalf("checks = %+v, want none", checks)
	}
}

func TestMissingPythonRuleUsesRealLookup(t *testing.T) {
	// Without an explicit PythonBin, a system without python3 must take the
	// missing path. If python3 exists here, pin the found path instead.
	found, py := resolvePython("")
	if !found {
		checks, _ := Run(context.Background(), "```python\nx = 1\n```\n", false, Options{})
		if len(checks) != 0 {
			t.Fatalf("checks = %+v", checks)
		}
		return
	}
	if _, err := os.Stat(py); err != nil {
		t.Fatalf("resolved python %q missing: %v", py, err)
	}
}

func TestResolvePythonCandidateOrder(t *testing.T) {
	// Explicit non-existent name must immediately return not found
	found, _ := resolvePython("non-existent-binary-for-test-xyz")
	if found {
		t.Fatal("expected non-existent binary to not be found")
	}

	// Default lookup checks python3, python, and py
	found, py := resolvePython("")
	if found && py == "" {
		t.Fatal("found is true but returned path is empty")
	}
}
