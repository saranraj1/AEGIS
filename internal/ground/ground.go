// Package ground implements stage 4: ground each claim against the
// provided context only (no web). Claims are processed in batches of 25;
// each batch is one LLM call returning [{claim_id, grounding, quote}]
// where claim_id may be omitted to bind to the next unassigned claim in
// order (A11).
//
// Quote verification always searches the FULL context files: a quote that
// is not an exact substring of some file discards the evidence and marks
// the claim ungrounded with reason invalid-evidence. When the context
// exceeds the token budget, sliding windows (40 lines, stride 20) are
// ranked by token overlap with the batch's claims and only the top
// windows are sent — but the quote check still searches full files.
//
// If a batched call hits the stage timeout, all claims in that batch
// become ungrounded with reason timeout and the run continues
// (assertions decide pass/fail, not the engine).
package ground

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aegis-verify/aegis/internal/matching"
	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/stagejson"
)

// Defaults per the spec and decisions D5.
const (
	DefaultBatchSize   = 25
	DefaultTokenBudget = 8000
	DefaultWindowSize  = 40
	DefaultStride      = 20
)

// ContextFile is one grounding source. The engine reads files and the
// inline blob (synthetic path "<inline>") before calling this stage.
type ContextFile struct {
	Path string
	Text string
}

// Options tunes the batching and windowing; zero fields take spec defaults.
type Options struct {
	BatchSize   int
	TokenBudget int
	WindowSize  int
	Stride      int
	// CallTimeout bounds each batched LLM call individually (the spec's
	// --stage-timeout applies per batched call, so one slow batch does not
	// starve the rest). Zero means no per-call deadline; the engine sets it.
	CallTimeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.BatchSize <= 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.TokenBudget <= 0 {
		o.TokenBudget = DefaultTokenBudget
	}
	if o.WindowSize <= 0 {
		o.WindowSize = DefaultWindowSize
	}
	if o.Stride <= 0 {
		o.Stride = DefaultStride
	}
	return o
}

// rawGround is one record of a grounding response. The quote is optional
// only for not-in-context; a missing quote for supported/contradicted
// flows into the invalid-evidence path (never a hard error).
type rawGround struct {
	ClaimID   string `json:"claim_id"`
	Grounding string `json:"grounding"`
	Quote     string `json:"quote"`
}

// SystemPrompt is the fixed system message for the grounding stage.
func SystemPrompt() string {
	return "You verify claims strictly against the provided context excerpts. Never use outside knowledge. " +
		"For each claim decide: supported (the context states it), contradicted (the context states the opposite), " +
		"or not-in-context (the context does not address it). " + "Reply with JSON only as an OBJECT with one key: {\"results\": [{\"claim_id\": string, \"grounding\": \"supported\"|\"contradicted\"|\"not-in-context\", \"quote\": string}]}. " +
		"(the response_format json_object mode requires a top-level object; records live under the results key). " +
		"quote must be an exact substring of the context and is required for supported and contradicted; " +
		"omit it for not-in-context. Include every claim id exactly once."
}

// UserPrompt builds the user message: rendered context windows plus the
// batch's claims.
func UserPrompt(contextText string, batch []report.Claim) string {
	var b strings.Builder
	b.WriteString("Context excerpts:\n")
	if strings.TrimSpace(contextText) == "" {
		b.WriteString("(no context provided)\n")
	} else {
		b.WriteString(contextText)
	}
	b.WriteString("\nClaims to ground:\n")
	for _, c := range batch {
		fmt.Fprintf(&b, "%s: %s\n", c.ID, c.Text)
	}
	b.WriteString("\nGround every claim against the context only. " +
		"Reply with JSON only as an object: " +
		"{\"results\": [{\"claim_id\": \"...\", \"grounding\": \"supported\"|\"contradicted\"|\"not-in-context\", \"quote\": \"...\"}]}.")
	return b.String()
}

