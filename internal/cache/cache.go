// Package cache computes cache keys and (in a later phase) the file store.
//
// Key material per spec: SHA-256 hex of the UTF-8 concatenation
//
//	v1\0
//	prompt\0
//	output\0
//	normalized_spec\0
//
// normalized_spec is canonical JSON (sorted keys) of: context.inline, the
// sorted context.files contents (path + raw file bytes, base64-encoded
// inside the JSON so binary-safe), the assertions object, and
// model.critique_model. `id` and `model.wrap` are deliberately excluded:
// wrap only affects whether output exists before hashing.
package cache

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/aegis-verify/aegis/internal/job"
)

// normalizeFilePath cleans paths and converts Windows backslashes to
// forward slashes so cache keys are identical across platforms.
func normalizeFilePath(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}

// File is one context file's contribution to the key: its path and raw bytes.
type File struct {
	Path  string
	Bytes []byte
}

// Key computes the cache key hex digest for a resolved job.
func Key(j *job.Job, output string, files []File) string {
	norm := normalizedSpec(j, files)
	material := "v1\x00" + j.Prompt + "\x00" + output + "\x00" + norm + "\x00"
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])
}

// normalizedSpec renders the canonical JSON for the non-prompt/output parts.
// Maps are used (encoding/json sorts map keys) so the encoding is canonical.
func normalizedSpec(j *job.Job, files []File) string {
	sorted := make([]File, len(files))
	for i, f := range files {
		sorted[i] = File{
			Path:  normalizeFilePath(f.Path),
			Bytes: f.Bytes,
		}
	}
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Path < sorted[b].Path })

	fileEntries := make([]map[string]any, 0, len(sorted))
	for _, f := range sorted {
		fileEntries = append(fileEntries, map[string]any{
			"path":  f.Path,
			"bytes": base64.StdEncoding.EncodeToString(f.Bytes),
		})
	}

	assertions := map[string]any{
		"must_claim":          nilIfEmpty(j.Assertions.MustClaim),
		"must_not_claim":      nilIfEmpty(j.Assertions.MustNotClaim),
		"grounded":            boolPtr(j.Assertions.Grounded),
		"max_ungrounded":      intPtr(j.Assertions.MaxUngrounded),
		"critique_must_agree": boolPtr(j.Assertions.CritiqueMustAgree),
		"code":                codeAssertions(j.Assertions.Code),
	}

	var inline any
	if j.Context.Inline != nil {
		inline = *j.Context.Inline
	}
	var critiqueModel any
	if j.Model.CritiqueModel != nil {
		critiqueModel = *j.Model.CritiqueModel
	}

	spec := map[string]any{
		"context": map[string]any{
			"inline": inline,
			"files":  fileEntries,
		},
		"assertions":     assertions,
		"critique_model": critiqueModel,
	}
	b, err := json.Marshal(spec)
	if err != nil {
		// Maps of strings/bools/ints cannot fail to marshal; this is a
		// programming error if it ever fires.
		panic(fmt.Sprintf("cache: canonical spec marshal failed: %v", err))
	}
	return string(b)
}

func nilIfEmpty(s []string) any {
	if len(s) == 0 {
		return nil
	}
	return s
}

func boolPtr(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}

func intPtr(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func codeAssertions(c *job.CodeAssertions) any {
	if c == nil {
		return nil
	}
	return map[string]any{"parse": c.Parse}
}
