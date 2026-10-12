# Aegis: CI assertions for LLM explainability and verification

Date: 2026-10-06
Status: approved design
Audience: developers who need to inspect, debug, and audit model outputs in CI

## Problem

Model output is opaque. Developers cannot treat a completion as a testable artifact: claims are mixed with hedging, reasoning is hidden, and a second look is ad hoc. Existing eval harnesses score similarity or check regex; they do not produce an inspectable audit trail or fail a build when a required claim is ungrounded.

## Goal

Aegis is a verification engine that turns a prompt, a model output, and a set of assertions into a structured audit report and a CI exit code.

Every uncached run always performs four stages:

1. Extract atomic claims from the output.
2. Ground each claim against provided context only.
3. Rebuild inspectable reasoning steps that cite claims.
4. Critique: a second pass that is forced to attempt a refute.

If the output looks like code, deterministic code checkers also run. They never replace the four stages.

Success for v1: a developer can declare `must_claim` / `must_not_claim` / `grounded` in YAML or pytest, run `aegis run` or `pytest` in GitHub Actions, and get a machine-readable report plus JUnit. After a recorded run, `--cache-only` replays without a live model.

## Non-goals (v1)

- IDE extension, web UI, or interactive step debugger
- Silent web fetch or any retrieval beyond files/inline context the job provides
- Snapshot-diff as the primary contract (cache replay is not a snapshot test)
- Multi-agent traces as a first-class job type
- Bundled or platform LLM keys; the user always brings `USER_LLM_*`
- Live provider tests in the default test suite
- Real compiler toolchains beyond parsing a snippet

## Users and form factor

Primary user: developers.

Product: Go CLI binary (`aegis`) as the only engine, plus a thin Python library / pytest plugin. Python never calls an LLM. TypeScript client is out of v1; the JSON report schema is the stable contract for a future client.

Typical loop in CI:

- Record on main: live audit, write cache.
- PRs: `--cache-only` unless prompt/output/spec hash changed.

## Architecture

```text
pytest  ──┐
YAML    ──┼──►  aegis run  ──►  report.json + JUnit + exit code
JSON    ──┤         │
stdin   ──┘         ├── Provider (OpenAI-compatible HTTP)
                    └── cache/ (sha256 of prompt + output + normalized spec)
```

The binary owns providers, cache, timeouts, retries, and assertion evaluation.

Python `aegis.verify()` writes a temp job file, invokes the binary, parses `report.json`, and raises pytest assertion failures with disagreeing claims inlined.

### Process isolation

Each pipeline stage is a Go package with a pure function over typed values. LLM I/O sits behind one `Provider` interface so tests inject a fake.

| Package | Responsibility | Depends on |
|---------|----------------|------------|
| `internal/job` | Parse YAML/JSON job, resolve context files, validate schema | none |
| `internal/provider` | OpenAI-compatible chat completions; fake provider | none |
| `internal/cache` | Hash, get, put, gc | none |
| `internal/claims` | Extract claims; drop claims not present in output text | provider |
| `internal/ground` | Supported / contradicted / not-in-context per claim | provider, context |
| `internal/reason` | Step list with `depends_on`; reject unknown ids | provider |
| `internal/critique` | Forced refute; agree / disagree / mixed | provider |
| `internal/codechk` | Detect code; parse snippets | none |
| `internal/assert` | Evaluate assertions against the report | none |
| `internal/report` | JSON report + JUnit writer | none |
| `internal/engine` | Orchestrate stages, cache, wrap-call, timeouts | all of the above |
| `cmd/aegis` | CLI: `run`, `cache gc` | engine |

Python package `aegis` contains only: job writer, subprocess runner, report dataclass, pytest plugin. It must not import an HTTP client.

## Job spec

Jobs are YAML or JSON. Pytest writes the same schema to a temp file.

```yaml
id: explain-cache-key
prompt: |
  How does aegis hash a cache key?
output: |
  The cache key is sha256 of prompt, output, and spec.
context:
  files:
    - internal/cache/key.go
  inline: |
    cache key = sha256(prompt + output + spec)
assertions:
  must_claim:
    - cache key is sha256 of prompt, output, and spec
  must_not_claim:
    - cache is stored in git
  grounded: true
  max_ungrounded: 0
  critique_must_agree: false
  code:
    parse: true
model:
  wrap: false
  critique_model: null
```

### Field rules

