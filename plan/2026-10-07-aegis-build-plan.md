# Aegis v1 — Build Plan (2026-10-07)

Derived from `Project Scope.txt` (approved design, 2026-10-06). This document does three things:
1. Challenges the design where it will hurt during implementation.
2. Locks a set of working assumptions (vetoable, recorded here).
3. Scopes the work into a phased, high-risk-first plan with verification gates.

## 0. Context

- Repository is empty (greenfield): no code, no git history, no toolchain files. The approved
  spec is the only artifact. Everything is built from scratch to the spec.
- Consequence: package layout, naming, CI, and the quality bar are decisions to make now,
  not adaptations to existing code.

## 1. Assessment of the design

The core idea is sound and unusually well-cut for v1: four fixed audit stages, one binary
owning all I/O, Python as a thin subprocess shim that must not import HTTP clients, report
JSON as the only stable contract, fake-provider-driven tests with zero network in the
default suite, and an explicit, honest non-goals list. I would not change the four stages,
the cache-hash model, or the process-isolation architecture. What follows are the places
where the letter of the spec conflicts with its execution reality, drifts mid-document, or
under-specifies enough to matter once you start building.

## 2. Challenges (C1–C13)

C1 — Cache-only determinism is weaker than it looks.
The spec says "PRs: `--cache-only` unless prompt/output/spec hash changed." A one-line
non-whitespace prompt tweak (e.g. adding "please") forces a live run. Because changes are
computed, not recorded, reproducibility depends entirely on hash discipline: a different
Go/JSON key order in `normalized_spec` produces a different cache key. Canonical JSON must
sort keys and number-encode deterministically. Risk is drifting to zero hit rate on PRs,
which defeats the CI loop.

C2 — The documented CI loop has a dead end. Assertions live inside the cache key, so
tightening a `must_claim` creates a new key. But the PR flow is `--cache-only`, which
never runs live — a tightened assertion on a PR fails with exit 2 (cache miss) instead
of evaluating the new assertion against the already-recorded output. Worse, the Goal
section's "PRs: --cache-only unless prompt/output/spec hash changed" contradicts the
flags section, where `--cache-only` misses are exit 2 with no live fallback. The spec
should pick one: strict replay (with documented healing via record-on-main) or
conditional liveness on change.
Assumption (frozen): strict replay wins — record-on-main is the only path that adopts
assertion changes; the README shows the healing recipe; no conditional live mode in v1.

C3 — Non-determinism inside the pipeline: "temperature 0 everywhere" (documented) does not
protect the cache story, since the cache key includes the *output* — not the audit results.
Two runs with wrap=true can hit the same key only if the model returns the same bytes.
Fine for freeze-output wraps; for wrap=true there will almost always be a miss. The spec
does not say this; record it: the CI loop is only cheap when outputs are recorded, not
when turn-generated at PR time.

C4 — The grounding vocabulary is overloaded. `ungrounded` is both a report status and the
outcome of two different failure paths (invalid evidence, timeout), and the spec's
example shows `grounding_reason: ""` — an empty reason beside `ungrounded` reads as "no
attempt was made" even when both are legal states.
Assumption (frozen): status and reason are two fields, never collapsed. Invalid
evidence ⇒ exactly `grounding: ungrounded`, `grounding_reason: invalid-evidence`;
stage timeout ⇒ exactly `ungrounded` + `timeout`. Claims dropped by the invented-text
filter never appear in the report's claim list at all, so they cannot carry the
empty-reason confusion. Goldens pin the full status×reason matrix.

C5 — The unknown-field rule is stricter than versioning hygiene suggests.
"Unknown fields are a hard error" plus "errors name the variable, not the value" is good,
but there is no schema version field in the job spec, so a pinned `aegis` binary cannot
be upgraded beside a new pytest suite without leaking the change as mystery
unknown-field errors. Recommendation: add an optional `schema_version: 1` field now
(default 1 if omitted) so that a v2 can hard-error with a clear message instead of
"unknown field". Cheap, reversible, no behavior change, and it preserves the strictness.