// Run grounds every claim in place (mutating Grounding, GroundingReason,
// Evidence) and returns the same slice. Every claim starts as
// not-in-context and is overwritten by response records.
func Run(ctx context.Context, prov provider.Provider, jobID string, claims []report.Claim, files []ContextFile, opts Options) ([]report.Claim, []string, error) {
	opts = opts.withDefaults()
	var warnings []string
	if len(claims) == 0 {
		return claims, warnings, nil
	}
	for i := range claims {
		claims[i].Grounding = report.NotInContext
		claims[i].GroundingReason = ""
		claims[i].Evidence = []report.Evidence{}
	}

	for start := 0; start < len(claims); start += opts.BatchSize {
		end := start + opts.BatchSize
		if end > len(claims) {
			end = len(claims)
		}
		batch := claims[start:end]
		batchWarns, err := runBatch(ctx, prov, jobID, batch, files, opts)
		warnings = append(warnings, batchWarns...)
		if err != nil {
			var perr *provider.Error
			if errors.As(err, &perr) && perr.Kind == provider.KindTimeout {
				// Spec: timeout marks only this batch ungrounded.
				for i := range batch {
					batch[i].Grounding = report.Ungrounded
					batch[i].GroundingReason = report.ReasonTimeout
					batch[i].Evidence = []report.Evidence{}
				}
				warnings = append(warnings, fmt.Sprintf("ground: batch timed out; %d claim(s) marked ungrounded (timeout)", len(batch)))
				continue
			}
			return nil, warnings, err
		}
	}
	return claims, warnings, nil
}

// runBatch performs one grounding call and binds its records to the batch.
func runBatch(ctx context.Context, prov provider.Provider, jobID string, batch []report.Claim, files []ContextFile, opts Options) ([]string, error) {
	var warnings []string
	callCtx := ctx
	if opts.CallTimeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, opts.CallTimeout)
		defer cancel()
	}
	req := provider.Request{
		Stage:  provider.StageGround,
		System: SystemPrompt(),
		User:   UserPrompt(renderContext(files, batch, opts), batch),
		JobID:  jobID,
	}
	var records []rawGround
	_, callWarns, err := stagejson.Call(callCtx, prov, req, func(content string) error {
		records = nil
		if err := stagejson.DecodeArray(content, "results", &records); err != nil {
			return err
		}
		for i, r := range records {
			switch r.Grounding {
			case report.Grounded, report.Contradicted, report.NotInContext:
			default:
				return fmt.Errorf("record %d: unknown grounding %q (accepted: %s, %s, %s)",
					i, r.Grounding, report.Grounded, report.Contradicted, report.NotInContext)
			}
		}
		return nil
	})
	warnings = append(warnings, callWarns...)
	if err != nil {
		return warnings, err
	}

	assigned := make([]bool, len(batch))
	next := 0 // next claim in order with no assignment yet (A11)
	for _, r := range records {
		idx := -1
		if r.ClaimID != "" {
			for i := range batch {
				if batch[i].ID == r.ClaimID {
					idx = i
					break
				}
			}
			if idx < 0 {
				warnings = append(warnings, fmt.Sprintf("ground: record references unknown claim %q; ignored", r.ClaimID))
				continue
			}
		} else {
			for next < len(batch) && assigned[next] {
				next++
			}
			if next >= len(batch) {
				warnings = append(warnings, "ground: extra record without claim_id; ignored")
				continue
			}
			idx = next
			next++
		}

		c := &batch[idx]
		switch r.Grounding {
		case report.Grounded, report.Contradicted:
			if ev, ok := verifyQuote(r.Quote, files); ok {
				c.Grounding = r.Grounding
				c.GroundingReason = ""
				c.Evidence = []report.Evidence{ev}
			} else {
				c.Grounding = report.Ungrounded
				c.GroundingReason = report.ReasonInvalid
				c.Evidence = []report.Evidence{}
				warnings = append(warnings, fmt.Sprintf("ground: claim %s: evidence quote not found in context; marked ungrounded (invalid-evidence)", c.ID))
			}
		case report.NotInContext:
			c.Grounding = report.NotInContext
			c.GroundingReason = ""
			c.Evidence = []report.Evidence{}
		}
		assigned[idx] = true
	}
	return warnings, nil
}