- `id`: required in YAML/JSON. Used as the JUnit testcase name. Python `verify()` may omit it (defaults to the pytest node id, else `job`).
- `prompt`: required string.
- `output`: optional. If omitted, the engine calls the primary model once with `prompt` and freezes that text as `output` before hashing. Wrap uses temperature 0.
- `context.files`: optional list of paths relative to the process working directory. Engine reads them and stores `{path, text, line_count}`. Missing file is a hard error (exit 2).
- `context.inline`: optional string treated as a synthetic file named `<inline>`.
- `assertions.must_claim`: list of strings. Each must fuzzy-match at least one extracted claim.
- `assertions.must_not_claim`: list of strings. Each must not fuzzy-match any extracted claim.
- `assertions.grounded`: if true, every `must_claim` match must have grounding status `supported`. Default false.
- `assertions.max_ungrounded`: integer, default `null` (unlimited). Counts extracted claims whose `grounding` is `ungrounded`, `not-in-context`, or `contradicted`. Independent of `grounded`.
- `assertions.critique_must_agree`: if true, critique verdict must be `agree`. Default false (critique is informational).
- `assertions.code.parse`: if true, run the parse checker even if code was not auto-detected. Default false.
- `model.wrap`: if true, ignore a provided `output` and always call the model. Default false.
- `model.critique_model`: optional override model id for the critique stage only. Null means same as primary.

Unknown fields are a hard error.

A job with no `assertions` key (or all assertion lists empty and flags at defaults) still runs the four stages and exits 0. Empty extracted claims is valid; any non-empty `must_claim` then fails.

### Fuzzy match

A required string matches a claim if, after Unicode NFKC, lowercasing, and collapsing whitespace:

- the required string is a substring of the claim text, or
- token F1 (whitespace-split) is >= 0.8.

The requirement is matched against each claim (requirement substring-of-claim or F1), not claim-against-requirement. Hedged claims (`stance: hedged`) still count as matches.

## Pipeline

1. **Load.** Parse job. Resolve context files. If `output` is missing or `model.wrap` is true, call primary model once; empty completion is exit 2. Compute cache key.
2. **Cache lookup.** If a report exists for the key and cache is enabled, return that report as-is (including its original `failed_assertions` and `ok`). Assertion text is part of the hash, so tightening a spec misses cache and requires a live run. There is no replay-old-report-with-new-assertions path in v1.
3. **Claims.** Seed the prompt with a deterministic sentence/bullet split of `output`. One structured LLM call returns JSON `[{text, stance}]` where stance is `asserted` or `hedged`. Engine drops any claim whose normalized text is not a substring of the normalized output (prevents invented claims). Duplicate claims (same normalized text) are merged, keeping `asserted` over `hedged`.
4. **Ground.** Context only; no web. For each remaining claim, batched LLM call returns `supported` | `contradicted` | `not-in-context` plus an evidence quote. Engine verifies the quote is a substring of some context file (or `<inline>`); if not, evidence is discarded and status becomes `ungrounded` with reason `invalid-evidence`. Token-overlap ranking of context chunks (sliding windows of 40 lines, stride 20) selects what is sent to the model when context exceeds 8k tokens; the quote check still searches full files.
5. **Reasoning.** One LLM call returns `[{id, text, depends_on}]`. `id` must match `^[A-Za-z][A-Za-z0-9_-]{0,31}$`. `depends_on` entries must be claim indexes (`c0`, `c1`, … assigned in extraction order) or earlier step ids. Unknown ids: the step is dropped and a warning is recorded; the run continues. Empty step list is allowed.
6. **Critique.** One LLM call receives prompt, output, claims with evidence, and steps. The system prompt requires an attempted refute. JSON: `{verdict: agree|disagree|mixed, notes: string, disputed_claim_ids: []}`. Critique does not rewrite output and does not add claims.
7. **Code checkers.** After stage 6 (not instead of it). If `assertions.code.parse` is true, or the output contains a fenced code block or a run of 3+ lines that parse as Python or Go, parse those snippets. Go: `go/parser` in-process. Python: subprocess `python3 -c` with `ast.parse`; if `python3` is missing and a Python snippet must be parsed, record `code.parse` failure (when `assertions.code.parse` is true) or a warning (when auto-detected only). Other languages: ignore. Parse failure appends failed assertion `code.parse`.
8. **Assert.** Evaluate assertions. Write `report.json` (stdout unless `-o`), optional JUnit, optional cache put. Exit 0 if `ok`, 1 if assertions failed, 2 if the run could not complete.

