"""Public API: aegis.verify() and engine-error plumbing."""

from __future__ import annotations

import contextvars

from .job import build_job, write_job_file
from .report import Report
from .runner import AegisEngineError, resolve_binary, run_binary

__all__ = ["AegisEngineError", "Report", "current_node_id", "verify"]

# Node-id context so the pytest plugin can inject the running test's id
# into verify() calls that omit id=. Kept out of the hot signature; a
# plain `assert r.ok` works identically without the plugin.
_node_id: contextvars.ContextVar[str | None] = contextvars.ContextVar("aegis_node_id", default=None)


def current_node_id(value: str | None = None) -> str | None:
    """Get (or, with a value, set) the active pytest node id."""
    if value is not None:
        _node_id.set(value)
    return _node_id.get()


def verify(
    *,
    prompt: str,
    output: str | None = None,
    id: str | None = None,
    context_files: list | None = None,
    inline: str | None = None,
    must_claim: list | None = None,
    must_not_claim: list | None = None,
    grounded: bool | None = None,
    max_ungrounded: int | None = None,
    critique_must_agree: bool | None = None,
    code_parse: bool | None = None,
    wrap: bool | None = None,
    critique_model: str | None = None,
    schema_version: int = 1,
    binary: str | None = None,
    cache_dir: str | None = None,
    cache_only: bool = False,
    no_cache: bool = False,
    timeout: float | None = None,
    fake_provider: bool = False,
    fake_dir: str | None = None,
    raise_on_fail: bool = False,
) -> Report:
    """Run one aegis job through the binary and return the parsed report.

    kwargs map 1:1 onto the job schema except underscored variants that
    collide with Python keywords (`id` is a builtin, kept verbatim for
    API parity with the YAML field).

    By default returns the Report; callers assert `r.ok` themselves.
    With the pytest plugin and `raise_on_fail=True` the failure is
    raised inline with `failed_assertions` and the disagreeing claims.
    """
    j = build_job(
        id_=id or current_node_id() or "job",
        prompt=prompt,
        output=output,
        context_files=context_files,
        inline=inline,
        must_claim=must_claim,
        must_not_claim=must_not_claim,
        grounded=grounded,
        max_ungrounded=max_ungrounded,
        critique_must_agree=critique_must_agree,
        code_parse=code_parse,
        wrap=wrap,
        critique_model=critique_model,
        schema_version=schema_version,
    )
    job_path = write_job_file(j, suffix=".yaml" if not fake_provider else ".json")
    try:
        rep = run_binary(
            resolve_binary(binary),
            job_path,
            cache_dir=cache_dir,
            cache_only=cache_only,
            no_cache=no_cache,
            timeout=timeout,
            fake_provider=fake_provider,
            fake_dir=fake_dir,
        )
    finally:
        job_path.unlink(missing_ok=True)

    assert rep is not None
    if raise_on_fail and not rep.ok:
        raise AssertionError(
            f"aegis assertions failed ({len(rep.failed_assertions)}):\n{rep.human_failures()}"
        )
    return rep
