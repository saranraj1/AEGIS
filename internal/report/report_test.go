package report

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReportJSONRoundTrip(t *testing.T) {
	r := Report{
		ID:         "explain-cache-key",
		OK:         true,
		ExitReason: "ok",
		Prompt:     "How does aegis hash a cache key?",
		Output:     "The cache key is sha256 of prompt, output, and spec.",
		Claims: []Claim{
			{
				ID:        "c0",
				Text:      "The cache key is sha256 of prompt, output, and spec.",
				Stance:    StanceAsserted,
				Grounding: Grounded,
				Evidence: []Evidence{{
					Path:  "internal/cache/key.go",
					Quote: "cache key = sha256(prompt + output + spec)",
					Line:  12,
				}},
			},
			{
				ID:              "c1",
				Text:            "Raw vendor claims are stored unencrypted",
				Stance:          StanceHedged,
				Grounding:       Ungrounded,
				GroundingReason: ReasonInvalid,
			},
		},
		Steps: []Step{{ID: "s1", Text: "Compute key from inputs", DependsOn: []string{"c0"}}},
		Critique: Critique{
			Verdict:          VerdictAgree,
			Notes:            "no refutation found",
			DisputedClaimIDs: []string{},
		},
		CodeChecks:       []CodeCheck{{Kind: "parse", Language: "go", OK: true}},
		FailedAssertions: []FailedAssertion{},
		Warnings:         []string{"context file ignored: large"},
		Cache:            CacheInfo{Hit: false, Key: "abc"},
	}
	b, err := json.Marshal(&r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ID != r.ID || back.OK != r.OK || back.ExitReason != r.ExitReason {
		t.Fatalf("round-trip mismatch: %+v", back)
	}
	if len(back.Claims) != 2 || len(back.Steps) != 1 || back.Critique.Verdict != VerdictAgree {
		t.Fatalf("nested round-trip mismatch: %+v", back)
	}
	if back.Claims[1].Grounding != Ungrounded || back.Claims[1].GroundingReason != ReasonInvalid {
		t.Fatalf("grounding matrix lost: %+v", back.Claims[1])
	}
}

func TestGroundingStatusVocabulary(t *testing.T) {
	// Pins the spec's status/reason vocabulary (incl. dual ungrounded paths).
	want := []string{Grounded, Contradicted, NotInContext, Ungrounded}
	for i, s := range want {
		if s == "" {
			t.Fatalf("status %d empty", i)
		}
	}
	if ReasonInvalid != "invalid-evidence" || ReasonTimeout != "timeout" {
		t.Fatal("reason constants drifted")
	}
}

func TestGroundingStateReasonMatrix(t *testing.T) {
	// Plan gate: the checked matrix of the 4 grounding states × reason states
	// must round-trip through the JSON contract losslessly.
	for _, state := range []string{Grounded, Contradicted, NotInContext, Ungrounded} {
		for _, reason := range []string{"", ReasonInvalid, ReasonTimeout} {
			c := Claim{ID: "c0", Text: "t", Stance: StanceAsserted, Grounding: state, GroundingReason: reason}
			b, err := json.Marshal(&c)
			if err != nil {
				t.Fatalf("marshal %s/%s: %v", state, reason, err)
			}
			var back Claim
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatalf("unmarshal %s/%s: %v", state, reason, err)
			}
			if back.Grounding != state || back.GroundingReason != reason {
				t.Fatalf("matrix loss: %s/%q -> %s/%q", state, reason, back.Grounding, back.GroundingReason)
			}
		}
	}
}

func TestJUnitPass(t *testing.T) {
	r := &Report{ID: "pass-job", OK: true, ExitReason: "ok", FailedAssertions: []FailedAssertion{}}
	xmlDoc := JUnitSuite([]*Report{r})
	s := string(xmlDoc)
	if !strings.Contains(s, `<testsuite name="aegis" tests="1" failures="0" id="1">`) {
		t.Fatalf("suite header wrong:\n%s", s)
	}
	if !strings.Contains(s, `<testcase name="pass-job" classname="aegis"/>`) {
		t.Fatalf("pass testcase wrong:\n%s", s)
	}
}

func TestJUnitMultiFailure(t *testing.T) {
	r := &Report{
		ID: "fail-job",
		FailedAssertions: []FailedAssertion{
			{Type: ATMustClaim, Expected: "x", Actual: "no match", Detail: "no claim matched"},
			{Type: ATCodeParse, Expected: "parses", Actual: "syntax error", Detail: `line 3: unexpected "eof"`},
		},
	}
	s := string(JUnitSuite([]*Report{r}))
	for _, frag := range []string{
		`tests="1" failures="2"`,
		`<failure type="must_claim"`,
		`<failure type="code.parse"`,
		`line 3: unexpected &#34;eof&#34;`,
	} {
		if !strings.Contains(s, frag) {
			t.Fatalf("missing %q in:\n%s", frag, s)
		}
	}
	// Attribute values use quoting; must not inject raw quotes.
	if strings.Contains(s, `message="... "eof""`) {
		t.Fatal("raw quote leaked into attribute")
	}
}

func TestJUnitEscapedJobID(t *testing.T) {
	r := &Report{ID: `<id & "quoted">`}
	s := string(JUnitSuite([]*Report{r}))
	if !strings.Contains(s, `name="&lt;id &amp; &#34;quoted&#34;&gt;"`) {
		t.Fatalf("job id not escaped:\n%s", s)
	}
}

func TestJUnitMultipleJobs(t *testing.T) {
	jobs := []*Report{
		{ID: "a", FailedAssertions: []FailedAssertion{}},
		{ID: "b", FailedAssertions: []FailedAssertion{{Type: ATGrounded}}},
		{ID: "c", FailedAssertions: []FailedAssertion{}},
	}
	s := string(JUnitSuite(jobs))
	if !strings.Contains(s, `tests="3" failures="1"`) {
		t.Fatalf("counts wrong:\n%s", s)
	}
	if !strings.Contains(s, `<testcase name="c" classname="aegis"/>`) {
		t.Fatalf("third testcase wrong")
	}
}