C6 — `response_format: json_object` "when the endpoint supports it" is a per-provider
transport behavior, yet the spec never defines how support is detected. Nothing in
the error, retry, or provider sections defines *how* support is
detected. Assumption: default on; fail the call if the server rejects `response_format`,
with the repair-retry path unchanged — and record "not supported" into a warning, never
silently degrade by stripping it.

C7 — Token budgeting is unspecified. "8k tokens" implies a tokenizer, and the two obvious
choices (real BPE vs word/char estimate) produce different windows. Assumption:
approximate by chars/4 (close for English, cheap, deterministic, testable) and isolate it
in one function so a real tokenizer can replace it later.

C8 — "Ground each claim against provided context only" is the promise; but quote
verification searches *full files* while selection sends *windows*. Security review must
pin down and test that the reports never leak text outside what the same run printed —
yet the quote check can surface a quote from a file region the model never saw, and the
file path plus line numbers go into a report users check into git. Assumption: verified
quotes may come from anywhere in a provided file (it was provided deliberately);
no redaction, and documentation states it once.

C9 — JUnit is specified in exactly one sentence: one testsuite, one testcase per id,
failures serialize failed_assertions. JUnit XML has no enum for assertion types; the
natural mapping is `type` attribute = assertion type + message carrying detail. That is
fine, but "multiple assertion failures per testcase" needs one <failure> node per
assertion or one combined. Assumption: one <failure> per failed assertion (CI consumers
handle it fine) and timestamps only in the aggregation attributes.

C10 — The Python contract is tight by design (subprocess shim, no HTTP imports), which
makes `verify()` an obvious bottleneck: one process spawn per verification, no result
sharing between tests. The spec does not address amortization (warm daemon, batch
mode), and adds nothing about what happens when many tests run in one pytest session.
Assumption (frozen): v1 keeps one subprocess per verify() with its timeout parameter;
verify() raises with failed_assertions inlined. No persistent daemon, no batch mode,
no pre-imported binary path — amortization is a measured v2 concern, only if real
suite wall-time shows it matters.

C11 — Critique forced-refute is the most philosophically fragile of the four stages.
A model can "attempt a refute" and just say agree anyway (the spec requires forcing the
attempt, not forcing success). The verdict vocabulary is agree/disagree/mixed with no
confidence. Assumption: respect it; critique is informational unless
critique_must_agree, all three verdicts are valid, and no fabricated confidence scores
are added in v1.

C12 — Exit 2 currently overlaps load failure and cache miss, and Python can only
distinguish by exit code plus stderr text. Assumption: keep to 3 codes (spec) but make
`aegis run` print a stable `error: <kind>: <msg>` prefix on stderr for machine parsing;
Python matches on the prefix, not prose.

