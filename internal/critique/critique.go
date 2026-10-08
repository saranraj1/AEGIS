// Package critique implements stage 6: a forced-refute second pass. One
// LLM call receives the prompt, output, claims with evidence, and steps,
// and returns {verdict: agree|disagree|mixed, notes, disputed_claim_ids}.
// Critique never rewrites the output or adds claims.
package critique

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/stagejson"
)

// raw is the critique response shape.
type raw struct {
	Verdict          string   `json:"verdict"`
	Notes            string   `json:"notes"`
	DisputedClaimIDs []string `json:"disputed_claim_ids"`
}

// SystemPrompt is the fixed system message: an attempted refute is
// mandatory.
func SystemPrompt() string {
	return "You are a skeptical reviewer. You MUST attempt to refute the answer: look for claims the context contradicts, " +
		"evidence that does not hold, or reasoning that does not follow. Only if your refutation attempt fails do you agree. " +
		"Reply with JSON only: {\"verdict\": \"agree\"|\"disagree\"|\"mixed\", \"notes\": string, \"disputed_claim_ids\": [string]}. " +
		"You do not rewrite the answer and you do not invent new claims."
}

// UserPrompt builds the user message with the full audit context.
func UserPrompt(prompt, output string, claims []report.Claim, steps []report.Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Original prompt:\n%s\n\n", prompt)
	fmt.Fprintf(&b, "Answer under review:\n%s\n\n", output)
	b.WriteString("Extracted claims (with grounding evidence):\n")
	for _, c := range claims {
		fmt.Fprintf(&b, "%s [%s]: %s\n", c.ID, c.Grounding, c.Text)
		for _, e := range c.Evidence {
			fmt.Fprintf(&b, "    evidence %s:%d %q\n", e.Path, e.Line, e.Quote)
		}
	}
	b.WriteString("\nReasoning steps:\n")
	for _, s := range steps {
		fmt.Fprintf(&b, "%s: %s (depends on %v)\n", s.ID, s.Text, s.DependsOn)
	}
	b.WriteString("\nAttempt to refute the answer. " +
		"Reply with JSON only: {\"verdict\": \"agree\"|\"disagree\"|\"mixed\", \"notes\": \"...\", \"disputed_claim_ids\": [\"...\"]}.")
	return b.String()
}

// Run executes the critique stage. Unknown disputed claim ids are filtered
// with a warning; the verdict must come from the report vocabulary (a
// foreign value goes through the repair path and hard-fails).
func Run(ctx context.Context, prov provider.Provider, jobID, prompt, output string, claims []report.Claim, steps []report.Step) (report.Critique, []string, error) {
	req := provider.Request{
		Stage:  provider.StageCritique,
		System: SystemPrompt(),
		User:   UserPrompt(prompt, output, claims, steps),
		JobID:  jobID,
	}
	var decoded raw
	_, warnings, err := stagejson.Call(ctx, prov, req, func(content string) error {
		decoded = raw{}
		if err := json.Unmarshal([]byte(content), &decoded); err != nil {
			return err
		}
		switch decoded.Verdict {
		case report.VerdictAgree, report.VerdictDisagree, report.VerdictMixed:
			return nil
		}
		return fmt.Errorf("unknown verdict %q (accepted: %s, %s, %s)",
			decoded.Verdict, report.VerdictAgree, report.VerdictDisagree, report.VerdictMixed)
	})
	if err != nil {
		return report.Critique{}, warnings, err
	}

	claimIDs := make(map[string]bool, len(claims))
	for _, c := range claims {
		claimIDs[c.ID] = true
	}
	disputed := make([]string, 0, len(decoded.DisputedClaimIDs))
	for _, id := range decoded.DisputedClaimIDs {
		if claimIDs[id] {
			disputed = append(disputed, id)
		} else {
			warnings = append(warnings, fmt.Sprintf("critique: disputed claim id %q does not match any extracted claim; dropped", id))
		}
	}

	return report.Critique{
		Verdict:          decoded.Verdict,
		Notes:            decoded.Notes,
		DisputedClaimIDs: disputed,
	}, warnings, nil
}
