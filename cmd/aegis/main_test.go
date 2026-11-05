package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCLI is the test harness: it invokes realMain with buffer IO and
// returns exit code, stdout, stderr.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := realMain(args, &out, &errb)
	return code, out.String(), errb.String()
}

// fakeFixtures writes the canonical happy-path fixtures for jobID.
func fakeFixtures(t *testing.T, dir, jobID string) {
	t.Helper()
	fixtures := map[string]string{
		"claims":   `[{"text":"The cache key is sha256 of prompt, output, and spec.","stance":"asserted"}]`,
		"ground":   `[{"claim_id":"c0","grounding":"supported","quote":"cache key = sha256(prompt + output + spec)"}]`,
		"reason":   `[{"id":"hash","text":"Derive the key.","depends_on":["c0"]}]`,
		"critique": `{"verdict":"agree","notes":"evidence holds","disputed_claim_ids":[]}`,
	}
	for stage, body := range fixtures {
		p := filepath.Join(dir, jobID+"."+stage+".json")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writeJobFile writes a job YAML file and returns its path.
func writeJobFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const passJobYAML = `id: job-ok
prompt: How does aegis hash a cache key?
output: The cache key is sha256 of prompt, output, and spec.
context:
  inline: |
    cache key = sha256(prompt + output + spec)
assertions:
  must_claim:
    - cache key is sha256
  grounded: true
  max_ungrounded: 0
`

const failJobYAML = `id: job-fail
prompt: How does aegis hash a cache key?
output: The cache key is stored in git.
context:
  inline: |
    cache key = sha256(prompt + output + spec)
assertions:
  must_claim:
    - cache key is sha256
`

func TestRunFakeProviderPass(t *testing.T) {
	dir := t.TempDir()
	fakeFixtures(t, dir, "job-ok")
	jobPath := writeJobFile(t, dir, "job-ok.yaml", passJobYAML)
	cdir := filepath.Join(dir, "cache")

	code, out, errOut := runCLI(t, "run", jobPath,
		"--fake-provider", "--fake-dir", dir,
		"--cache-dir", cdir, "--no-cache")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, errOut, str(out))
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stdout is not a report: %v\n%s", err, str(out))
	}
	if rep["ok"] != true || rep["exit_reason"] != "ok" {
		t.Fatalf("report ok=%v reason=%v", rep["ok"], rep["exit_reason"])
	}
	if _, ok := rep["cache"].(map[string]any)["key"]; !ok {
		t.Fatal("report missing cache.key")
	}
}

func TestRunFakeProviderAssertionsFailExit1(t *testing.T) {
	dir := t.TempDir()
	fakeFixtures(t, dir, "job-fail")
	// ground fixture for job-fail: the claim does not exist in the job
	// with id job-fail... fixtures are keyed by job id, write them.
	fakeFixtures(t, dir, "job-fail")
	jobPath := writeJobFile(t, dir, "job-fail.yaml", failJobYAML)

	code, out, _ := runCLI(t, "run", jobPath,
		"--fake-provider", "--fake-dir", dir,
		"--cache-dir", filepath.Join(dir, "cache"), "--no-cache")
	if code != 1 {
		t.Fatalf("exit=%d stdout=%s", code, str(out))
	}
	var rep map[string]any
	_ = json.Unmarshal([]byte(out), &rep)
	if rep["ok"] != false {
		t.Fatalf("ok=%v", rep["ok"])
	}
}

