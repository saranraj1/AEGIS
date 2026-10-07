// Package job parses and validates Aegis job specs (YAML or JSON).
//
// Strictness per the approved design: unknown fields at any nesting level are
// hard errors naming the field and the accepted keys (never user values);
// required fields are id and prompt; unknown enum/literal values error;
// an optional schema_version pins the schema (default 1; >1 rejected with a
// clear upgrade message).
package job

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the schema version this binary understands (decision D1).
const SchemaVersion = 1

// CodeAssertions configures deterministic code parsing (stage 7).
type CodeAssertions struct {
	Parse bool `yaml:"parse" json:"parse"`
}

// Assertions is the declarations block. Pointers distinguish "unset" (default)
// from explicit false/0; zero-values apply schema defaults.
type Assertions struct {
	MustClaim         []string        `yaml:"must_claim" json:"must_claim"`
	MustNotClaim      []string        `yaml:"must_not_claim" json:"must_not_claim"`
	Grounded          *bool           `yaml:"grounded" json:"grounded"`
	MaxUngrounded     *int            `yaml:"max_ungrounded" json:"max_ungrounded"`
	CritiqueMustAgree *bool           `yaml:"critique_must_agree" json:"critique_must_agree"`
	Code              *CodeAssertions `yaml:"code" json:"code"`
}

// Model config: Wrap forces a provider call even when output is provided;
// CritiqueModel overrides the model id for the critique stage only.
type Model struct {
	Wrap          bool    `yaml:"wrap" json:"wrap"`
	CritiqueModel *string `yaml:"critique_model" json:"critique_model"`
}

// Context holds the grounding corpus: file paths (read by the engine) plus an
// inline blob the engine treats as a synthetic file named "<inline>".
type Context struct {
	Files  []string `yaml:"files" json:"files"`
	Inline *string  `yaml:"inline" json:"inline"`
}

// Job is a validated job spec.
type Job struct {
	SchemaVersion int        `yaml:"schema_version" json:"schema_version"`
	ID            string     `yaml:"id" json:"id"`
	Prompt        string     `yaml:"prompt" json:"prompt"`
	Output        *string    `yaml:"output" json:"output"`
	Context       Context    `yaml:"context" json:"context"`
	Assertions    Assertions `yaml:"assertions" json:"assertions"`
	Model         Model      `yaml:"model" json:"model"`
}

// acceptedKeys returns the allowed keys at the given path plus a sorted,
// comma-separated rendering for deterministic error messages.
func acceptedKeys(path string) (map[string]bool, string) {
	var keys []string
	switch path {
	case "$":
		keys = []string{"assertions", "context", "id", "model", "output", "prompt", "schema_version"}
	case "context":
		keys = []string{"files", "inline"}
	case "assertions":
		keys = []string{"code", "critique_must_agree", "grounded", "max_ungrounded", "must_claim", "must_not_claim"}
	case "model":
		keys = []string{"critique_model", "wrap"}
	case "assertions.code":
		keys = []string{"parse"}
	default:
		return nil, ""
	}
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m, strings.Join(keys, ", ")
}

// childPath maps a key at the current path to a nested object path, if any.
func childPath(path, key string) string {
	switch {
	case path == "$" && (key == "context" || key == "assertions" || key == "model"):
		return key
	case path == "assertions" && key == "code":
		return "assertions.code"
	}
	return ""
}

func unknownFieldErr(key, path string) error {
	_, list := acceptedKeys(path)
	return fmt.Errorf("unknown field %q at %s (accepted: %s)", key, path, list)
}