Default uncached LLM calls: 3 (claims, reasoning, critique) + 1 batched grounding call. Temperature 0 on every audit and wrap call.

## Cache

Key: SHA-256 hex of the UTF-8 concatenation:

```text
v1\0
prompt\0
output\0
normalized_spec\0
```

`normalized_spec` is canonical JSON of: `context.inline`, sorted `context.files` contents (path + file bytes), `assertions` object, `model.critique_model`. File paths are included so renaming a context file misses cache. `id` and `model.wrap` are not part of the hash (`wrap` only affects whether output is produced before hashing).

Store: directory from `--cache-dir` (default `.aegis/cache`). One file per key: `<key>.json` containing the full report plus `cached_at` RFC3339.

Flags:

- `--cache` (default true): read/write cache.
- `--no-cache`: neither read nor write.
- `--cache-only`: read only; missing key is exit 2. No provider calls.
- `--record`: same as default (`--cache`): read cache, live provider on miss, then write. Exists so CI YAML can say `--record` on main.

`aegis cache gc --older-than 720h` deletes entries by `cached_at`.

Assertions are inside the hash. Changing `must_claim` is a new key. There is no “replay old report with new assertions” path in v1; that was considered and rejected because it made pass/fail depend on a report that might not contain newly required claims. Document this explicitly in the CLI help.

## Report schema

JSON object:

```json
{
  "id": "explain-cache-key",
  "ok": true,
  "exit_reason": "ok",
  "prompt": "...",
  "output": "...",
  "claims": [
    {
      "id": "c0",
      "text": "...",
      "stance": "asserted",
        "grounding": "supported",
        "grounding_reason": "",
      "evidence": [{"path": "internal/cache/key.go", "quote": "...", "line": 12}]
    }
  ],
  "steps": [
    {"id": "s1", "text": "...", "depends_on": ["c0"]}
  ],
  "critique": {
    "verdict": "agree",
    "notes": "...",
    "disputed_claim_ids": []
  },
  "code_checks": [
    {"kind": "parse", "language": "go", "ok": true, "detail": ""}
  ],
  "failed_assertions": [],
  "warnings": [],
  "cache": {"hit": false, "key": "abc..."}
}
```

`grounding` per claim: `supported` | `contradicted` | `not-in-context` | `ungrounded`. `ungrounded` is the status after invalid evidence or grounding timeout. `grounding_reason` is empty or `invalid-evidence` | `timeout`.

`exit_reason`: `ok` | `assertions_failed` | `engine_error`.

`failed_assertions` items: `{type, expected, actual, detail}` where `type` is one of `must_claim`, `must_not_claim`, `grounded`, `max_ungrounded`, `critique_must_agree`, `code.parse`.

JUnit: one testsuite `aegis`, one testcase per job `id`. Failures serialize `failed_assertions`.

## CLI

```text
aegis run <job.yaml|job.json|-> [flags]
aegis cache gc --older-than duration
```

`run` flags:

- `-o, --output` report JSON path (default stdout)
- `--junit` path
- `--cache-dir` (default `.aegis/cache`)
- `--cache` / `--no-cache` / `--cache-only` / `--record`
- `--timeout` whole-run timeout (default 120s)
- `--stage-timeout` per LLM stage (default 45s)
- `--offline` alias of `--cache-only`
- `--fake-provider` use canned JSON instead of HTTP
- `--fake-dir` directory of canned stage responses (see Fake provider files)

Exit codes: 0 pass, 1 assertion failure, 2 engine error (including cache miss in cache-only, missing key, bad YAML).

Multiple jobs: `aegis run` accepts one file per invocation in v1. A directory or glob is out of v1; CI loops or pytest collects instead.

## Python API

Package name: `aegis`.

```python
import aegis

def test_cache_key_explanation():
    r = aegis.verify(
        prompt="How does aegis hash a cache key?",
        output=model_text,
        context_files=["internal/cache/key.go"],
        must_claim=["cache key is sha256 of prompt, output, and spec"],
        must_not_claim=["cache is stored in git"],
        grounded=True,
        max_ungrounded=0,
        critique_must_agree=False,
        code_parse=False,
        binary="aegis",
    )
    assert r.ok
```

