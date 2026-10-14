# aegis (Python shim)

Thin subprocess-only wrapper for the `aegis` verification CLI. The Python
side never calls an LLM and imports no HTTP client: it writes a job
file, runs the binary (`aegis` on `PATH`, or `AEGIS_BINARY`), parses the
JSON report, and raises assertion failures with the disagreeing claims
inlined.

```python
import aegis

def test_cache_key_explanation():
    r = aegis.verify(
        prompt="How does aegis hash a cache key?",
        output=model_text,
        context_files=["internal/cache/key.go"],
        must_claim=["cache key is sha256 of prompt, output, and spec"],
        grounded=True,
    )
    assert r.ok
```

Default `id` is the running pytest node id under the plugin, else
`"job"`. `assert r.ok` alone is enough; the plugin (installed via
`pip install "aegis-verify[pytest]"`) rewrites failures to show
`failed_assertions`.

## CLI failure convention

Exit-2 paths print `error: <kind>: <message>` on stderr; `verify()`
matches on the prefix and raises `AegisEngineError(name.kind, stderr)`.
