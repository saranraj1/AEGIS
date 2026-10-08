// Package reason implements stage 5: rebuild inspectable reasoning steps
// that cite claims. One LLM call returns [{id, text, depends_on}]. The id
// must match ^[A-Za-z][A-Za-z0-9_-]{0,31}$; depends_on entries must be
// claim indexes (c0, c1, ...) or earlier kept step ids. Unknown ids drop
// the step with a warning and the run continues; an empty step list is
// valid.
package reason

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/stagejson"
)

// rawStep is one entry of the reasoning response.
type rawStep struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	DependsOn []string `json:"depends_on"`
}

var idRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// SystemPrompt is the fixed system message for the reasoning stage.
func SystemPrompt() string {
	return "You rebuild the reasoning behind an answer as a short list of inspectable steps. " +
		"Each step cites the claims it relies on. " +
		"Reply with JSON only: an array of objects {\"id\": string, \"text\": string, \"depends_on\": [string]}. " +
		"ids are short slugs starting with a letter; depends_on entries are claim ids (c0, c1, ...) or ids of earlier steps."
}

// UserPrompt builds the user message listing the extracted claims.
func UserPrompt(claims []report.Claim) string {
	var b strings.Builder
	b.WriteString("Extracted claims:\n")
	for _, c := range claims {
		fmt.Fprintf(&b, "%s: %s\n", c.ID, c.Text)
	}
	b.WriteString("\nRebuild the reasoning as steps. " +
		"Reply with JSON only: [{\"id\": \"...\", \"text\": \"...\", \"depends_on\": [\"...\"]}]. " +
		"An empty list is acceptable when no steps can be identified.")
	return b.String()
}

// Run executes the reasoning stage. Steps referencing unknown ids are
// dropped with warnings; everything else is returned in response order.
func Run(ctx context.Context, prov provider.Provider, jobID string, claims []report.Claim) ([]report.Step, []string, error) {
	req := provider.Request{
		Stage:  provider.StageReason,
		System: SystemPrompt(),
		User:   UserPrompt(claims),
		JobID:  jobID,
	}
	var raws []rawStep
	_, warnings, err := stagejson.Call(ctx, prov, req, func(content string) error {
		raws = nil
		return json.Unmarshal([]byte(content), &raws)
	})
	if err != nil {
		return nil, warnings, err
	}

	claimIDs := make(map[string]bool, len(claims))
	for _, c := range claims {
		claimIDs[c.ID] = true
	}

	kept := make([]report.Step, 0, len(raws))
	keptIDs := make(map[string]bool, len(raws))
	for _, r := range raws {
		if !idRe.MatchString(r.ID) {
			warnings = append(warnings, fmt.Sprintf("reason: step %q dropped: id does not match the required pattern", r.ID))
			continue
		}
		if strings.TrimSpace(r.Text) == "" {
			warnings = append(warnings, fmt.Sprintf("reason: step %q dropped: empty text", r.ID))
			continue
		}
		if keptIDs[r.ID] {
			warnings = append(warnings, fmt.Sprintf("reason: step %q dropped: duplicate id", r.ID))
			continue
		}
		unknown := ""
		for _, d := range r.DependsOn {
			if !claimIDs[d] && !keptIDs[d] {
				unknown = d
				break
			}
		}
		if unknown != "" {
			warnings = append(warnings, fmt.Sprintf("reason: step %q dropped: unknown depends_on %q", r.ID, unknown))
			continue
		}
		deps := r.DependsOn
		if deps == nil {
			deps = []string{}
		}
		keptIDs[r.ID] = true
		kept = append(kept, report.Step{ID: r.ID, Text: r.Text, DependsOn: deps})
	}
	return kept, warnings, nil
}
