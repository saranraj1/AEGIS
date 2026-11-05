// Package codechk implements stage 7: deterministic code parsing. It never
// replaces the four LLM stages.
//
// Detection: a markdown fenced block with language go/golang/py/python (or
// an empty language), or a run of 3+ consecutive non-blank lines that parse
// as a Go or Python fragment. Detection runs parse for the run heuristic —
// a run that fails to parse is simply not code, so it produces no check.
// A NAMED fence is always checked: a named fence that fails its parser
// yields a failing check (which appends failed assertion code.parse).
//
// Parsing: Go via go/parser in-process (trying a package wrapper and a
// function wrapper for fragments); Python via a subprocess
// `python3 -c "import ast..."`. If python3 is missing and a Python snippet
// must be parsed: a code.parse failure when assertions.code.parse is true,
// otherwise a warning (auto-detected only).
package codechk

import (
	"bytes"
	"context"
	"go/parser"
	"go/scanner"
	"go/token"
	"os/exec"
	"strings"
	"time"

	"github.com/aegis-verify/aegis/internal/report"
)

// Kind is the only check kind in v1.
const Kind = "parse"

const pythonTimeout = 5 * time.Second

// Options tunes the checker; PythonBin overrides the python3 lookup
// (tests inject fake or missing binaries).
type Options struct {
	PythonBin string
}

// Run parses detected snippets and returns checks plus warnings. force is
// assertions.code.parse: with no detected snippets it changes nothing (zero
// snippets parse trivially); it only matters for the missing-python rule.
func Run(ctx context.Context, output string, force bool, opts Options) ([]report.CodeCheck, []string) {
	var checks []report.CodeCheck
	var warnings []string
	pyOK, pyBin := resolvePython(opts.PythonBin)

	for _, cand := range candidates(output) {
		switch {
		case cand.lang == "go":
			ok, detail := parseGo(cand.code)
			checks = append(checks, report.CodeCheck{Kind: Kind, Language: "go", OK: ok, Detail: detail})
		case cand.lang == "python":
			if !pyOK {
				if force {
					checks = append(checks, report.CodeCheck{
						Kind: Kind, Language: "python", OK: false,
						Detail: "python3 not found",
					})
				} else {
					warnings = append(warnings, "codechk: python3 not found; python snippet not parsed")
				}
				continue
			}
			ok, detail := parsePython(ctx, pyBin, cand.code)
			checks = append(checks, report.CodeCheck{Kind: Kind, Language: "python", OK: ok, Detail: detail})
		default:
			// Empty-language fence or bare line run: auto-detect only.
			if ok, _ := parseGo(cand.code); ok {
				checks = append(checks, report.CodeCheck{Kind: Kind, Language: "go", OK: true})
				continue
			}
			if pyOK {
				if ok, _ := parsePython(ctx, pyBin, cand.code); ok {
					checks = append(checks, report.CodeCheck{Kind: Kind, Language: "python", OK: true})
				}
			}
		}
	}
	return checks, warnings
}

// candidate is one region of output that may be code.
type candidate struct {
	lang string // "go" | "python" | "" (unknown/empty)
	code string
}

// candidates extracts fenced blocks and 3+ line runs in document order.
func candidates(output string) []candidate {
	lines := strings.Split(output, "\n")
	var out []candidate
	var run []string // current non-fence run of non-blank lines
	flushRun := func() {
		if len(run) >= 3 {
			out = append(out, candidate{lang: "", code: strings.Join(run, "\n")})
		}
		run = run[:0]
	}

	inFence := false
	fenceLang := ""
	var fence []string
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if !inFence && strings.HasPrefix(trimmed, "```") {
			flushRun()
			inFence = true
			fenceLang = normalizeLang(strings.TrimSpace(trimmed[3:]))
			fence = nil
			continue
		}
		if inFence {
			if strings.HasPrefix(trimmed, "```") {
				// Close the fence and classify it.
				switch fenceLang {
				case "go", "python":
					out = append(out, candidate{lang: fenceLang, code: strings.Join(fence, "\n")})
				case "":
					// Unknown-language or empty fence: auto-detect only.
					out = append(out, candidate{lang: "", code: strings.Join(fence, "\n")})
				default:
					// Other languages are ignored by codechk in v1.
				}
				inFence = false
				fence = nil
				continue
			}
			fence = append(fence, raw)
			continue
		}
		if trimmed == "" {
			flushRun()
			continue
		}
		run = append(run, raw)
	}
	if inFence {
		// Unterminated fence: treat the remainder as the block.
		switch fenceLang {
		case "go", "python":
			out = append(out, candidate{lang: fenceLang, code: strings.Join(fence, "\n")})
		case "":
			out = append(out, candidate{lang: "", code: strings.Join(fence, "\n")})
		}
	}
	flushRun()
	return out
}

func normalizeLang(lang string) string {
	switch strings.ToLower(lang) {
	case "go", "golang":
		return "go"
	case "py", "python":
		return "python"
	}
	return strings.ToLower(lang)
}

// parseGo parses code with go/parser, trying a bare (package-prefixed when
// needed) form and then a function wrapper for statement fragments.
func parseGo(code string) (bool, string) {
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return false, "empty snippet"
	}
	src := trimmed
	if !strings.HasPrefix(trimmed, "package ") {
		src = "package p\n" + trimmed
	}
	attempts := []string{
		src,
		"package p\nfunc _aegis_wrap() {\n" + trimmed + "\n}",
	}
	var firstErr string
	for i, a := range attempts {
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, "snippet.go", a, parser.AllErrors); err == nil {
			return true, ""
		} else if i == 0 {
			firstErr = goErrString(err)
		}
	}
	return false, firstErr
}

func goErrString(err error) string {
	if list, ok := err.(scanner.ErrorList); ok && len(list) > 0 {
		return capDetail(list[0].Error())
	}
	return capDetail(err.Error())
}

// parsePython parses code via a subprocess `python -c "import ast..."`.
func parsePython(ctx context.Context, pyBin, code string) (bool, string) {
	cctx, cancel := context.WithTimeout(ctx, pythonTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, pyBin, "-c", "import ast,sys; ast.parse(sys.stdin.read())")
	cmd.Stdin = strings.NewReader(code)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return true, ""
	}
	detail := strings.TrimSpace(stderr.String())
	if detail == "" {
		detail = "python parse failed"
	}
	return false, capDetail(detail)
}

// resolvePython finds the python interpreter. An explicit binary that does
// not exist resolves to "missing" so tests can pin the missing-python rule.
func resolvePython(explicit string) (bool, string) {
	names := []string{"python3", "python", "py"}
	if explicit != "" {
		names = []string{explicit}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return true, p
		}
	}
	return false, ""
}

func capDetail(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
