// Package assert evaluates job assertions against extracted claims, critique
// verdicts, and code-check results. It is a pure function from report-shaped
// data to the ordered failed_assertions list.
package assert

import (
	"fmt"
	"strconv"

	"github.com/aegis-verify/aegis/internal/matching"
	"github.com/aegis-verify/aegis/internal/report"
)

// Claims abstracts the claims section of a report for evaluation.
type Claim struct {
	ID        string
	Text      string
	Stance    string // hedged claims still count as matches
	Grounding string // supported | contradicted | not-in-context | ungrounded
}

// Critique is the forced-refute verdict.
type Critique struct {
	Verdict string // agree | disagree | mixed
	Notes   string
}

// CodeCheck mirrors report.CodeCheck for parse-kind results.
type CodeCheck struct {
	Kind     string // only "parse" in v1; guards against future-kind drift
	Language string
	OK       bool
	Detail   string
}

// Result is one failed assertion, shaped exactly like report.FailedAssertion.
type Result = report.FailedAssertion

// Input bundles the job's assertions with the stage outputs to evaluate.
type Input struct {
	MustClaim         []string
	MustNotClaim      []string
	Grounded          bool
	MaxUngrounded     *int
	CritiqueMustAgree bool
	CodeParse         bool

	Claims     []Claim
	Critique   *Critique
	CodeChecks []CodeCheck
}

// matchesClaim returns indexes of claims that fuzzy-match the requirement.
// Hedge stance does not disqualify a match.
func matchesClaim(claims []Claim, requirement string) []int {
	var out []int
	for i := range claims {
		if matching.Matches(requirement, claims[i].Text) {
			out = append(out, i)
		}
	}
	return out
}

// UngroundedCount counts claims whose grounding is not supported (spec: counts
// ungrounded, not-in-context, and contradicted).
func UngroundedCount(claims []Claim) int {
	n := 0
	for _, c := range claims {
		if c.Grounding != report.Grounded {
			n++
		}
	}
	return n
}

// Evaluate produces the ordered failed_assertions list: empty list means all
// assertions pass.
func Evaluate(in Input) []Result {
	var out []Result

	// must_claim, in spec order.
	matches := make([][]int, len(in.MustClaim))
	for i, req := range in.MustClaim {
		m := matchesClaim(in.Claims, req)
		if len(m) == 0 {
			out = append(out, Result{
				Type:     report.ATMustClaim,
				Expected: req,
				Actual:   "no matching claim",
				Detail:   fmt.Sprintf("no claim matched must_claim %q", req),
			})
			continue
		}
		matches[i] = m
	}

	// grounded: if set, every must_claim match must be supported.
	if in.Grounded {
		for i, req := range in.MustClaim {
			for _, idx := range matches[i] {
				c := in.Claims[idx]
				if c.Grounding == report.Grounded {
					continue
				}
				out = append(out, Result{
					Type:     report.ATGrounded,
					Expected: report.Grounded,
					Actual:   c.Grounding,
					Detail: fmt.Sprintf("claim %s (%s) has grounding %q, not supported (requirement %q)",
						c.ID, c.Text, c.Grounding, req),
				})
			}
		}
	}

	// must_not_claim: any match is a failure.
	for _, req := range in.MustNotClaim {
		for _, idx := range matchesClaim(in.Claims, req) {
			c := in.Claims[idx]
			out = append(out, Result{
				Type:     report.ATMustNotClaim,
				Expected: req,
				Actual:   c.Text,
				Detail:   fmt.Sprintf("claim %s (%s) matches must_not_claim %q", c.ID, c.Text, req),
			})
		}
	}

	// max_ungrounded: bounded count of non-supported claims.
	if in.MaxUngrounded != nil {
		n := UngroundedCount(in.Claims)
		if n > *in.MaxUngrounded {
			out = append(out, Result{
				Type:     report.ATMaxUngrounded,
				Expected: "<=" + strconv.Itoa(*in.MaxUngrounded),
				Actual:   strconv.Itoa(n),
				Detail:   fmt.Sprintf("%d claims ungrounded (contradicted/not-in-context/ungrounded); max %d", n, *in.MaxUngrounded),
			})
		}
	}

	// critique_must_agree.
	if in.CritiqueMustAgree {
		v := ""
		if in.Critique != nil {
			v = in.Critique.Verdict
		}
		if v != report.VerdictAgree {
			out = append(out, Result{
				Type:     report.ATCritiqueMustAgree,
				Expected: report.VerdictAgree,
				Actual:   v,
				Detail:   fmt.Sprintf("critique verdict is %q, not agree", v),
			})
		}
	}

	// code.parse: parse-kind checks that failed.
	if in.CodeParse {
		for _, cc := range in.CodeChecks {
			if cc.Kind != "parse" || cc.OK {
				continue
			}
			out = append(out, Result{
				Type:     report.ATCodeParse,
				Expected: "parses",
				Actual:   cc.Language + ": " + cc.Detail,
				Detail:   fmt.Sprintf("code check failed for %s: %s", cc.Language, cc.Detail),
			})
		}
	}

	return out
}
