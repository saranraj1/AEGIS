// Package report defines the audit report schema (the stable contract for
// future clients) and its JSON and JUnit encoders.
package report

// Grounding status per claim.
const (
	Grounded      = "supported"
	Contradicted  = "contradicted"
	NotInContext  = "not-in-context"
	Ungrounded    = "ungrounded"
	ReasonInvalid = "invalid-evidence"
	ReasonTimeout = "timeout"
)

// Stance of an extracted claim.
const (
	StanceAsserted = "asserted"
	StanceHedged   = "hedged"
)

// Assertion types emitted in failed_assertions / JUnit failures.
const (
	ATMustClaim         = "must_claim"
	ATMustNotClaim      = "must_not_claim"
	ATGrounded          = "grounded"
	ATMaxUngrounded     = "max_ungrounded"
	ATCritiqueMustAgree = "critique_must_agree"
	ATCodeParse         = "code.parse"
)

// Verdict from the critique stage.
const (
	VerdictAgree    = "agree"
	VerdictDisagree = "disagree"
	VerdictMixed    = "mixed"
)

// Evidence grounds a claim to a quote in a context file.
type Evidence struct {
	Path  string `json:"path" yaml:"path"`
	Quote string `json:"quote" yaml:"quote"`
	Line  int    `json:"line" yaml:"line"`
}

// Claim is one atomic assertion extracted from output, with grounding.
type Claim struct {
	ID              string     `json:"id"`
	Text            string     `json:"text"`
	Stance          string     `json:"stance"`
	Grounding       string     `json:"grounding"`
	GroundingReason string     `json:"grounding_reason"`
	Evidence        []Evidence `json:"evidence"`
}

// Step is one inspectable reasoning step citing claims.
type Step struct {
	ID        string   `json:"id"`
	Text      string   `json:"text"`
	DependsOn []string `json:"depends_on"`
}

// Critique is the forced-refute second-pass result.
type Critique struct {
	Verdict          string   `json:"verdict"`
	Notes            string   `json:"notes"`
	DisputedClaimIDs []string `json:"disputed_claim_ids"`
}

// CodeCheck is one deterministic parse result.
type CodeCheck struct {
	Kind     string `json:"kind"`
	Language string `json:"language"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
}

// FailedAssertion is one assertion failure with expected/actual/detail.
type FailedAssertion struct {
	Type     string `json:"type"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Detail   string `json:"detail"`
}

// CacheInfo records whether this report came from the replay store.
type CacheInfo struct {
	Hit bool   `json:"hit"`
	Key string `json:"key"`
}

// Report is the machine-readable audit artifact written by every run.
type Report struct {
	ID               string            `json:"id"`
	OK               bool              `json:"ok"`
	ExitReason       string            `json:"exit_reason"`
	Prompt           string            `json:"prompt"`
	Output           string            `json:"output"`
	Claims           []Claim           `json:"claims"`
	Steps            []Step            `json:"steps"`
	Critique         Critique          `json:"critique"`
	CodeChecks       []CodeCheck       `json:"code_checks"`
	FailedAssertions []FailedAssertion `json:"failed_assertions"`
	Warnings         []string          `json:"warnings"`
	Cache            CacheInfo         `json:"cache"`
}
