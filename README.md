# aegis

**CI assertions for LLM explainability and verification.** Aegis turns a
prompt, a model output, and a set of declarative assertions into a
structured audit report and a CI exit code. It always runs four stages
(extract claims → ground against your context → rebuild reasoning steps
→ forced-refute critique), records every intermediate value, and fails
the build when a required claim is ungrounded.

Live audit:

```text
aegis run job.yaml          ──►  report.json (stdout or -o) + JUnit + exit code
aegis run job.yaml --record ──►  same, written to the replay cache
aegis run job.yaml --offline──►  replay only: no model call, no env needed
```

- **Exit 0** — all assertions passed.
- **Exit 1** — assertions failed (reasons are in the report).
- **Exit 2** — the run could not complete (bad job, cache miss in
  `--cache-only`, provider failure, missing `USER_LLM_*`, …). Stderr is
  one line: `error: <kind>: <message>`.

## Quickstart

```bash
# 1. Bring your own OpenAI-compatible endpoint.
export USER_LLM_API_KEY=sk-...
export USER_LLM_BASE_URL=https://api.example.com/v1
export USER_LLM_MODEL=my-model

# 2. Write a job.
cat > my-job.yaml <<'EOF'
id: explain-cache-key
prompt: |
  How does aegis hash a cache key?
output: |
  The cache key is sha256 of prompt, output, and spec.
context:
  inline: |
    cache key = sha256(prompt + output + spec)
assertions:
  must_claim:
    - cache key is sha256 of prompt, output, and spec
  must_not_claim:
    - cache is stored in git
  grounded: true
  max_ungrounded: 0
EOF

# 3. Run it (live), then replay it anywhere without a key.
aegis run my-job.yaml --record
aegis run my-job.yaml --offline
```

Need a dry run without any credentials at all?

```bash
aegis run my-job.yaml --fake-provider --fake-dir testdata/fixtures
```

`--fake-provider` serves canned stage responses from
`--fake-dir/<job-id>.{wrap|claims|ground|reason|critique}.json` and never
reads any environment key. The committed `testdata/` set is exactly this
against the default `.aegis/cache` replay store.

## Job spec reference

Jobs are YAML or JSON (`aegis run job.yaml|job.json|-`; `-` is stdin).

| Field | Type | Required | Meaning |
|---|---|---|---|
| `id` | string | yes* | JUnit testcase name; fixtures are keyed on it |
| `prompt` | string | yes | asked to the model when `output` is absent |
| `output` | string | no | frozen model output; if missing, Primary model is called once (wrap, temperature 0); empty completion is exit 2 |
| `context.files` | list of paths | no | read from disk; missing file is exit 2 |
| `context.inline` | string | no | treated as a synthetic file `<inline>` |
| `assertions.must_claim` | list of strings | no | each must fuzzy-match a claim: after NFKC/lower/whitespace, either your string is a substring of the claim, or token F1 ≥ 0.8; hedged claims still count |
| `assertions.must_not_claim` | list of strings | no | inversely none may match |
| `assertions.grounded` | bool (default false) | no | every `must_claim` match must be `supported` |
| `assertions.max_ungrounded` | int (default unbounded) | no | caps claims whose grounding is `ungrounded`, `not-in-context`, or `contradicted`; independent of `grounded` |
| `assertions.critique_must_agree` | bool (default false) | no | otherwise critique is informational |
| `assertions.code.parse` | bool (default false) | no | parse any Go/Python snippet found in output; parse failure → `code.parse` failed assertion |
| `model.wrap` | bool (default false) | no | if true, ignore `output` and always call the model |
| `model.critique_model` | string | no | override model id for the critique stage only |
| `schema_version` | int (default 1) | no | reserved; v1 only accepts 1 |

Unknown fields are a hard error. A job with no assertions still runs all
four stages and exits 0.

## Report schema

JSON report (stdout by default, or `-o PATH`):

```json
{
  "id": "explain-cache-key",
  "ok": true,
  "exit_reason": "ok",
  "claims": [{"id": "c0", "text": "...", "stance": "asserted",
               "grounding": "supported", "grounding_reason": "",
               "evidence": [{"path": "<inline>", "quote": "...", "line": 1}]}],
  "steps": [{"id": "s1", "text": "...", "depends_on": ["c0"]}],
  "critique": {"verdict": "agree", "notes": "...", "disputed_claim_ids": []},
  "code_checks": [],
  "failed_assertions": [],
  "warnings": [],
  "cache": {"hit": false, "key": "b8d09e..."}
}
```

`grounding` values: `supported` | `contradicted` | `not-in-context` |
`ungrounded` (with reason `invalid-evidence` or `timeout`).
`exit_reason`: `ok` | `assertions_failed` | `engine_error`.
`--junit PATH` writes a JUnit testsuite for CI.

## The replay cache and CI

Cache key = SHA-256 of `prompt` + `output` + normalized spec
(context, assertions, `critique_model`; file renames count). **Assertion
text is inside the key**: tightening `must_claim` misses cache and
forces a live run. There is no replay-old-report-with-new-assertions
path (deliberately rejected — see the design spec).

The golden CI pattern (docs/recipes give full YAML):

```yaml
# record on main — refreshes the replay cache
aegis run apps/policy-job.yaml --record

# on PRs — zero model cost, zero flake
aegis run apps/policy-job.yaml --offline
```

`--offline` (`--cache-only`) is **not a snapshot test**: the cached
report replays with its own original ok state, so pass/fail stays the
one from the recorded run. If you changed the assertion text on a PR
branch you must re-record on `main` first (see `docs/recipes/` for the
golden rule).

Garbage-collect the store: `aegis cache gc --older-than 720h`.

## Development

```bash
go test ./...
go test -run TestRunGoldenE2E ./cmd/aegis/   # committed golden e2e
AEGIS_REGEN_GOLDEN=1 go test -run TestRunGoldenRegen ./cmd/aegis/  # refresh golden
go build ./...
```

Layout:

```text
cmd/aegis/           CLI
internal/...         engine stages (claims, ground, reason, critique, codechk, assert), provider, cache
testdata/jobs       committed example jobs
testdata/fixtures   fake-provider stage fixtures for those jobs
testdata/reports    committed golden reports (regen harness included)
docs/superpowers/specs/   the full design document
```
