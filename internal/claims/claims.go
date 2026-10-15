// Package claims implements stage 3: extract atomic claims from the model
// output. One structured LLM call returns [{text, stance}]; the engine
// drops claims whose normalized text is not a substring of the normalized
// output (invented claims), merges duplicates keeping asserted over hedged,
// and assigns ids c0, c1, ... in extraction order.
package claims

import (
	"context"
	"fmt"
	"strings"

	"github.com/aegis-verify/aegis/internal/matching"
	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/stagejson"
)

// Raw is one claim candidate from the extraction response.
type Raw struct {
	Text   string `json:"text"`
	Stance string `json:"stance"`
}

// SystemPrompt is the fixed system message for the claims stage.
func SystemPrompt() string {
	return "You extract atomic factual claims from an assistant answer. " +
		"Each claim must be one short, self-contained factual statement whose wording comes from the answer. " +
		"Mark a claim \"hedged\" if the answer qualifies it with words like may, might, possibly, or could; otherwise \"asserted\". " +
		"Reply with JSON only as an OBJECT with one key: {\"claims\": [{\"text\": string, \"stance\": \"asserted\"|\"hedged\"}]} " +
		"(the response_format json_object mode requires a top-level object; claims live under the claims key)."
}

// UserPrompt builds the user message: the output plus a deterministic
// sentence/bullet split as extraction seeds.
func UserPrompt(output string, seeds []string) string {
	var b strings.Builder
	b.WriteString("Answer to analyze:\n\n")
	b.WriteString(output)
	b.WriteString("\n\nSeed segments from the answer (for reference, not necessarily atomic):\n")
	for _, s := range seeds {
		b.WriteString("- ")
		b.WriteString(s)
		b.WriteString("\n")
	}
	b.WriteString("\nExtract every atomic factual claim. " +
		"Reply with JSON only as an object: {\"claims\": [{\"text\": \"...\", \"stance\": \"asserted\"|\"hedged\"}]}.")
	return b.String()
}

// Extract runs the claims stage against prov for jobID and the frozen
// output text. Returned claims have ids assigned, empty grounding fields
// (filled by the ground stage), and empty (non-nil) evidence lists.
func Extract(ctx context.Context, prov provider.Provider, jobID, output string) ([]report.Claim, []string, error) {
	req := provider.Request{
		Stage:  provider.StageClaims,
		System: SystemPrompt(),
		User:   UserPrompt(output, SeedSentences(output)),
		JobID:  jobID,
	}
	var raws []Raw
	_, warnings, err := stagejson.Call(ctx, prov, req, func(content string) error {
		raws = nil
		return stagejson.DecodeArray(content, "claims", &raws)
	})
	if err != nil {
		return nil, warnings, err
	}

	normOutput := matching.Normalize(output)
	kept := make([]report.Claim, 0, len(raws))
	index := make(map[string]int, len(raws)) // normalized text -> position in kept
	invented, unknownStance, merged := 0, 0, 0

	for _, r := range raws {
		norm := matching.Normalize(r.Text)
		if norm == "" {
			continue // blank entries carry no claim
		}
		if !strings.Contains(normOutput, norm) {
			invented++
			continue
		}
		stance := r.Stance
		if stance != report.StanceAsserted && stance != report.StanceHedged {
			stance = report.StanceAsserted
			unknownStance++
		}
		if pos, ok := index[norm]; ok {
			merged++
			if stance == report.StanceAsserted && kept[pos].Stance != report.StanceAsserted {
				kept[pos].Stance = report.StanceAsserted
			}
			continue
		}
		index[norm] = len(kept)
		kept = append(kept, report.Claim{
			Text:     strings.TrimSpace(r.Text),
			Stance:   stance,
			Evidence: []report.Evidence{},
		})
	}
	for i := range kept {
		kept[i].ID = fmt.Sprintf("c%d", i)
	}

	if invented > 0 {
		warnings = append(warnings, fmt.Sprintf("claims: dropped %d invented claim(s) not present in output", invented))
	}
	if unknownStance > 0 {
		warnings = append(warnings, fmt.Sprintf("claims: treated %d claim(s) with unknown stance as asserted", unknownStance))
	}
	if merged > 0 {
		warnings = append(warnings, fmt.Sprintf("claims: merged %d duplicate claim(s), keeping asserted over hedged", merged))
	}
	return kept, warnings, nil
}

// SeedSentences deterministically splits output into bullets and sentences:
// bullet lines yield one seed each; other lines split on sentence
// terminators followed by whitespace or end of line.
func SeedSentences(output string) []string {
	var seeds []string
	for _, line := range strings.Split(output, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if rest, isBullet := stripBullet(t); isBullet {
			if rest != "" {
				seeds = append(seeds, rest)
			}
			continue
		}
		seeds = append(seeds, splitSentences(t)...)
	}
	return seeds
}

// stripBullet recognizes "- ", "* ", "• " and "N. "/"N) " markers.
func stripBullet(line string) (string, bool) {
	if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "• ") {
		return strings.TrimSpace(line[2:]), true
	}
	for i := 0; i < len(line) && i < 4; i++ {
		c := line[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if (c == '.' || c == ')') && i > 0 && i+1 < len(line) && line[i+1] == ' ' {
			return strings.TrimSpace(line[i+2:]), true
		}
		return line, false
	}
	return line, false
}

// splitSentences cuts s on '.', '!', '?' followed by a space or end of
// string. Segments keep their terminator.
func splitSentences(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '.' && c != '!' && c != '?' {
			continue
		}
		end := i + 1
		if end == len(s) || s[end] == ' ' {
			if seg := strings.TrimSpace(s[start:end]); seg != "" {
				out = append(out, seg)
			}
			start = end
		}
	}
	if start < len(s) {
		if seg := strings.TrimSpace(s[start:]); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}