// checkYAMLUnknown walks a decoded YAML node tree (a mapping at each object
// position) and rejects keys outside acceptedKeys. devNote: yaml.Unmarshal
// into yaml.Node wraps the document in a DocumentNode; unwrap it first.
func checkYAMLUnknown(root *yaml.Node, path string) error {
	node := root
	if node.Kind == yaml.DocumentNode {
		if len(node.Content) == 0 {
			return nil // empty document; Validate gates id/prompt
		}
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		if path == "$" && len(node.Content) > 0 {
			return fmt.Errorf("job spec must be a mapping, got %v", node.Kind)
		}
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		allowed, _ := acceptedKeys(path)
		key := keyNode.Value
		if keyNode.Tag != "!!str" && keyNode.Tag != "" {
			return fmt.Errorf("keys must be strings at %s", path)
		}
		if !allowed[key] {
			return unknownFieldErr(key, path)
		}
		if sub := childPath(path, key); sub != "" {
			if err := checkYAMLUnknown(valNode, sub); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkJSONUnknown unmarshals levels as raw maps and rejects unknown keys.
// encoding/json itself ignores unknown fields, so the loader probes the shape
// before decoding into Job.
func checkJSONUnknown(raw []byte, path string) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		// Not a JSON object at this level. Decode-time will produce the real
		// structural error message for the whole document; nothing to add.
		return nil
	}
	allowed, _ := acceptedKeys(path)
	for k, v := range probe {
		if !allowed[k] {
			return unknownFieldErr(k, path)
		}
		if sub := childPath(path, k); sub != "" {
			if err := checkJSONUnknown(v, sub); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodeYAML decodes a parsed YAML source into Job after field checks.
func decodeYAML(src []byte, out *Job) error {
	var node yaml.Node
	if err := yaml.Unmarshal(src, &node); err != nil {
		return fmt.Errorf("parse yaml: %w", err)
	}
	if err := checkYAMLUnknown(&node, "$"); err != nil {
		return err
	}
	if err := node.Decode(out); err != nil {
		return fmt.Errorf("decode yaml: %w", err)
	}
	return nil
}

// decodeJSON decodes a JSON job after field checks.
func decodeJSON(src []byte, out *Job) error {
	if err := checkJSONUnknown(src, "$"); err != nil {
		return err
	}
	if err := json.Unmarshal(src, out); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

// ApplyDefaults fills schema defaults after decode: schema_version=1 when
// omitted; grounded/critique_must_agree/code.parse default false and
// max_ungrounded unlimited via their nil-safe zero values.
func (j *Job) ApplyDefaults() {
	if j.SchemaVersion == 0 {
		j.SchemaVersion = SchemaVersion
	}
}

// Validate enforces required fields and value constraints. Error wording ties
// back to the field path so users can locate the fix.
func (j *Job) Validate() error {
	if j.SchemaVersion > SchemaVersion {
		return fmt.Errorf("schema_version %d requires a newer aegis binary; this binary supports version %d",
			j.SchemaVersion, SchemaVersion)
	}
	if j.ID == "" {
		return fmt.Errorf("field %q is required in YAML/JSON (pytest may omit it)", "id")
	}
	if j.Prompt == "" {
		return fmt.Errorf("field %q is required", "prompt")
	}
	if j.Context.Inline != nil && strings.TrimSpace(*j.Context.Inline) == "" {
		return fmt.Errorf("field %q must not be empty when present", "context.inline")
	}
	for _, f := range j.Context.Files {
		if strings.TrimSpace(f) == "" {
			return fmt.Errorf("field context.files contains an empty path")
		}
	}
	for i, m := range j.Assertions.MustClaim {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("field assertions.must_claim[%d] is empty", i)
		}
	}
	for i, m := range j.Assertions.MustNotClaim {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("field assertions.must_not_claim[%d] is empty", i)
		}
	}
	if j.Assertions.MaxUngrounded != nil && *j.Assertions.MaxUngrounded < 0 {
		return fmt.Errorf("field assertions.max_ungrounded must be >= 0, got %d", *j.Assertions.MaxUngrounded)
	}
	return nil
}

// Load parses src (YAML or JSON detected by leading '{'), applies defaults,
// and validates in one step.
func Load(src []byte) (*Job, error) {
	var j Job
	var err error
	if strings.HasPrefix(strings.TrimLeft(string(src), " \t\r\n"), "{") {
		err = decodeJSON(src, &j)
	} else {
		err = decodeYAML(src, &j)
	}
	if err != nil {
		return nil, err
	}
	if j.SchemaVersion > SchemaVersion {
		// Same message as Validate to give decode-time feedback on
		// schema_version before anything else runs.
		return nil, fmt.Errorf("schema_version %d requires a newer aegis binary; this binary supports version %d",
			j.SchemaVersion, SchemaVersion)
	}
	j.ApplyDefaults()
	if err := j.Validate(); err != nil {
		return nil, err
	}
	return &j, nil
}

// LoadFile reads path from disk and parses it.
func LoadFile(path string) (*Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read job file: %w", err)
	}
	return Load(data)
}

// Ensure sort is referenced even as messages only use the pre-sorted lists.
var _ = sort.Strings