func TestRunExit2Cases(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing job file", func(t *testing.T) {
		code, _, errOut := runCLI(t, "run", filepath.Join(dir, "nope.yaml"))
		if code != 2 || !strings.Contains(errOut, "job-read-failed") {
			t.Fatalf("exit=%d err=%s", code, str(errOut))
		}
	})

	t.Run("bad yaml", func(t *testing.T) {
		p := writeJobFile(t, dir, "bad.yaml", "id: [unclosed")
		code, out, errOut := runCLI(t, "run", p, "--fake-provider", "--fake-dir", dir)
		if code != 2 || !strings.Contains(errOut, "invalid-job") {
			t.Fatalf("exit=%d err=%s", code, str(errOut))
		}
		// A1 load branch: a job that never parsed leaves stdout empty.
		if strings.TrimSpace(out) != "" {
			t.Fatalf("load-stage failure must leave stdout empty, got %.200s", str(out))
		}
	})

	t.Run("missing fake dir flag", func(t *testing.T) {
		p := writeJobFile(t, dir, "j.yaml", "id: j\nprompt: p\noutput: o\n")
		code, _, errOut := runCLI(t, "run", p, "--fake-provider")
		if code != 2 || !strings.Contains(errOut, "requires --fake-dir") {
			t.Fatalf("exit=%d err=%s", code, str(errOut))
		}
	})

	t.Run("missing fake fixture", func(t *testing.T) {
		empty := filepath.Join(dir, "empty-fixtures")
		if err := os.MkdirAll(empty, 0o755); err != nil {
			t.Fatal(err)
		}
		p := writeJobFile(t, dir, "need.yaml", "id: need\nprompt: p\noutput: o\n")
		code, _, errOut := runCLI(t, "run", p, "--fake-provider", "--fake-dir", empty)
		if code != 2 || !strings.Contains(errOut, "fake-fixture-missing") {
			t.Fatalf("exit=%d err=%s", code, str(errOut))
		}
	})

	t.Run("missing USER_LLM_*", func(t *testing.T) {
		t.Setenv("USER_LLM_API_KEY", "")
		t.Setenv("USER_LLM_BASE_URL", "")
		t.Setenv("USER_LLM_MODEL", "")
		p := writeJobFile(t, dir, "live.yaml", "id: live\nprompt: p\noutput: o\n")
		code, _, errOut := runCLI(t, "run", p, "--no-cache")
		if code != 2 || !strings.Contains(errOut, "USER_LLM_API_KEY") || strings.Contains(errOut, "sk-") {
			t.Fatalf("exit=%d err=%s", code, str(errOut))
		}
	})
}

func TestRunCacheOnlyZeroCallsAndGC(t *testing.T) {
	dir := t.TempDir()
	fakeFixtures(t, dir, "job-ok")
	jobPath := writeJobFile(t, dir, "job-ok.yaml", passJobYAML)
	cdir := filepath.Join(dir, "cache")

	// Seed the cache with one default-mode run.
	if code, _, errOut := runCLI(t, "run", jobPath,
		"--fake-provider", "--fake-dir", dir, "--cache-dir", cdir); code != 0 {
		t.Fatalf("seed run exit=%d err=%s", code, str(errOut))
	}
	entryName := firstJSON(t, cdir)

	// cache-only replay: no fake dir provided at all, so ANY provider
	// attempt would fail loudly. Must succeed purely from cache.
	code, out, errOut := runCLI(t, "run", jobPath, "--cache-only", "--cache-dir", cdir)
	if code != 0 {
		t.Fatalf("cache-only exit=%d err=%s", code, str(errOut))
	}
	var rep map[string]any
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("bad replay report: %v", err)
	}
	if rep["cache"].(map[string]any)["hit"] != true {
		t.Fatalf("cache-only hit=%v", rep["cache"].(map[string]any)["hit"])
	}

	// --offline alias behaves identically.
	code, out2, _ := runCLI(t, "run", jobPath, "--offline", "--cache-dir", cdir)
	if code != 0 {
		t.Fatalf("offline exit=%d", code)
	}
	if !strings.Contains(out2, `"hit": true`) {
		t.Fatalf("offline replay not a hit:\n%s", str(out2))
	}

	// cache-only miss is exit 2 and mentions the kind; A1 resolved-branch
	// behavior: the partial engine_error JSON goes to stdout (job parsed).
	missJob := writeJobFile(t, dir, "miss.yaml", "id: miss-job\nprompt: p\noutput: o\n")
	var missOut string
	code, missOut, errOut = runCLI(t, "run", missJob, "--cache-only", "--cache-dir", cdir)
	if code != 2 || !strings.Contains(errOut, "cache-miss") {
		t.Fatalf("miss exit=%d err=%s", code, str(errOut))
	}
	var missRep map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(missOut)), &missRep); err != nil {
		t.Fatalf("cache-miss stdout is not the engine_error JSON line: %v\n%.200s", err, str(missOut))
	}
	if missRep["id"] != "miss-job" || missRep["ok"] != false || missRep["exit_reason"] != "engine_error" {
		t.Fatalf("cache-miss line malformed: %v", missRep)
	}

	// gc on the seeded dir with --older-than 0 rejected; fresh entries
	// survive a 720h sweep.
	if code, _, errOut := runCLI(t, "cache", "gc"); code != 2 || !strings.Contains(errOut, "older-than") {
		t.Fatalf("gc no-flag exit=%d err=%s", code, str(errOut))
	}
	if code, out, _ := runCLI(t, "cache", "gc", "--older-than", "720h", "--cache-dir", cdir); code != 0 {
		t.Fatalf("gc exit code %d", code)
	} else if !strings.Contains(out, "removed 0") {
		t.Fatalf("gc output=%s", str(out))
	}
	if _, err := os.Stat(filepath.Join(cdir, entryName)); err != nil {
		t.Fatal("gc deleted a fresh entry")
	}
}

