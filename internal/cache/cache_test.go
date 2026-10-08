package cache

import (
	"encoding/json"
	"testing"

	"github.com/aegis-verify/aegis/internal/job"
)

func baseJob(t *testing.T, src string) *job.Job {
	t.Helper()
	j, err := job.Load([]byte(src))
	if err != nil {
		t.Fatalf("job.Load: %v", err)
	}
	return j
}

const jobYAML = `
id: explain-cache-key
prompt: How does aegis hash a cache key?
output: The cache key is sha256 of prompt, output, and spec.
assertions:
  must_claim:
    - cache key is sha256
`

func TestKeyDeterministic(t *testing.T) {
	j := baseJob(t, jobYAML)
	a := Key(j, "out", nil)
	b := Key(j, "out", nil)
	if a != b || len(a) != 64 {
		t.Fatalf("keys %q vs %q", a, b)
	}
}

func TestKeyChangesWithOutput(t *testing.T) {
	j := baseJob(t, jobYAML)
	if Key(j, "out1", nil) == Key(j, "out2", nil) {
		t.Fatal("different outputs must produce different keys")
	}
}

func TestKeyIgnoresID(t *testing.T) {
	j1 := baseJob(t, jobYAML)
	j2 := baseJob(t, `
id: a-completely-different-id
prompt: How does aegis hash a cache key?
output: The cache key is sha256 of prompt, output, and spec.
assertions:
  must_claim:
    - cache key is sha256
`)
	if Key(j1, "out", nil) != Key(j2, "out", nil) {
		t.Fatal("id must not be part of the hash")
	}
}

func TestKeyIgnoresModelWrap(t *testing.T) {
	j1 := baseJob(t, jobYAML)
	j2 := baseJob(t, jobYAML+`
model:
  wrap: true
`)
	if Key(j1, "out", nil) != Key(j2, "out", nil) {
		t.Fatal("model.wrap must not be part of the hash")
	}
}

func TestKeyChangesWithAssertions(t *testing.T) {
	j1 := baseJob(t, jobYAML)
	j2 := baseJob(t, jobYAML+`
  grounded: true
`)
	if Key(j1, "out", nil) == Key(j2, "out", nil) {
		t.Fatal("tightening assertions must miss cache")
	}
}

func TestKeyChangesWithFileRename(t *testing.T) {
	j := baseJob(t, jobYAML)
	a := Key(j, "out", []File{{Path: "a.go", Bytes: []byte("x")}})
	b := Key(j, "out", []File{{Path: "b.go", Bytes: []byte("x")}})
	if a == b {
		t.Fatal("renaming a context file must miss cache")
	}
}

func TestKeyChangesWithFileBytes(t *testing.T) {
	j := baseJob(t, jobYAML)
	a := Key(j, "out", []File{{Path: "a.go", Bytes: []byte("x")}})
	b := Key(j, "out", []File{{Path: "a.go", Bytes: []byte("y")}})
	if a == b {
		t.Fatal("changed file content must miss cache")
	}
}

func TestKeyIgnoresFileOrder(t *testing.T) {
	j := baseJob(t, jobYAML)
	a := Key(j, "out", []File{
		{Path: "a.go", Bytes: []byte("x")},
		{Path: "b.go", Bytes: []byte("y")},
	})
	b := Key(j, "out", []File{
		{Path: "b.go", Bytes: []byte("y")},
		{Path: "a.go", Bytes: []byte("x")},
	})
	if a != b {
		t.Fatal("file order must not affect the key")
	}
}

func TestKeyChangesWithInline(t *testing.T) {
	j1 := baseJob(t, jobYAML)
	j2 := baseJob(t, jobYAML+`
context:
  inline: cache key = sha256(prompt + output + spec)
`)
	if Key(j1, "out", nil) == Key(j2, "out", nil) {
		t.Fatal("inline context must be part of the hash")
	}
}

func TestKeyChangesWithCritiqueModel(t *testing.T) {
	j1 := baseJob(t, jobYAML)
	j2 := baseJob(t, jobYAML+`
model:
  critique_model: strong-model
`)
	if Key(j1, "out", nil) == Key(j2, "out", nil) {
		t.Fatal("critique_model must be part of the hash")
	}
}

func TestNormalizedSpecIsCanonicalJSON(t *testing.T) {
	j := baseJob(t, jobYAML)
	var decoded map[string]any
	if err := json.Unmarshal([]byte(normalizedSpec(j, nil)), &decoded); err != nil {
		t.Fatalf("normalized_spec is not valid JSON: %v", err)
	}
	ctx := decoded["context"].(map[string]any)
	if _, ok := ctx["files"]; !ok {
		t.Error("context.files key must always be present")
	}
	if _, ok := ctx["inline"]; !ok {
		t.Error("context.inline key must always be present (null when unset)")
	}
	if _, ok := decoded["assertions"]; !ok {
		t.Error("assertions key must always be present")
	}
	if _, ok := decoded["critique_model"]; !ok {
		t.Error("critique_model key must always be present (null when unset)")
	}
}