C13 — The spec's internal inconsistency about `wrap`: "Wrap uses temperature 0" appears
in a paragraph about default calls, but nothing tells what the *wrap* prompt template is
(the primary model sees the user's prompt directly). Assumption: wrap is a raw pass-
through of the user prompt — no system prompt, no added text, single user message — so
the output is reproducibly attributable to exactly the prompt the user wrote. Otherwise Aegis audits a prompt shaped by Aegis itself, which weakens the audit
claim.

## 3. Frozen assumptions (amendments; veto any, I re-plan only the affected phase)

- A1 — `exit 2` prints `error: <kind>: <message>` to stderr; when the job parsed and the
  failure is stage-level, a best-effort one-line `exit_reason: engine_error` JSON goes to
  stdout for tooling. Load-stage hard failures (bad YAML, missing context file, unknown
  fields) write nothing to stdout and no report is parseable.
- A2 — Pipeline orchestration is a linear sequence with explicit per-stage timeout and
  retry only for the provider transport layer (429/5xx ×3 exponential), plus the spec's
  single JSON-repair retry per structured stage. No plugin points in v1.
- A3 — All stage responses decode into typed structs with `json.Unmarshal`; helper
  validators reject unknown enum values. Strict; no salvage regexes.
- A4 — The engine reads context files into memory (all of them, `context.files` +
  `<inline>` synthetic file); window selection never re-reads disk.
- A5 — Deterministic ordering everywhere: claim ids c0..cn in extraction order after the
  merge step (merged claims keep one id), steps validated by map lookup, report arrays
  preserve insertion order.
- A6 — Provider = raw `net/http` + `encoding/json` only. No third-party SDK.
- A7 — Go deps: `gopkg.in/yaml.v3` for runtime job parsing (testdata fixtures are
  YAML). Dev-only deps: `testify/assert` in Go tests, `ruff`+`pytest` in Python;
  nothing else. No JSON-Schema library (validation is hand-written); no CLI framework
  (stdlib `flag` covers the spec's flag set exactly).
  AMENDED 2026-10-07 (Phase 1): `golang.org/x/text/unicode/norm` approved as a
  second runtime dep — stdlib has no Unicode NFKC, which the spec's match rule
  requires (§fuzzy match). Recorded here per the veto protocol.
- A8 — Context size policy: any file >1MiB or >10k lines is accepted with a warning; NOT
  rejected (spec's context side is user-owned). Window ranks on chars/4 cost.
- A9 — Fuzzy match implemented exactly as specified (NFKC + lowercase + collapse
  whitespace; requirement ⊆ claim OR token-F1 ≥ 0.8) in a dedicated pure package, with a
  committed ~40-case table spanning both directions (requirement-substring and F1) and
  the hedged-claims-still-match rule.
- A10 — Claim JSON: an array; each entry needs `text` (string ≥1 char) and `stance`
  (`asserted`|`hedged`); extra fields are ignored (LLM robustness), not unknown-field
  errors. The strict unknown-field rule applies to *jobs*, not to model output.
- A11 — Ground response format is `{results: [{claim_id, grounding, quote}]}`, one
  record per claim, in claim order; a missing `claim_id` binds the record to the next
  unassigned claim in order. `quote` is required for `supported`/`contradicted`
  (empty/missing/invalid quote ⇒ ungrounded + invalid-evidence, never a silent
  "supported") and optional for `not-in-context`.
- A12 — Reasoning steps: any step whose `depends_on` references an unknown id is dropped
  with a warning that names both the step id and the unknown id; the *other* steps keep
  going. Steps referencing claims by index c0..cn are valid iff the claim index exists
  (post-merge and post-drop list).
- A13 — Timeouts: `--stage-timeout` applies per LLM call; the whole-run timeout is
  `.Context` with `context.WithTimeout` applied to the orchestrator, and ctx cancellation
  is checked between stages (no orphan goroutine writes to cache).
- A14 — Exit-code discipline: assertion failure must include the report on stdout (the
  CI consumer wants the JSON even while failing), so failed assertions DO write the full
  report; engine errors after stage failure include the partial report on stdout
  (best-effort; a stderr note names the failing stage).

## 4. Phased implementation (7 phases)

Ordered so that the riskiest unknowns are reduced first. Every phase ends with
`go build ./...` + `go vet ./...` + `go test ./...` green and committed.

### Phase 1 — Pure foundations (highest logic risk, zero I/O) — ~2 days
STATUS: COMPLETE 2026-10-07. All four packages green: matching (23 match cases,
NFKC fold tests), job (22 cases: unknown-field rejection at every nesting level
for YAML+JSON, schema_version D1 rules, required-field errors), report (JSON
round-trip incl. ungrounded+invalid-evidence/timeout, JUnit with XML escaping),
assert (100.0% statement coverage — plan gate). Full gate: go build/vet/gofmt/
test all exit 0. Deps: yaml.v3 v3.0.1, x/text v0.16.0 (A7 amendment). Go SDK
vendored at `_tools/` (underscore keeps `./...` scans clean).
Deliverables:
- `internal/job` — spec structs + YAML/JSON loader + strict unknown-field rejection +
  full defaults. Tests incl. every example from the spec text.
- `internal/matching` — the fuzzy matcher as its own tiny pure package. Table tests from
  the spec's exact match rules.
- `internal/assert` — the assertion evaluator (must_claim, must_not_claim, grounded,
  max_ungrounded, critique_must_agree, code.parse) over a report-shaped struct.
  **100% statement coverage is required for this one package** (the spec's "100% test
  coverage" goal is scoped here), including every failed_assertions type.
- `internal/report` — schema structs + JSON writer + JUnit writer, with a checked matrix
  of the 4 grounding states × 2 reason states.

Verification gate: pure packages, table tests, goldens. No mocks yet — they cannot fail.

### Phase 2 — Provider interface + fake provider — ~1–2 days
STATUS: COMPLETE 2026-10-08. `internal/provider` green: 24 passing tests, all via
httptest.Server on loopback (zero external network). Full gate: go build/vet/gofmt/
test all exit 0 (`-race` unavailable: vendored toolchain's gcc is 32-bit, no cgo —
environment limitation, not a code defect). Deliverables:
- `provider.go` — Stage enum (wrap|claims|ground|reason|critique), Request/Response,
  structured `*Error` with Kind values matching the D2 stderr convention, `FromEnv`
  naming every missing USER_LLM_* var (never its value), `Provider` interface.
- `openai.go` — OpenAI-compatible client, raw net/http only: POST {base}/chat/completions,
  system+user messages, temperature 0, `response_format: json_object` default-on;
  any non-429 4xx triggers one full retry mode without the field + warning (C6),
  a second 4xx hard-fails. 429/5xx retried with exponential backoff (250ms doubling,
  4s cap), max 3 attempts per mode, then KindProviderTransient. ctx deadline →
  KindTimeout (never retried). API key scrubbed from all error details. 2xx with
  no/bad choices → KindBadResponse; empty content passes through (stage-level check).
- `fake.go` — FakeProvider serving `{job-id}.{stage}.json` verbatim from --fake-dir,
  never reads USER_LLM_*; missing fixture → KindFakeMissing; `DecodeGround` validates
  the ground fixture array (strict unknown-field rejection, report vocabulary, quote
  required for supported/contradicted, claim_id optional per A11 order binding).
Note: test fixture counts expect 2 requests for any 4xx (one per response_format
mode); the transient 3-attempt budget applies within each mode.
- `internal/provider` (interface, OpenAI-compatible HTTP client, retry/timeout, env
  resolution that names the missing `USER_LLM_*` var), in-process `FakeProvider` that
  loads canned JSON fixtures.
- `response_format: json_object` sent by default; per C6: if the server rejects it,
  one retry without the field + a warning recorded.

Verification gate: `httptest.Server` round-trip tests plus provider timeout/retry
tests with zero network in the default suite.

### Phase 3 — The four stages, each as a pure function over Provider — ~3 days
STATUS: COMPLETE 2026-10-08. Four stage packages + two support packages green; full
gate (build/vet/gofmt/test) exit 0, zero network (all tests via testprov stubs and
provider.Fake transcripts). Deliverables:
- `internal/stagejson` — the shared "repair this JSON" rule: one stage call, on
  JSON-shape failure exactly one repair call carrying the invalid response, then a
  hard error. Provider errors (timeout/4xx/exhaustion) pass through untouched.
- `internal/testprov` — sequential scripted provider for tests (records requests,
  supports content + error scripts); used by every stage test.
- `internal/claims` — deterministic sentence/bullet seed split; decode [{text,stance}];
  invented-claim drop via normalized-substring rule (warnings count drops); duplicate
  merge keyed on normalized text keeping asserted over hedged; ids c0..cN in
  extraction order; unknown stance coerced to asserted with warning; repair path wired.
- `internal/ground` — batches of 25 (D5); lenient live decode of
  [{claim_id,grounding,quote}] (unknown grounding value → repair path; missing quote
  is NOT an error — it flows to invalid-evidence); A11 order binding for records
  without claim_id; quote verified as exact substring over FULL files (line number
  computed); 40-line/stride-20 sliding windows ranked by token overlap, selected
  within an 8k whitespace-token budget, rendered in document order; per-batch ctx
  timeout marks only that batch ungrounded/timeout and later batches continue.
- `internal/reason` — id regex ^[A-Za-z][A-Za-z0-9_-]{0,31}$; duplicate ids and empty
  text dropped; depends_on limited to claim ids or EARLIER kept step ids (forward
  refs and cascades dropped with warnings); empty list valid; nil depends_on
  normalized to [] for clean JSON.
- `internal/critique` — forced-refute system prompt; verdict must be
  agree|disagree|mixed (foreign values take the repair path, then hard fail);
  disputed_claim_ids filtered to known claim ids with warnings.
Note: live grounding decode is deliberately more lenient than provider.DecodeGround
(fixtures fail fast on missing quotes; live responses route them throughinvalid-evidence instead) — both behaviors are pinned by tests.

- `internal/claims` — extraction prompt builder + response decode + drop-invented-claims
  (normalized substring check) + forced distinct merge (asserted > hedged).
- `internal/ground` — batched grounding with token-overlap window ranking (spec's 8k cap,
  40-line windows, stride 20) + full-file quote verification + invalid-evidence rule.
- `internal/reason` — step list validation (regex `^[A-Za-z][A-Za-z0-9_-]{0,31}$` on ids,
  depends_on = claim indexes or earlier step ids), drop-unknown with warnings.
- `internal/critique` — forced-refute prompt builder + verdict decode + disputed
  claim ids.

Verification gate: each stage gated by table tests on fake-provider transcripts, plus
the spec's named edge cases: claim drop of invented text, quote rejection, unknown-id
drop, merge of duplicate claims.

### Phase 4 — Code checkers + engine orchestration — ~2 days
STATUS: COMPLETE 2026-10-08. Full gate (build/vet/gofmt/test) exit 0; assert coverage
re-verified at 100.0% after its fix. Deliverables:
- `internal/assert` AMENDED — code.parse failures now emit UNCONDITIONALLY (spec:
  "Parse failure appends failed assertion code.parse"); the assertions.code.parse flag
  only forces the checker when nothing was auto-detected. Phase 1's Input lost the
  CodeParse field (no existing test pinned the gated behavior; new subtest pins the
  spec rule).
- `internal/cache` (key half) — SHA-256 of `v1\0prompt\0output\0normalized_spec\0`;
  normalized_spec is canonical JSON (map-keyed, sorted) with inline, sorted
  path+base64-bytes file entries, assertions object, critique_model. Table tests pin
  the spec exclusions (id, model.wrap NOT in hash) and inclusions (assertion change,
  file rename/bytes, inline, critique_model; file ORDER ignored). Store/gc = Phase 5.
- `internal/ground` — Options.CallTimeout added: each batched call gets its own
  --stage-timeout so one slow batch cannot starve the rest (D5 semantics).
- `internal/codechk` — fenced blocks (go/golang/py/python named; empty-language
  auto-detects; other languages ignored) plus 3+ consecutive non-blank line runs that
  parse (a run that fails to parse is not code — no check, no failure). Go parsed
  in-process via go/parser with package- and function-wrapper attempts; Python via
  `python3 -c "import ast..."` subprocess (5s timeout, detail capped 200 chars).
  Missing-python rule: forced mode → failing check (code.parse assertion);
  auto-detect mode → warning only. Unterminated fence handled.
- `internal/engine` — full orchestration: context resolution (missing file = exit-2
  error naming the path; inline as `<inline>`), output freeze vs wrap (model.wrap
  ignores provided output; empty wrap = exit-2 error), claims → ground → reason →
  critique ordering with per-call stage timeouts, ground timeouts marked ungrounded
  (never exit 2), code checks, assert evaluation, report assembly (OK, exit_reason
  ok|assertions_failed, warnings aggregation, cache key computed with Hit=false).
  Whole-run deadline (120s default) classified as kind run-timeout; stage timeouts on
  non-ground stages surface as provider-timeout (exit 2). Integration tests use fake
  fixtures and scripted stubs; zero network.
Note: a test-only go quirk worth remembering — Go raw strings cannot contain
backticks, so fence-bearing test jobs are built with double-quoted strings.

- `internal/codechk` — fenced code + 3-line heuristic detection, Go `go/parser`, Python
  `python3 -c` ast subprocess (handle the missing-python3 rule: record warning, or
  failure if `code.parse` is explicitly required in assertions).
- `internal/engine` — orchestration across stages, output freeze vs wrap, cache key,
  wrap/context, whole-run + per-stage timeouts, exit-code discipline (3 codes),
  `--offline` alias, stdout `-o` writer, `--junit` writer.

Verification gate: engine integration tests using fake providers for each stage; golden
reports.

### Phase 5 — Cache get/put/gc — ~1 day
- Hash (`v1\0prompt\0output\0normalized_spec\0`)
- Canonical JSON on normalized_spec (sorted keys, base64 not needed — use raw bytes; the
  spec says "+ file bytes"; record explicitly which encoding we use: raw bytes copied as
  bytes; JSON canonicalization covers the rest).
- File store per `<key>.json` with cached_at; `--cache/--no-cache/--cache-only/--record`
  semantics; `--cache-only` missing key = exit 2 with no provider contacts.
- `aegis cache gc --older-than 720h` with delete-by-mtime from `cached_at`.

Verification gate: cache-key table tests incl. the spec's exact exclusions (`id`,
`model.wrap` not in hash; context rename is).

STATUS: COMPLETE 2026-10-10. `internal/cache` store half green: `Store` (one
`<key>.json` per entry, report fields plus `cached_at` RFC3339, atomic write
temp+rename), `Get` (bad JSON / bad cached_at surfaces as entry error, missing
as os.ErrNotExist, invalid keys rejected both directions), `GC(olderThan)`
deletes by cached_at, ignores stray files/subdirs, no-op on absent dir;
12 store tests. `internal/engine` wired: `CacheMode` enum (CacheDefault
read+write = --cache/--record, CacheOff = --no-cache, CacheOnly = read-only
zero provider calls on miss + hit; miss = exit-2 kind `cache-miss`, corrupt
entry in cache-only = kind `cache-store-error`, corrupt entry in default
mode = warning + live fallback), `CacheDir` (default `.aegis/cache`, now
gitignored), lookup after context resolution but before wrap (wrap-mode key
takes the final output), hit returns the stored report as-is with only
cache.hit flipped (JSON-payload fidelity pinned by round-trip test), put
failure = warning not audit failure; assertions-in-key rule pinned by test
(tighter must_claim misses vs cache-only). Full gate exit 0, 13 ok packages;
coverage: cache 87.6%, engine 92.7%, assert 100.0%.

Cross-verification of previously reported limitations (2026-10-10):
- `go test -race` STILL UNBUILDABLE: vendored Go 1.22.5's bundled gcc is
  32-bit (`cc1.exe: sorry, unimplemented: 64-bit mode not compiled in`),
  no cgo. Unchanged from Phase 4's report; not blocking the normal-test
  gate. (Re-tried this phase per request — fixing it needs a MinGW-w64
  64-bit toolchain, out of the vendored-toolchain scope.)
- python3 skips RESOLVED: python3 (3.11.9, WindowsApps shim) is now on
  PATH, and `internal/codechk` subprocess tests run for real — 14 tests,
  -count=1, zero SKIP lines (`TestAutoDetectAcrossRuntimes` compares real
  CPython against the shim). The Phase 4 "missing-python" skip limitation
  no longer applies on this machine.
- Top-level-only note: the full-gate gofmt check now sweeps `internal/`
  (there is no cmd/ tree yet; Phase 7 introduces it).

### Phase 6 — Python library + pytest plugin — ~1–2 days
- Subprocess-only: no HTTP imports; `AEGIS_BINARY` env override; `binary="aegis"` kwarg.
- `verify()` raising assertion failures with failed_assertions inlined.
- pytest plugin: default `verify(id=None)` picks up the running pytest node id; direct
  `assert r.ok` works without the plugin.
- One example under `examples/` wired to a stub binary so the full pytest loop runs CI
  green with zero model access.

Verification gate: import-hygiene tests (a test asserting the lib has no http/requests/
openai imports — enforced by statically listing symbols loaded) + a stub-binary test +
linter (ruff).

### Phase 7 — CI plumbing, docs, and the goldens — ~1–2 days
- `cmd/aegis run|cache gc` argument parse + flag docs + `--help`.
- `.github/workflows/ci.yml`: go test, go vet, build; python pytest; one fake-provider
  end-to-end run with committed goldens.
- README quickstart; job-spec reference; CI recipes (record-on-main / cache-only-on-PR);
  docs/superpowers/specs/2026-10-06-aegis-design.md moved into `specs/`.
- A full `--fake-provider` end-to-end replay of the spec's own example job with the
  committed golden report diff-clean.

Verification gate: if a live `USER_LLM_*` key exists in this environment, one opt-in
smoke job against the live endpoint is run once locally before tagging; its transcript
goes in `testdata/manual-smoke/` (not CI).

## 5. Test and verification matrix (what "done" means)

- 100% statement coverage for internal/assert only; no global coverage bar (playing out
  the spec's hyper-clever aim at the highest-leverage surface).
- All table-driven everywhere feasible; every named edge case in the spec is a test name
  (invented-claim drop, quote rejection, unknown depends_on, duplicate merge, cache
  miss on assertion change, wrap/key exclusion, timeout→ungrounded).
- CI-golden end-to-end runs against `testdata/jobs/*.yaml` with `--fake-provider`,
  reports committed. zero network in full CI.
- Python: stub-binary tests, import hygiene, examples, ruff.

## 6. Risks register (top 5, live)

1. Schema-as-contract drift between Python dataclass and Go JSON output → mitigated by
   a single Go-generated JSON snapshot that Python dataclass tests parse in the same CI
   job (one source of truth, two consumers).
2. Provider schema drift across OpenAI-compatible vendors → `response_format` fallback
   + warning, never silent.
3. Tokenization mismatch relative to token budgeting → isolated, replaceable, tested.
4. Fake-provider fixtures replacing real failure paths (tests too happy) → every happy-
   path fixture is paired with at least one failure-path fixture (invalid JSON, missing
   keys, timeouts) in the same testdata set.
5. CI-cache footgun (changing assertion text = live run on PR) → documentation shows the
   golden rule: if you tighten an assertion on a PR job, either bump it on every change
   intentionally or accept one live run there.

## 7. Decisions (RESOLVED 2026-10-07, all approved as recommended)

- D1 — `schema_version` field: **APPROVED**. Optional `schema_version: 1` in the job
  spec, default 1 when omitted (per C5).
- D2 — Structured stderr: **APPROVED**. Exit-2 paths print `error: <kind>: <message>`
  on stderr; the Python shim matches on the prefix.
- D3 — Module path: **PLACEHOLDER CONFIRMED**. `github.com/aegis-verify/aegis` stands
  for Phase 0; swap later if a real org path is claimed (known nonzero refactor cost).
- D4 — Go YAML dep: **APPROVED**. `gopkg.in/yaml.v3` is the single runtime Go dep;
  testify/ruff/pytest remain dev-only.
- D5 — Grounding batching: **APPROVED, CHUNKED**. Batches of 25 claims per LLM call;
  a batch timeout marks only that batch `ungrounded` + `timeout`, other batches
  proceed normally. Spec's "one batched call" is read as "batched calls".

## 8. Completion record (2026-10-14)

All seven phases are implemented and committed. Full verification matrix run
locally on 2026-10-14 with the vendored toolchain (`_tools/go`, go1.22.5),
mirroring the CI `go` job:

- `gofmt -l cmd internal` clean; `go vet ./...` clean; `go build ./...` OK.
- `go test -count=1 ./...` — 14/14 packages pass; no skips on this machine
  (real CPython present for codechk subprocess tests).
- Golden gates: `TestRunGoldenE2E` diff-clean, and the regen-stability check
  (`AEGIS_REGEN_GOLDEN=1` regenerates byte-identical golden).
- `internal/assert` coverage: **100.0% of statements** (§5 target met).
- Python side: `pytest` (20 tests: shim, hygiene, Go-golden schema contract,
  stub example) + `ruff` clean — the risk-#1 “one Go snapshot, two consumers”
  mitigation is implemented in `python/tests/test_schema_contract.py`.

~~Open gate: the phase-7 opt-in live smoke (`USER_LLM_*` against a real
endpoint, transcript to `testdata/manual-smoke/`, not CI) is **blocked in this
environment — no `USER_LLM_*` key is present**. It remains the single
unexecuted gate and must run once manually before any v1 tag.~~
RESOLVED 2026-10-15: the gate was executed with a user-supplied OpenAI-
compatible key (transcript artifacts in `testdata/manual-smoke/`, not CI),
PASS — after Phase 9 fixed a real interop bug it exposed (below). The
only remaining pre-tag action is key rotation by the key owner.

Addendum 2026-10-15 — risk-#4 “tests too happy” mitigation shipped. The
committed testdata fixture set is no longer happy-path-only; it now pairs
`explain-cache-key` with two failure companions, each replaying through the
real CLI in `TestRunFailureFixturesE2E` (zero network):

- `explain-cache-key-failure` (exit 1): ground fixture marks the must claim
  `not-in-context`, so `grounded` + `max_ungrounded` fail; test pins
  `ok=false`, `exit_reason="assertions_failed"`, the exact two
  `failed_assertions` entries, and claim `c0` grounding/evidence shape.
- `explain-cache-key-badjson` (exit 2): invalid-JSON claims fixture burns
  the repair retry and hard-fails; test pins stdout empty and the kind-first
  line `error: provider-bad-response: stage claims: ...`.

The exit-2 pair exposed a real D2-convention bug now fixed: the stagejson
repair-exhausted error escaped as a kindless plain error; it is now a
structured `provider.Error` with kind `provider-bad-response`, matching all
other stage failures. Re-verified after the fix: gofmt/vet clean,
`go test -count=1 ./...` 15/15 packages, golden diff-clean and regen
byte-stable, `go build ./...` OK; Python side unchanged (20 pytest, ruff
clean). Timeout/4xx and 5xx failure fixtures remain live-phase only (the
fake provider cannot simulate transport failures); their code paths are
already covered by provider and ground unit tests.

Addendum 2 (2026-10-15) — Phase 8, A1/A14 exit-2 stdout contract shipped.
All seven planned phases were complete; this closed the last frozen-
assumption gap found in the code: the report vocabulary contained
`exit_reason: "engine_error"` (pinned by the Python schema-contract test)
but the Go binary never wrote anything to stdout on exit 2.

- `internal/engine.Run` now classifies failures: RESOLVED-class errors
  (after context resolution — cache-only miss/store error, wrap errors,
  any stage-call failure, run timeout) return the partial report next to
  the error; LOAD-class errors (missing/unreadable context file) return
  nil. All other load failures (bad args, unreadable/invalid job)
  already never reached the engine.
- `cmd/aegis`: on an engine error with a partial report, stdout gets the
  best-effort one-line JSON (copy of the report with `ok=false`,
  `exit_reason="engine_error"`, replay cache info zeroed); load-stage
  failures still leave stdout empty — both branches now pinned by tests
  (committed badjson fixture replays: engine_error line + kind-first
  stderr; cache-only miss: engine_error line; bad yaml: stdout empty).
- README documents the split (`Exit-2 stdout (A1 contract)` section).
- Re-verified: gofmt/vet clean, `go test -count=1 ./...` all packages,
  golden diff-clean + regen byte-stable, gofmt-scoped build OK, Python
  side unchanged (pytest 20, ruff clean).

Addendum 3 (2026-10-15) — Phase 9, live-smoke interop fix. Executing the
long-blocked live gate surfaced a real bug the fake provider could never
show: under `response_format: json_object` (C6: default on), compliant
endpoints enforce a top-level JSON OBJECT and bend the array-topped stage
reply schemas (`claims`, `ground`, `reason` asked for bare arrays) into
single objects, deterministically failing both the original call and the
repair retry for two unrelated model families. Fix: stage replies are now
object-topped (claims `{"claims": [...]}`, ground `{"results": [...]}`
— the shape A11 already specified — reason `{"steps": [...]}`), and a
shared `stagejson.DecodeArray` accepts the wrapper OR a bare array, so
the committed fixtures and the golden keep decoding unchanged; a bare
single-record object is still an error (no salvage). New wrapper/repair
tests in claims/ground/reason plus decoder table tests in stagejson.
Matrix re-verified green (gofmt/vet/build/test -count=1, golden diff-clean,
regen byte-stable); Python side untouched. Retry against the real endpoint
now passes: exit 0, claim grounded with valid evidence, PASS recorded in
`testdata/manual-smoke/NOTES.md`.

Original D1–D5 question text is preserved in the session record; the resolved
rulings above are the operative contract for implementation.