package assert

import (
	"reflect"
	"testing"

	"github.com/aegis-verify/aegis/internal/report"
)

func claim(id, text, stance, grounding string) Claim {
	return Claim{ID: id, Text: text, Stance: stance, Grounding: grounding}
}

func TestEvaluatePassWithDefaults(t *testing.T) {
	in := Input{
		Claims: []Claim{claim("c0", "the sky is blue", report.StanceAsserted, report.Grounded)},
	}
	if got := Evaluate(in); len(got) != 0 {
		t.Fatalf("defaults must pass, got %v", got)
	}
}

func TestEvaluateMustClaim(t *testing.T) {
	tests := []struct {
		name   string
		in     Input
		nFails int
	}{
		{"no claims at all", Input{MustClaim: []string{"key is sha256"}}, 1},
		{"claims but no match", Input{
			MustClaim: []string{"cache key is sha256"},
			Claims:    []Claim{claim("c0", "models dream of electric sheep", report.StanceAsserted, report.Grounded)},
		}, 1},
		{"substring match", Input{
			MustClaim: []string{"cache key is sha256"},
			Claims:    []Claim{claim("c0", "The cache key is sha256 of prompt.", report.StanceAsserted, report.Grounded)},
		}, 0},
		{"f1 match", Input{
			MustClaim: []string{"a b c d"},
			Claims:    []Claim{claim("c0", "a b c d e", report.StanceAsserted, report.Grounded)},
		}, 0},
		{"hedged counts as match", Input{
			MustClaim: []string{"cache key is sha256"},
			Claims:    []Claim{claim("c0", "The cache key is sha256, probably.", report.StanceHedged, report.Grounded)},
		}, 0},
		{"hedged stance alone never matches", Input{
			MustClaim: []string{"cache key is sha256"},
			Claims:    []Claim{claim("c0", "The cache key is probably sha256-ish per the docs", report.StanceHedged, report.Grounded)},
		}, 1},
		{"two reqs one misses", Input{
			MustClaim: []string{"key is sha256", "output stored on disk"},
			Claims:    []Claim{claim("c0", "key is sha256 of spec", report.StanceAsserted, report.Grounded)},
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Evaluate(tt.in)
			if len(got) != tt.nFails {
				t.Fatalf("got %d failures %+v, want %d", len(got), got, tt.nFails)
			}
			if tt.nFails > 0 && got[0].Type != report.ATMustClaim {
				t.Fatalf("type = %s", got[0].Type)
			}
		})
	}
}

func TestEvaluateMustNotClaim(t *testing.T) {
	t.Run("no match passes", func(t *testing.T) {
		got := Evaluate(Input{
			MustNotClaim: []string{"cache in git"},
			Claims:       []Claim{claim("c0", "cache lives on disk", report.StanceAsserted, report.Grounded)},
		})
		if len(got) != 0 {
			t.Fatalf("want pass, got %v", got)
		}
	})
	t.Run("match fails with claim text", func(t *testing.T) {
		got := Evaluate(Input{
			MustNotClaim: []string{"stored in git"},
			Claims:       []Claim{claim("c1", "the cache is stored in git", report.StanceAsserted, report.Grounded)},
		})
		if len(got) != 1 || got[0].Type != report.ATMustNotClaim {
			t.Fatalf("want must_not_claim failure, got %v", got)
		}
		if got[0].Actual != "the cache is stored in git" {
			t.Fatalf("actual = %q", got[0].Actual)
		}
	})
}

