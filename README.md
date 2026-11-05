# Aegis

Deterministic CI verification and explainability audit engine for LLM outputs.

[![Go Report Card](https://goreportcard.com/badge/github.com/saranraj1/AEGIS)](https://goreportcard.com/report/github.com/saranraj1/AEGIS)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Aegis evaluates language model completions against formal, declarative assertions. Rather than relying on non-deterministic scoring prompts or heuristic benchmarks, Aegis parses model completions through a four-stage audit pipeline, generates structured evidence citations, writes JUnit XML reports for CI test suites, and exits with standard Unix status codes.

---

## Table of Contents

- [Core Audit Pipeline](#core-audit-pipeline)
- [Installation](#installation)
- [Quickstart](#quickstart)
- [Execution Output](#execution-output)
- [Job Specification](#job-specification)
- [Deterministic Replay Cache](#deterministic-replay-cache)
- [Exit Code Contract](#exit-code-contract)
- [Python SDK and Pytest Plugin](#python-sdk-and-pytest-plugin)
- [CLI Reference](#cli-reference)
- [Development and Testing](#development-and-testing)
- [Repository Structure](#repository-structure)
- [License](#license)

---

## Core Audit Pipeline

Every Aegis verification job executes four sequential stages. Intermediate artifacts from each stage are preserved in the final report:

```text
  [ Job Spec: prompt, output, context, assertions ]
                         |
                         v
   Stage 1: Claims Extraction (Atomic factual propositions)
                         |
                         v
   Stage 2: Context Grounding (Supported / Contradicted / Ungrounded)
                         |
                         v
   Stage 3: Reasoning Graph (Directed dependency steps)
                         |
                         v
   Stage 4: Adversarial Critique (Forced-refute validation)
                         |
                         v
  [ Report JSON + JUnit XML + Exit Code (0, 1, or 2) ]
```

1. **Claims Extraction:** Extracts atomic factual assertions from model output, tagging stance (asserted or hedged).
2. **Context Grounding:** Evaluates each claim against provided reference context files, attributing citations (file path, exact quote, line number) and status (`supported`, `contradicted`, `not-in-context`, or `ungrounded`).
3. **Reasoning Graph:** Reconstructs the logical argument steps, establishing explicit dependencies between claims and intermediate deductions.
4. **Adversarial Critique:** Prompts a referee stage to challenge unstated assumptions, spot unsupported leaps, and flag hallucinations.

---

## Installation

### Pre-compiled Binaries

Download compiled binaries for your operating system and architecture from the [GitHub Releases](https://github.com/saranraj1/AEGIS/releases) page:

- **Linux:** `aegis_linux_amd64.tar.gz`, `aegis_linux_arm64.tar.gz`
- **macOS:** `aegis_darwin_amd64.tar.gz` (Intel), `aegis_darwin_arm64.tar.gz` (Apple Silicon)
- **Windows:** `aegis_windows_amd64.zip`

Extract the binary and place it in your system `PATH`:

```bash
# Example for Linux/macOS
tar -xzf aegis_*_linux_amd64.tar.gz
sudo mv aegis /usr/local/bin/
```

### Install via Go

If you have Go 1.22 or newer installed:

```bash
go install github.com/saranraj1/AEGIS/cmd/aegis@latest
```

### Build from Source

```bash
git clone https://github.com/saranraj1/AEGIS.git
cd AEGIS
go build -o aegis ./cmd/aegis
```

### Python Package

The Python SDK and pytest integration can be installed directly from source:

```bash
pip install -e python
```

---

## Quickstart

### 1. Configure Provider Credentials

Aegis connects to any OpenAI-compatible API endpoint:

```bash
export USER_LLM_API_KEY="your-api-key"
export USER_LLM_BASE_URL="https://api.openai.com/v1"
export USER_LLM_MODEL="gpt-4o"
```

### 2. Define a Verification Job

Create a job definition file in YAML or JSON format (for example, `job.yaml`):

```yaml
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
```

### 3. Run the Verification

Execute the verification run and record the output in the replay cache:

```bash
aegis run job.yaml --record
```

### 4. Replay in CI Without Credentials

Replay the cached run offline on Pull Requests with zero API calls, zero latency, and zero token expense:

```bash
aegis run job.yaml --offline
```

---

## Execution Output

When running a verification job, Aegis prints the full audit report as JSON to standard output (or writes it to a file via `-o/--output`).

### Invocation Command

```bash
aegis run testdata/jobs/explain-cache-key.yaml --fake-provider --fake-dir testdata/fixtures
```

### Verbatim CLI Output

```json
{
  "id": "explain-cache-key",
  "ok": true,
  "exit_reason": "ok",
  "prompt": "How does aegis hash a cache key?\n",
  "output": "The cache key is sha256 of prompt, output, and spec.\n",
  "claims": [
    {
      "id": "c0",
      "text": "The cache key is sha256 of prompt, output, and spec.",
      "stance": "asserted",
      "grounding": "supported",
      "grounding_reason": "",
      "evidence": [
        {
          "path": "<inline>",
          "quote": "cache key = sha256(prompt + output + spec)",
          "line": 1
        }
      ]
    }
  ],
  "steps": [
    {
      "id": "s1",
      "text": "Hash covers prompt, output, and normalized spec.",
      "depends_on": [
        "c0"
      ]
    }
  ],
  "critique": {
    "verdict": "agree",
    "notes": "evidence quote matches the inline spec.",
    "disputed_claim_ids": []
  },
  "code_checks": [],
  "failed_assertions": null,
  "warnings": [],
  "cache": {
    "hit": false,
    "key": "b8d09e0931af1def4fb412403e06e530ca738693f8c1f8f885a0ab5d9f70d2d9"
  }
}
```

---

## Job Specification

Jobs can be supplied as YAML, JSON, or standard input (`aegis run -`).

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | string | yes | Unique identifier used for JUnit testcase names and cache fixture keys. |
| `prompt` | string | yes | Prompt provided to the model. Required when `output` is omitted. |
| `output` | string | no | Static model output. If omitted, Primary model is called once to generate text. |
| `context.files` | list of strings | no | File paths to load into grounding context. Missing files trigger exit code 2. |
| `context.inline` | string | no | Literal text included as synthetic file `<inline>` in grounding context. |
| `assertions.must_claim` | list of strings | no | Claims that must appear in the output. Matched via NFKC normalization and token F1 >= 0.8. |
| `assertions.must_not_claim` | list of strings | no | Claims that must not appear in the output. |
| `assertions.grounded` | boolean | no | When true, every matched `must_claim` must have `supported` grounding status. Default: false. |
| `assertions.max_ungrounded` | integer | no | Maximum allowed claims with `ungrounded`, `not-in-context`, or `contradicted` status. |
| `assertions.critique_must_agree` | boolean | no | When true, an adversarial critique verdict of `disagree` fails the run. Default: false. |
| `assertions.code.parse` | boolean | no | Validates syntax of Python and Go code snippets extracted from output. Default: false. |
| `model.wrap` | boolean | no | When true, forces live model execution even if `output` is supplied. Default: false. |
| `model.critique_model` | string | no | Custom model identifier override for the critique stage. |
| `schema_version` | integer | no | Specification version. Currently must be 1. Default: 1. |

---

## Deterministic Replay Cache

Aegis features a content-addressed replay cache designed for cost-effective CI workflows.

### Cache Key Composition

The cache key is a SHA-256 hash calculated over:
- Model prompt text.
- Model completion output.
- Normalized job specification (context paths, inline context, assertion parameters, model identifiers).

Because assertion parameters are baked into the hash key, tightening or adding a `must_claim` invalidates the cache and requires a fresh live execution. Replaying old reports against newly altered assertions is disallowed by design.

### CI Workflow Pattern

Recommended CI architecture:

1. **Main Branch (Merge Workflow):** Run with `--record` to refresh the replay cache when features or prompts change:
   ```bash
   aegis run tests/jobs/query-accuracy.yaml --record
   ```
2. **Pull Requests (Gate Workflow):** Run with `--offline` (`--cache-only`) to enforce zero-flake, zero-cost regression testing:
   ```bash
   aegis run tests/jobs/query-accuracy.yaml --offline
   ```

### Cache Maintenance

Purge expired cache entries using the cache garbage collection command:

```bash
aegis cache gc --older-than 720h --cache-dir .aegis/cache
```

---

## Exit Code Contract

Aegis communicates results through standard Unix exit codes:

- **Exit 0 (Success):** All assertions passed. Report JSON contains complete stage data and citations.
- **Exit 1 (Assertion Failure):** One or more declarative assertions failed. The report JSON lists specific violations under `failed_assertions`.
- **Exit 2 (Engine Error):** Execution could not proceed due to invalid inputs, missing context files, provider API timeouts, or cache misses in `--offline` mode.

### Error Protocol

On exit code 2, standard error outputs a single formatted line:

```text
error: <kind>: <message>
```

When an error occurs after job parsing has succeeded (such as provider timeouts or cache misses), Aegis prints a machine-readable JSON object to standard output containing `"ok": false` and `"exit_reason": "engine_error"` to allow downstream orchestration tools to pinpoint the failed job.

---

## Python SDK and Pytest Plugin

The `aegis-verify` Python package executes the Aegis binary as an external validation engine. It imports no HTTP libraries and requires no model API tokens in test environments.

### Installation

```bash
pip install -e python
```

### Pytest Integration

```python
import aegis

def test_cache_key_explanation():
    report = aegis.verify(
        prompt="How does aegis hash a cache key?",
        output="The cache key is sha256 of prompt, output, and spec.",
        context_files=["internal/cache/key.go"],
        must_claim=["cache key is sha256 of prompt, output, and spec"],
        grounded=True,
        raise_on_fail=True,
    )
    assert report.ok
```

When running under `pytest`, the Aegis plugin automatically binds `job.id` to the active test node ID.

---

## CLI Reference

```text
Usage:
  aegis run <job.yaml|job.json|-> [flags]
  aegis cache gc --older-than <duration>
  aegis version

Run Flags:
  -o, --output PATH      Write report JSON to PATH (default: stdout)
  --junit PATH           Write JUnit XML test suite to PATH
  --cache-dir DIR        Replay cache directory (default: .aegis/cache)
  --cache                Read and write cache (default: enabled)
  --record               Alias for --cache; refreshes cache entries
  --no-cache             Disable cache reading and writing
  --cache-only           Replay mode only; missing keys exit with status 2
  --offline              Alias for --cache-only
  --timeout DUR          Total run timeout (default: 120s)
  --stage-timeout DUR    Per-stage LLM call timeout (default: 45s)
  --fake-provider        Serve canned stage responses from disk
  --fake-dir DIR         Directory containing canned stage responses
  --                     Read job configuration from stdin

Cache GC Flags:
  --older-than DUR       Delete cache records older than duration (e.g., 720h)
  --cache-dir DIR        Replay cache directory (default: .aegis/cache)
```

---

## Development and Testing

### Run All Unit and Integration Tests

```bash
go test ./...
```

### Run Golden End-to-End Test

```bash
go test -run TestRunGoldenE2E ./cmd/aegis/
```

### Regenerate Golden Test Reports

```bash
AEGIS_REGEN_GOLDEN=1 go test -run TestRunGoldenRegen ./cmd/aegis/
```

### Run Python Test Suite and Linters

```bash
pytest python/tests examples/pytest-example -q
ruff check python examples
```

---

## Repository Structure

```text
cmd/aegis/               CLI entry point and command parsing
internal/
  assert/                Declarative assertion evaluation engine
  cache/                 Content-addressed SHA-256 replay cache
  claims/                Atomic claim extraction and stance analysis
  codechk/               Go and Python AST code parse checkers
  critique/              Adversarial referee and refute engine
  engine/                Four-stage pipeline coordinator
  ground/                Context grounding and citation matcher
  job/                   Job specification YAML/JSON parser
  matching/              Fuzzy string matching and token F1 scoring
  provider/              OpenAI-compatible HTTP provider client
  reason/                Logical reasoning step reconstruction
  report/                Report data structures and serialization
  stagejson/             Stage response JSON extraction helpers
  testprov/              Mock provider harness for testing
python/
  aegis/                 Python client SDK and pytest plugin
  tests/                 Schema contract and hygiene tests
examples/
  pytest-example/        Example test suite demonstrating stub-binary CI loop
testdata/
  jobs/                  Example verification job definitions
  fixtures/              Canned stage responses for deterministic tests
  reports/               Golden reference report documents
```

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
