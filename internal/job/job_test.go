package job

import "testing"

const specExample = `
id: explain-cache-key
prompt: |
  How does aegis hash a cache key?
output: |
  The cache key is sha256 of prompt, output, and spec.
context:
  files:
    - internal/cache/key.go
  inline: |
    cache key = sha256(prompt + output + spec)
assertions:
  must_claim:
    - cache key is sha256 of full pipeline
  must_not_claim:
    - cache is stored in git
  grounded: true
  max_ungrounded: 0
  critique_must_agree: false
  code:
    parse: true
model:
  wrap: false
  critique_model: null
`

func TestLoadYAMLSpecExample(t *testing.T) {
	j, err := Load([]byte(specExample))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if j.ID != "explain-cache-key" {
		t.Errorf("ID = %q", j.ID)
	}
	if j.Prompt == "" || j.Output == nil {
		t.Fatalf("prompt/output missing")
	}
	if len(j.Context.Files) != 1 || j.Context.Files[0] != "internal/cache/key.go" {
		t.Errorf("Files = %v", j.Context.Files)
	}
	if j.Context.Inline == nil || !contains("cache key = sha256(prompt + output + spec)", *j.Context.Inline) {
		t.Errorf("Inline = %v", j.Context.Inline)
	}
	if j.Assertions.Grounded == nil || !*j.Assertions.Grounded {
		t.Errorf("Grounded = %v", j.Assertions.Grounded)
	}
	if j.Assertions.MaxUngrounded == nil || *j.Assertions.MaxUngrounded != 0 {
		t.Errorf("MaxUngrounded = %v", j.Assertions.MaxUngrounded)
	}
	if j.Assertions.CritiqueMustAgree == nil || *j.Assertions.CritiqueMustAgree {
		t.Errorf("CritiqueMustAgree = %v", j.Assertions.CritiqueMustAgree)
	}
	if j.Assertions.Code == nil || !j.Assertions.Code.Parse {
		t.Errorf("Code.Parse = %v", j.Assertions.Code)
	}
	if j.Model.Wrap {
		t.Errorf("Wrap = true, want false")
	}
	if j.Model.CritiqueModel != nil {
		t.Errorf("CritiqueModel = %v, want nil", j.Model.CritiqueModel)
	}
	if j.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", j.SchemaVersion)
	}
}

func contains(needle, hay string) bool {
	return len(needle) <= len(hay) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestLoadMinimalDefaults(t *testing.T) {
	j, err := Load([]byte("id: j1\nprompt: p\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if j.Output != nil {
		t.Error("Output should be nil")
	}
	if j.Assertions.Grounded != nil || j.Assertions.MaxUngrounded != nil ||
		j.Assertions.CritiqueMustAgree != nil || j.Assertions.Code != nil {
		t.Error("assertion pointers should be nil for defaults")
	}
	if len(j.Assertions.MustClaim) != 0 || len(j.Assertions.MustNotClaim) != 0 {
		t.Error("claim lists should be empty")
	}
	if j.Context.Inline != nil || len(j.Context.Files) != 0 {
		t.Error("context should be empty")
	}
	if j.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want default 1", j.SchemaVersion)
	}
}

func TestLoadJSONHappyPath(t *testing.T) {
	src := `{"id":"j","prompt":"p","output":"o","schema_version":1}`
	j, err := Load([]byte(src))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if j.ID != "j" || j.Prompt != "p" || j.Output == nil || *j.Output != "o" {
		t.Fatalf("got %v", j)
	}
}

func TestLoadUnknownFields(t *testing.T) {
	tests := []struct {
		name, src string
		wantErr   string
	}{
		{
			name:    "yaml top-level",
			src:     "id: j\nprompt: p\npromt: typo\n",
			wantErr: `unknown field "promt" at $ (accepted: assertions, context, id, model, output, prompt, schema_version)`,
		},
		{
			name:    "json top-level",
			src:     `{"id":"j","prompt":"p","promt":1}`,
			wantErr: `unknown field "promt" at $ (accepted: assertions, context, id, model, output, prompt, schema_version)`,
		},
		{
			name: "yaml nested assertions",
			src: `
id: j
prompt: p
assertions:
  must_claims:
    - x
`,
			wantErr: `unknown field "must_claims" at assertions (accepted: code, critique_must_agree, grounded, max_ungrounded, must_claim, must_not_claim)`,
		},
		{
			name: "yaml nested model",
			src: `
id: j
prompt: p
model:
  wrapped: false
`,
			wantErr: `unknown field "wrapped" at model (accepted: critique_model, wrap)`,
		},
		{
			name: "yaml context",
			src: `
id: j
prompt: p
context:
  file: a.go
`,
			wantErr: `unknown field "file" at context (accepted: files, inline)`,
		},
		{
			name: "yaml assertions.code",
			src: `
id: j
prompt: p
assertions:
  code:
    parses: true
`,
			wantErr: `unknown field "parses" at assertions.code (accepted: parse)`,
		},
		{
			name:    "json nested assertions",
			src:     `{"id":"j","prompt":"p","assertions":{"grounded":true,"groundds":1}}`,
			wantErr: `unknown field "groundds" at assertions (accepted: code, critique_must_agree, grounded, max_ungrounded, must_claim, must_not_claim)`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load([]byte(tt.src))
			if err == nil {
				t.Fatalf("expected error %q, got nil", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestSchemaVersions(t *testing.T) {
	t.Run("explicit v1 ok", func(t *testing.T) {
		if _, err := Load([]byte("id: j\nprompt: p\nschema_version: 1\n")); err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	})
	t.Run("future version rejected", func(t *testing.T) {
		_, err := Load([]byte("id: j\nprompt: p\nschema_version: 2\n"))
		want := "schema_version 2 requires a newer aegis binary; this binary supports version 1"
		if err == nil {
			t.Fatalf("expected schema error")
		}
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	})
}

func TestValidateRequiredFields(t *testing.T) {
	tests := []struct {
		name, src, wantErr string
	}{
		{"missing id", "prompt: p\n", `field "id" is required in YAML/JSON (pytest may omit it)`},
		{"missing prompt", "id: j\n", `field "prompt" is required`},
		{"empty inline", "id: j\nprompt: p\ncontext:\n  inline: \"   \"\n", `field "context.inline" must not be empty when present`},
		{"empty file path", "id: j\nprompt: p\ncontext:\n  files:\n    - \"  \"\n", "field context.files contains an empty path"},
		{"empty must_claim entry", "id: j\nprompt: p\nassertions:\n  must_claim:\n    - \"\"\n", "field assertions.must_claim[0] is empty"},
		{"empty must_not entry", "id: j\nprompt: p\nassertions:\n  must_not_claim:\n    - \"\"\n", "field assertions.must_not_claim[0] is empty"},
		{"negative max_ungrounded", "id: j\nprompt: p\nassertions:\n  max_ungrounded: -1\n", "field assertions.max_ungrounded must be >= 0, got -1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load([]byte(tt.src))
			if err == nil {
				t.Fatalf("expected error %q", tt.wantErr)
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadNullOutput(t *testing.T) {
	j, err := Load([]byte("id: j\nprompt: p\noutput: null\n"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if j.Output != nil {
		t.Errorf("null output should decode to nil, got %v", *j.Output)
	}
}