func TestEvaluateGrounded(t *testing.T) {
	req := "key is sha256"
	mk := func(g string) Input {
		return Input{
			MustClaim: []string{req},
			Grounded:  true,
			Claims:    []Claim{claim("c0", "key is sha256", report.StanceAsserted, g)},
		}
	}
	for _, status := range []string{report.Contradicted, report.NotInContext, report.Ungrounded} {
		t.Run("fails on "+status, func(t *testing.T) {
			got := Evaluate(mk(status))
			if len(got) != 1 || got[0].Type != report.ATGrounded {
				t.Fatalf("want grounded failure, got %v", got)
			}
			if got[0].Actual != status || got[0].Expected != report.Grounded {
				t.Fatalf("expected/actual mismatch: %+v", got[0])
			}
		})
	}
	t.Run("passes when supported", func(t *testing.T) {
		if got := Evaluate(mk(report.Grounded)); len(got) != 0 {
			t.Fatalf("want pass, got %v", got)
		}
	})
	t.Run("flag false ignores grounding", func(t *testing.T) {
		in := Input{
			MustClaim: []string{req},
			Claims:    []Claim{claim("c0", "key is sha256", report.StanceAsserted, report.Contradicted)},
		}
		if got := Evaluate(in); len(got) != 0 {
			t.Fatalf("grounded=false must not emit grounded failures, got %v", got)
		}
	})
	t.Run("unmatched requirement skips grounded", func(t *testing.T) {
		in := Input{
			MustClaim: []string{"nothing matches this"},
			Grounded:  true,
			Claims:    []Claim{claim("c0", "other", report.StanceAsserted, report.Contradicted)},
		}
		got := Evaluate(in)
		if len(got) != 1 || got[0].Type != report.ATMustClaim {
			t.Fatalf("want only must_claim failure, got %v", got)
		}
	})
	t.Run("only offending matches emitted", func(t *testing.T) {
		in := Input{
			MustClaim: []string{req},
			Grounded:  true,
			Claims: []Claim{
				claim("c0", "key is sha256 exactly", report.StanceAsserted, report.Grounded),
				claim("c1", "key is sha256 with salt", report.StanceAsserted, report.Contradicted),
			},
		}
		got := Evaluate(in)
		if len(got) != 1 || got[0].Type != report.ATGrounded || got[0].Actual != report.Contradicted {
			t.Fatalf("want single grounded failure for c1, got %v", got)
		}
	})
}

func TestEvaluateMaxUngrounded(t *testing.T) {
	bad := []string{report.Ungrounded, report.NotInContext, report.Contradicted}
	mk := func(n int, statuses ...string) Input {
		claims := make([]Claim, 0, len(statuses))
		for i, s := range statuses {
			claims = append(claims, claim("c"+string(rune('0'+i)), "claim "+s, report.StanceAsserted, s))
		}
		mu := n
		return Input{MaxUngrounded: &mu, Claims: claims}
	}
	t.Run("nil skips", func(t *testing.T) {
		in := mk(3, bad...)
		in.MaxUngrounded = nil
		if got := Evaluate(in); len(got) != 0 {
			t.Fatalf("nil max must pass, got %v", got)
		}
	})
	t.Run("zero fails on any", func(t *testing.T) {
		got := Evaluate(mk(0, report.Ungrounded))
		if len(got) != 1 || got[0].Type != report.ATMaxUngrounded {
			t.Fatalf("want max_ungrounded failure, got %v", got)
		}
		if got[0].Expected != "<=0" || got[0].Actual != "1" {
			t.Fatalf("expected/actual: %+v", got[0])
		}
	})
	t.Run("boundary equal passes", func(t *testing.T) {
		if got := Evaluate(mk(2, report.Contradicted, report.NotInContext)); len(got) != 0 {
			t.Fatalf("want pass at limit, got %v", got)
		}
	})
	t.Run("over limit fails", func(t *testing.T) {
		got := Evaluate(mk(2, report.Contradicted, report.NotInContext, report.Ungrounded))
		if len(got) != 1 || got[0].Type != report.ATMaxUngrounded || got[0].Actual != "3" {
			t.Fatalf("want failure, got %v", got)
		}
	})
	t.Run("supported claims do not count", func(t *testing.T) {
		in := mk(0)
		in.Claims = append(in.Claims, claim("c5", "fine", report.StanceAsserted, report.Grounded))
		if got := Evaluate(in); len(got) != 0 {
			t.Fatalf("supported claims must not count, got %v", got)
		}
	})
	t.Run("count helper", func(t *testing.T) {
		if got := UngroundedCount([]Claim{
			claim("a", "x", report.StanceAsserted, report.Grounded),
			claim("b", "x", report.StanceAsserted, report.Ungrounded),
			claim("c", "x", report.StanceAsserted, report.NotInContext),
			claim("d", "x", report.StanceAsserted, report.Contradicted),
		}); got != 3 {
			t.Fatalf("count = %d, want 3", got)
		}
	})
}