// verifyQuote requires an exact substring occurrence in some context file
// (searched in file order, full text) and reports the 1-based line of the
// first occurrence.
func verifyQuote(quote string, files []ContextFile) (report.Evidence, bool) {
	q := strings.TrimSpace(quote)
	if q == "" {
		return report.Evidence{}, false
	}
	for _, f := range files {
		if idx := strings.Index(f.Text, q); idx >= 0 {
			return report.Evidence{
				Path:  f.Path,
				Quote: q,
				Line:  1 + strings.Count(f.Text[:idx], "\n"),
			}, true
		}
	}
	return report.Evidence{}, false
}

// lineWindow is one sliding window over a file.
type lineWindow struct {
	fileIdx int
	start   int // 1-based first line
	lines   []string
	tokens  int
	score   int
}

func buildWindows(files []ContextFile, size, stride int) []lineWindow {
	var out []lineWindow
	for fi, f := range files {
		lines := strings.Split(f.Text, "\n")
		for start := 0; start < len(lines); start += stride {
			end := start + size
			if end > len(lines) {
				end = len(lines)
			}
			w := lineWindow{fileIdx: fi, start: start + 1, lines: lines[start:end]}
			w.tokens = len(strings.Fields(strings.Join(w.lines, " ")))
			out = append(out, w)
			if end >= len(lines) {
				break
			}
		}
	}
	return out
}

// renderContext ranks windows by token overlap with the batch's claims,
// selects within the token budget, and renders the selected windows in
// document order. When the whole corpus fits the budget, everything is
// sent unchanged.
func renderContext(files []ContextFile, batch []report.Claim, opts Options) string {
	windows := buildWindows(files, opts.WindowSize, opts.Stride)
	if len(windows) == 0 {
		return ""
	}

	query := map[string]int{}
	for _, c := range batch {
		for _, tok := range strings.Fields(matching.Normalize(c.Text)) {
			query[tok]++
		}
	}
	for i := range windows {
		counts := map[string]int{}
		for _, ln := range windows[i].lines {
			for _, tok := range strings.Fields(matching.Normalize(ln)) {
				counts[tok]++
			}
		}
		score := 0
		for t, n := range query {
			if counts[t] > 0 {
				score += n
			}
		}
		windows[i].score = score
	}

	total := 0
	for i := range windows {
		total += windows[i].tokens
	}
	var selected []lineWindow
	if total <= opts.TokenBudget {
		selected = windows
	} else {
		order := make([]int, len(windows))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			wa, wb := windows[order[a]], windows[order[b]]
			if wa.score != wb.score {
				return wa.score > wb.score
			}
			if wa.fileIdx != wb.fileIdx {
				return wa.fileIdx < wb.fileIdx
			}
			return wa.start < wb.start
		})
		used := 0
		for _, i := range order {
			w := windows[i]
			if used+w.tokens > opts.TokenBudget {
				continue
			}
			selected = append(selected, w)
			used += w.tokens
		}
	}

	sort.SliceStable(selected, func(a, b int) bool {
		if selected[a].fileIdx != selected[b].fileIdx {
			return selected[a].fileIdx < selected[b].fileIdx
		}
		return selected[a].start < selected[b].start
	})

	var b strings.Builder
	for i, w := range selected {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "=== %s lines %d-%d ===\n", files[w.fileIdx].Path, w.start, w.start+len(w.lines)-1)
		b.WriteString(strings.Join(w.lines, "\n"))
	}
	return b.String()
}