`verify()` kwargs map 1:1 onto the job schema. Optional `id` defaults to the pytest node id when collected, else `job`. `binary` is looked up on `PATH` unless set via `AEGIS_BINARY`. Extra kwargs: `cache_dir`, `cache_only`, `timeout`, `fake_provider`, `fake_dir`, passed as CLI flags.

On `r.ok is False`, pytest plugin (activated by installing `aegis` with extra `pytest` or by `pytest_plugins`) rewrites the failure to show `failed_assertions`. Direct `assert r.ok` is enough; the plugin is optional sugar.

`aegis.verify()` must not import `httpx`, `requests`, `openai`, or equivalent.

## Provider and secrets

OpenAI-compatible `POST {base}/chat/completions`. Request: one user message (plus a fixed system message per stage), `temperature: 0`, `response_format: json_object` when the endpoint supports it.

Environment (user project, never Agent platform keys):

- `USER_LLM_API_KEY` (required for live calls)
- `USER_LLM_BASE_URL` (required for live calls, e.g. `https://api.example.com/v1`)
- `USER_LLM_MODEL` (required for live calls)

No default base URL or model id is hardcoded. Missing any of the three when a live call is required is exit 2 with a message that names the missing variable, not the value.

The key is never written to reports, logs, JUnit, or cache files. Specs that contain a field resembling an API key are not scanned; we simply do not accept a `api_key` field (unknown field error).

Fake provider: `AEGIS_FAKE_PROVIDER=1` or `--fake-provider` plus `--fake-dir`. Does not read `USER_LLM_*`.

Fake provider files under `--fake-dir`, named `{job-id}.{stage}.json` where stage is `wrap` | `claims` | `ground` | `reason` | `critique`. Missing file for a required stage is exit 2. `ground.json` is an array of `{claim_id, grounding, quote}` in claim order if `claim_id` is omitted.

## Errors, retries, flake

Hard fail (exit 2): bad YAML/JSON, unknown fields, missing context file, empty wrap output, missing `USER_LLM_*` when live, provider 4xx other than 429, JSON that still fails after one repair retry, whole-run timeout.

Retry: 429 and 5xx, exponential backoff, max 3 attempts, then exit 2.

Claim JSON parse fail: one “repair this JSON” call, then exit 2.

Grounding: if the batched call hits `--stage-timeout`, all claims in that batch become `ungrounded` with reason `timeout`; assertions then decide pass/fail (exit 1 or 0, not 2).

## Code detection

Auto-detect if:

- markdown fenced block with language `go`, `golang`, `py`, `python`, or empty language, or
- three consecutive lines that `go/parser` or Python `ast` accepts as a fragment (function, extra-wrapped).

Languages in v1: Go and Python only. Other fences are ignored by codechk.

## Testing

Go:

- Fake `Provider` in all `go test` runs. Default suite makes zero network calls.
- Table tests: claim drop of invented text; grounding quote rejection; reasoning unknown id drop; assert evaluator for each assertion type; cache hit/miss when assertion text changes.
- CLI golden: `aegis run testdata/*.yaml --fake-provider` compared to committed reports.

Python:

- Stub binary on `PATH` that writes a fixture report.
- Assert `aegis` module does not import HTTP clients.
- One example test under `examples/` using the stub.

Not in v1 tests: live provider, real `go test` of user snippets, web retrieval.

## Repository layout

```text
cmd/aegis/           main.go
internal/...         packages listed above
python/aegis/        library + pytest plugin
testdata/jobs/       YAML fixtures
testdata/reports/    golden reports
examples/            pytest example with stub
docs/superpowers/specs/2026-10-06-aegis-design.md
```

Go module path: `github.com/aegis-verify/aegis` as a placeholder; replace if the user sets a real module path at implementation time. Python package install name: `aegis-verify` on PyPI placeholder, import `aegis`.

## v1 cut line

In: `aegis run`, `aegis cache gc`, YAML+JSON jobs, pytest plugin, four-stage audit, Go/Python parse checker, cache-only CI mode, JUnit, fake provider, BYO OpenAI-compatible key.

Out: everything in Non-goals.

## Implementation notes (decisions already made)

- Working name: aegis
- Language split: Go engine, Python lib
- Provider: BYO `USER_LLM_*`, OpenAI-compatible
- Cache-only on PRs, record on main, is the documented CI pattern, not enforced by the tool
- No silent web
- Critique is informational unless `critique_must_agree: true`
- One job per `aegis run` invocation
- Assertion text is part of the cache key