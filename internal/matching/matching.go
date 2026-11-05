// Package matching implements the spec's requirement-matching rule:
// normalize (Unicode NFKC, lowercase, collapse whitespace), then match iff the
// requirement is a substring of the claim or token F1 >= 0.8.
package matching

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize applies the spec's normalization: NFKC, lowercase, collapse runs of
// whitespace to a single space, trim ends.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space && b.Len() > 0 {
		b.WriteByte(' ') // trailing space, removed by TrimSpace below
	}
	return strings.TrimSpace(b.String())
}

// isCJK reports whether r is a CJK ideograph or Kana/Hangul syllable.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// tokenize splits s on whitespace, and further segments CJK runes into individual
// character tokens so non-whitespace-delimited languages can compute token F1.
func tokenize(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	var tokens []string
	for _, f := range fields {
		var word strings.Builder
		for _, r := range f {
			if isCJK(r) {
				if word.Len() > 0 {
					tokens = append(tokens, word.String())
					word.Reset()
				}
				tokens = append(tokens, string(r))
			} else {
				word.WriteRune(r)
			}
		}
		if word.Len() > 0 {
			tokens = append(tokens, word.String())
		}
	}
	return tokens
}

// TokenF1 computes F1 over tokens (whitespace-split for alphabetic text,
// character-segmented for CJK text). Both inputs must already be
// normalized by the caller for meaningful results.
func TokenF1(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	at := tokenize(a)
	bt := tokenize(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}

	// Multiset intersection (common tokens with multiplicity).
	counts := make(map[string]int, len(at))
	for _, t := range at {
		counts[t]++
	}
	common := 0
	for _, t := range bt {
		if counts[t] > 0 {
			common++
			counts[t]--
		}
	}
	if common == 0 {
		return 0
	}
	p := float64(common) / float64(len(bt))
	r := float64(common) / float64(len(at))
	return 2 * p * r / (p + r)
}

// Matches reports whether requirement matches claim per the spec: after NFKC +
// lowercase + whitespace-collapse normalization, requirement is a substring of
// claim, or token F1 >= 0.8.
func Matches(requirement, claim string) bool {
	nr, nc := Normalize(requirement), Normalize(claim)
	if nr == "" || nc == "" {
		return false
	}
	if strings.Contains(nc, nr) {
		return true
	}
	return TokenF1(nr, nc) >= 0.8
}
