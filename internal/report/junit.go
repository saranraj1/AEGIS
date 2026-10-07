package report

import (
	"encoding/xml"
	"strings"
)

// junitText escapes and renders text content for XML.
func junitText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&escapeWriter{b: &b}, []byte(s))
	return b.String()
}

type escapeWriter struct{ b *strings.Builder }

func (w *escapeWriter) Write(p []byte) (int, error) {
	w.b.Write(p)
	return len(p), nil
}

// JUnitSuite renders the canonical JUnit XML for a run: one <testsuite
// name="aegis"> containing one <testcase> per job (name = job id; classname =
// "aegis"). Failed assertions produce one <failure> node each, with JUnit's
// type=>assertion type and message combining expected/actual. A pass yields a
// bare testcase node.
func JUnitSuite(jobs []*Report) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	total := 0
	failures := 0
	for _, r := range jobs {
		total++
		failures += len(r.FailedAssertions)
	}
	b.WriteString(`<testsuite name="aegis" tests="` + itoa(total) +
		`" failures="` + itoa(failures) + `" id="1">` + "\n")
	for _, r := range jobs {
		b.WriteString(`  <testcase name="` + junitText(r.ID) + `" classname="aegis"`)
		if len(r.FailedAssertions) == 0 {
			b.WriteString(`/>` + "\n")
			continue
		}
		b.WriteString(`>` + "\n")
		for _, fa := range r.FailedAssertions {
			msg := fa.Type
			if fa.Expected != "" || fa.Actual != "" {
				msg += ": expected=[" + fa.Expected + "] actual=[" + fa.Actual + "]"
			}
			if fa.Detail != "" {
				msg += " detail=" + fa.Detail
			}
			b.WriteString(`    <failure type="` + junitText(fa.Type) +
				`" message="` + junitText(msg) + `">` + junitText(fa.Detail) + "\n" +
				"    </failure>\n")
		}
		b.WriteString(`  </testcase>` + "\n")
	}
	b.WriteString(`</testsuite>` + "\n")
	return []byte(b.String())
}

// itoa avoids a strconv import for two integer renderings.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
