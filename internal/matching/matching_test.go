package matching

import (
	"fmt"
	"testing"
)

func TestMatches(t *testing.T) {
	tests := []struct {
		req, claim string
		want       bool
	}{
		// Substring direction: requirement inside claim (spec-mandated).
		{
			req:   "cache key is sha256 of prompt, output, and spec",
			claim: "The cache key is sha256 of prompt, output, and spec.",
			want:  true,
		},
		{
			req:   "Cache Key is SHA256",
			claim: "the cache key is sha256 of everything",
			want:  true,
		},
		{
			req:   "cache   key\nis  sha256",
			claim: "the cache key\tis sha256 hashing",
			want:  true,
		},
		{
			// Reverse direction must NOT count (claim inside requirement).
			req:   "the entire sentence with extra words",
			claim: "the entire sentence",
			want:  false, // F1: 3/7-p,3/3-r = 2*(3/7)/(3/7+1) = 0.6 < 0.8
		},
		// Token F1 path.
		{
			req:   "key stores data in git",
			claim: "key stores data on disk",
			want:  false, // common=3/5 (in≠on, git≠disk): p=r=0.6 => F1=0.6
		},
		{
			req:   "sha256 of prompt output spec",
			claim: "sha256 of prompt, output and spec",
			want:  false, // whitespace-split keeps punctuation: common=4, p=4/7, r=4/5 => 0.667
		},
		{
			req:   "a b c d",
			claim: "a b c d e",
			want:  true, // common=4, p=4/5, r=1 => 8/9 = 0.889 >= 0.8
		},
		{
			req:   "b a c d",
			claim: "e d c a b",
			want:  true, // order-free, common=4: p=4/5, r=4/4=1 => F1=0.889 >= 0.8
		},
		{
			req:   "one two three four",
			claim: "one two three x y",
			want:  false, // common=3: p=3/5, r=3/4 => 0.667 < 0.8
		},
		{
			req:   "cache key stored in git",
			claim: "cache key computed from files",
			want:  false, // 2/5 shared: F1=0.4
		},
		{
			req:   "hash hash hash key",
			claim: "hash hash hash value",
			want:  false, // multiset common=3: p=r=3/4 => 0.75
		},
		{
			req:   "hash hash key",
			claim: "hash hash hash key",
			want:  true, // multiset common=3: p=3/4, r=1 => 0.857
		},
		{
			// Hedged phrasing still counts only if threshold met; here it fails
			// (4/7 shared: p=0.571, r=0.8 => 0.667) and no substring.
			req:   "cache is stored in git",
			claim: "the cache might be stored in git",
			want:  false,
		},
		// NFKC folding (x/text): fullwidth letters and proper compatibility chars.
		{
			req:   "ｃａｃｈｅ key",
			claim: "cache ｋｅｙ is hashed",
			want:  true, // both fold to ascii; substring "cache key"
		},
		{
			req:   "version ①",
			claim: "version 1 of the spec",
			want:  true, // circled one folds to "1"
		},
		{
			req:   "ﬁle cached",
			claim: "file cached on disk",
			want:  true, // fi-ligature folds to "file"
		},
		// Empty / whitespace-only inputs never match.
		{req: "", claim: "something", want: false},
		{req: "something", claim: "", want: false},
		{req: "   ", claim: "something", want: false},
		{req: "\t\n", claim: "   something   ", want: false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q~%q", tt.req, tt.claim), func(t *testing.T) {
			if got := Matches(tt.req, tt.claim); got != tt.want {
				t.Fatalf("Matches(%q, %q) = %v, want %v", tt.req, tt.claim, got, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"  Hello   World  ", "hello world"},
		{"a\n\tb\r\nc", "a b c"},
		{"ＦＵＬＬＷＩＤＴＨ", "fullwidth"}, // NFKC fullwidth -> ascii
		{"① one", "1 one"},         // circled digit decomposes
		{"ﬁle", "file"},            // ligature decomposes
		{"naïve", "naïve"},         // unchanged stay same
		{"no change", "no change"},
		{"", ""},
		{" \t\n ", ""},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Fatalf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTokenF1(t *testing.T) {
	f1 := func(a, b string) float64 { return TokenF1(Normalize(a), Normalize(b)) }
	tests := []struct {
		name   string
		a, b   string
		lo, hi float64
	}{
		{"identical", "a b c", "a b c", 1.0, 1.0},
		{"order-free", "a b c", "c b a", 1.0, 1.0},
		{"half overlap", "a b", "a b c d", 0.666, 0.667}, // p=.5, r=1 => 2/3
		{"disjoint", "a b", "c d", 0.0, 0.0},
		{"empty first", "", "x", 0.0, 0.0},
		{"empty second", "x", "", 0.0, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := f1(tt.a, tt.b)
			if got < tt.lo || got > tt.hi {
				t.Fatalf("TokenF1(%q, %q) = %.4f, want in [%.4f, %.4f]", tt.a, tt.b, got, tt.lo, tt.hi)
			}
		})
	}
}