func TestRunOutputsAndRecordFlag(t *testing.T) {
	dir := t.TempDir()
	fakeFixtures(t, dir, "job-ok")
	jobPath := writeJobFile(t, dir, "job-ok.yaml", passJobYAML)
	cdir := filepath.Join(dir, "cache")
	reportPath := filepath.Join(dir, "report.json")
	junitPath := filepath.Join(dir, "junit.xml")

	code, stdout, _ := runCLI(t, "run", jobPath,
		"--fake-provider", "--fake-dir", dir, "--cache-dir", cdir,
		"--record", "-o", reportPath, "--junit", junitPath)
	if code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("with -o, stdout must stay empty, got %q", str(stdout))
	}
	if _, err := os.Stat(reportPath); err != nil {
		t.Fatalf("-o report not written: %v", err)
	}
	jb, err := os.ReadFile(junitPath)
	if err != nil || !strings.Contains(string(jb), "<testsuite") {
		t.Fatalf("junit not written: err=%v body=%.80s", err, string(jb))
	}

	// --no-cache followed by --cache-only must miss (no entry written).
	cdir2 := filepath.Join(dir, "cache2")
	if code, _, _ := runCLI(t, "run", jobPath, "--fake-provider", "--fake-dir", dir,
		"--cache-dir", cdir2, "--no-cache"); code != 0 {
		t.Fatal("no-cache run failed")
	}
	if code, _, errOut := runCLI(t, "run", jobPath, "--cache-only", "--cache-dir", cdir2); code != 2 {
		t.Fatalf("cache-only after no-cache must miss, exit=%d err=%s", code, str(errOut))
	}
}

func TestHelpAndUnknownCommand(t *testing.T) {
	code, out, _ := runCLI(t, "--help")
	if code != 0 || !strings.Contains(out, "aegis run") {
		t.Fatalf("help exit=%d out=%.100s", code, str(out))
	}
	code, out, _ = runCLI(t, "version")
	if code != 0 || !strings.Contains(out, "aegis version") {
		t.Fatalf("version exit=%d out=%.100s", code, str(out))
	}
	code, _, errOut := runCLI(t, "frobnicate")
	if code != 2 || !strings.Contains(errOut, "unknown command") {
		t.Fatalf("unknown cmd exit=%d err=%s", code, str(errOut))
	}
}

func firstJSON(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			return e.Name()
		}
	}
	t.Fatal("no cache entries")
	return ""
}

func str(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

var _ = fmt.Sprintf // guard: fmt used by helpers above may drift
