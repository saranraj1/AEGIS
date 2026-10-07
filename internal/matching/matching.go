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

// TokenF1 computes F1 over whitespace-split tokens. Both inputs must already be
// normalized by the caller for meaningful results.
func TokenF1(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	at := strings.Fields(a)
	bt := strings.Fields(b)
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
