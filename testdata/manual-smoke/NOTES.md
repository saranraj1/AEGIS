# Live smoke transcript — 2026-10-15

The plan's phase-7 opt-in live gate (§Phase 7 verification), executed against
a real OpenAI-compatible endpoint. Not a CI artifact — committed for the
completion record, never run in CI.

- Endpoint: `https://api.groq.com/openai/v1` (OpenAI-compatible), key via
  `USER_LLM_*` env (kept in a gitignored local file, never committed, never
  logged — the protocol scrubs key material from reports/errors).
- Job: the spec's own example, `testdata/jobs/explain-cache-key.yaml`.
- Command: `aegis run <job> --no-cache --timeout 60s --stage-timeout 30s
  -o report.json --junit junit.xml`.
- Final model: `qwen/qwen3.8-27b` (chosen from the endpoint's model list;
  `llama-3.1-8b-instant` no longer exists on this account).

## Result: PASS

`exit 0`, `ok=true`, `exit_reason="ok"`; claim `c0` grounded `supported`
with line-1 evidence; critique `agree`; zero warnings; deterministic cache
key `b8d09e...` present. Artifacts: `report.json`, `junit.xml`
(`stdout.txt`/`stderr.txt` are empty on a clean exit-0 run).

## What the gate caught: a real interop bug (fixed in Phase 9)

1. Pre-fix runs failed at the claims stage with
   `provider-bad-response: stage claims: response still not valid JSON
   after one repair retry` for BOTH `openai/gpt-oss-120b` and
   `qwen/qwen3.8-27b`, each on a fresh 404-free account key.
2. Minimal-curl probes reproduced it: with
   `response_format: {"type":"json_object"}`, compliant
   endpoints/models enforce a **top-level JSON object** and bend the
   requested "array of objects" reply into a single object (`{"text": ...,
   "stance": ...}`) — `json: cannot unmarshal object into Go value of type
   []claims.Raw`. Exact-wording probes alone were not sufficient to show
   it (a trivial probe still returned a bare array); only the realistic
   claims prompt reproduced the shape.
3. Root cause: array-topped stage reply schemas contradict the
   `json_object` envelope we send by default (C6). Not a model quirk —
   deterministic across two unrelated model families.
4. Fix (Phase 9): stage replies are now object-topped —
   `{"claims": [...]}` (claims), `{"results": [...]}` (ground, which A11
   already specified), `{"steps": [...]}` (reason) — with
   `stagejson.DecodeArray` accepting the wrapper OR a bare array (fake
   fixtures and existing transcripts keep working, and the committed
   golden trivially stays byte-stable). A bare single-record object
   remains an error (no salvage), flowing into the repair retry.

Consequence: aegis is now provably interoperable with a real
json_object-enforcing vendor, not just the in-process fake provider.