func TestEvaluateCritique(t *testing.T) {
	t.Run("agree passes", func(t *testing.T) {
		got := Evaluate(Input{
			CritiqueMustAgree: true,
			Critique:          &Critique{Verdict: report.VerdictAgree, Notes: "n"},
		})
		if len(got) != 0 {
			t.Fatalf("want pass, got %v", got)
		}
	})
	t.Run("disagree fails", func(t *testing.T) {
		got := Evaluate(Input{
			CritiqueMustAgree: true,
			Critique:          &Critique{Verdict: report.VerdictDisagree},
		})
		if len(got) != 1 || got[0].Type != report.ATCritiqueMustAgree || got[0].Actual != report.VerdictDisagree {
			t.Fatalf("want critique failure, got %v", got)
		}
	})
	t.Run("mixed fails", func(t *testing.T) {
		got := Evaluate(Input{
			CritiqueMustAgree: true,
			Critique:          &Critique{Verdict: report.VerdictMixed},
		})
		if len(got) != 1 || got[0].Actual != report.VerdictMixed {
			t.Fatalf("want failure, got %v", got)
		}
	})
	t.Run("nil critique fails", func(t *testing.T) {
		got := Evaluate(Input{CritiqueMustAgree: true})
		if len(got) != 1 || got[0].Actual != "" {
			t.Fatalf("nil critique must fail, got %v", got)
		}
	})
	t.Run("flag false skips", func(t *testing.T) {
		got := Evaluate(Input{Critique: &Critique{Verdict: report.VerdictDisagree}})
		if len(got) != 0 {
			t.Fatalf("informational critique must pass, got %v", got)
		}
	})
}

func TestEvaluateCodeParse(t *testing.T) {
	t.Run("ok checks pass", func(t *testing.T) {
		got := Evaluate(Input{
			CodeChecks: []CodeCheck{{Kind: "parse", Language: "go", OK: true}, {Kind: "parse", Language: "python", OK: true}},
		})
		if len(got) != 0 {
			t.Fatalf("want pass, got %v", got)
		}
	})
	t.Run("failing check emits entry even without the flag", func(t *testing.T) {
		// Spec: "Parse failure appends failed assertion code.parse" — the
		// assertions.code.parse flag only forces the checker to run when
		// nothing was auto-detected; it does not gate emission.
		got := Evaluate(Input{
			CodeChecks: []CodeCheck{{Kind: "parse", Language: "go", OK: false, Detail: "unexpected }"}},
		})
		if len(got) != 1 || got[0].Type != report.ATCodeParse {
			t.Fatalf("want code.parse failure, got %v", got)
		}
		if got[0].Expected != "parses" || got[0].Actual != "go: unexpected }" {
			t.Fatalf("fields: %+v", got[0])
		}
	})
	t.Run("non-parse kind not counted", func(t *testing.T) {
		// Pinned contract: only parse-kind failures counted (v1 has no other
		// kinds, this guards future drift).
		got := Evaluate(Input{
			CodeChecks: []CodeCheck{{Kind: "lint", Language: "go", OK: false, Detail: "d"}},
		})
		if len(got) != 0 {
			t.Fatalf("non-parse kind must be ignored, got %v", got)
		}
	})
	t.Run("no checks and flag set passes", func(t *testing.T) {
		// Spec: parse=true with no detected code runs the checker over zero
		// snippets — pass, no failure emitted.
		if got := Evaluate(Input{}); len(got) != 0 {
			t.Fatalf("zero snippets must pass, got %v", got)
		}
	})
}

func TestEvaluateOrderingDeterministic(t *testing.T) {
	in := Input{
		MustClaim:         []string{"no such claim", "key is sha256"},
		MustNotClaim:      []string{"stored in git"},
		Grounded:          true,
		MaxUngrounded:     intPtr(0),
		CritiqueMustAgree: true,
		Claims: []Claim{
			claim("c0", "key is sha256", report.StanceAsserted, report.Contradicted),
			claim("c1", "the cache is stored in git", report.StanceAsserted, report.Grounded),
		},
		Critique:   &Critique{Verdict: report.VerdictMixed},
		CodeChecks: []CodeCheck{{Kind: "parse", Language: "python", OK: false, Detail: "SyntaxError"}},
	}
	got := Evaluate(in)
	wantTypes := []string{
		report.ATMustClaim,     // "no such claim"
		report.ATGrounded,      // c0 contradicted for "key is sha256"
		report.ATMustNotClaim,  // c1 matches
		report.ATMaxUngrounded, // 1 > 0
		report.ATCritiqueMustAgree,
		report.ATCodeParse,
	}
	var gotTypes []string
	for _, r := range got {
		gotTypes = append(gotTypes, r.Type)
	}
	if !reflect.DeepEqual(gotTypes, wantTypes) {
		t.Fatalf("type order = %v, want %v", gotTypes, wantTypes)
	}
}

func intPtr(n int) *int { return &n }
