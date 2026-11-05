"""Job writer: kwargs of verify() → the aegis job schema on disk."""

from __future__ import annotations

import json
import tempfile
from pathlib import Path
from typing import Any

__all__ = ["write_job_file"]


def build_job(
    *,
    id_: str | None,
    prompt: str,
    output: str | None,
    context_files: list | None,
    inline: str | None,
    must_claim: list | None,
    must_not_claim: list | None,
    grounded: bool | None,
    max_ungrounded: int | None,
    critique_must_agree: bool | None,
    code_parse: bool | None,
    wrap: bool | None,
    critique_model: str | None,
    schema_version: int,
) -> dict[str, Any]:
    """Build the job dict matching the Go engine's schema exactly.

    ``_``-suffixed parameter names avoid clashing with Python keywords
    and keep the signature stable for callers using eval-style kwargs.
    """
    j: dict[str, Any] = {"id": id_ or "job", "prompt": prompt, "schema_version": schema_version}
    if output is not None:
        j["output"] = output
    if context_files or inline:
        ctx: dict[str, Any] = {}
        if context_files:
            ctx["files"] = [
                Path(f).as_posix() if isinstance(f, (str, Path)) else str(f)
                for f in context_files
            ]
        if inline:
            ctx["inline"] = inline
        j["context"] = ctx
    a: dict[str, Any] = {}
    if must_claim:
        a["must_claim"] = list(must_claim)
    if must_not_claim:
        a["must_not_claim"] = list(must_not_claim)
    if grounded is not None:
        a["grounded"] = bool(grounded)
    if max_ungrounded is not None:
        a["max_ungrounded"] = int(max_ungrounded)
    if critique_must_agree is not None:
        a["critique_must_agree"] = bool(critique_must_agree)
    if code_parse is not None:
        a["code"] = {"parse": bool(code_parse)}
    if a:
        j["assertions"] = a
    if wrap is not None or critique_model is not None:
        m: dict[str, Any] = {}
        if wrap is not None:
            m["wrap"] = bool(wrap)
        if critique_model is not None:
            m["critique_model"] = critique_model
        j["model"] = m
    return j


def write_job_file(job: dict[str, Any], suffix: str = ".yaml") -> Path:
    """Write the job to a temp file the binary reads; return its path."""
    # NamedTemporaryFile must be closed before the Go process reads it,
    # so delete=False + with-block close is the cross-platform pattern;
    # the payloads stay 0600 by umask and are unlinked by verify().
    with tempfile.NamedTemporaryFile(
        mode="w", suffix=suffix or ".yaml", prefix="aegis-job-", encoding="utf-8", delete=False
    ) as fd:
        json.dump(job, fd, ensure_ascii=False, indent=1)
    # Closed on exiting the with-block; Windows reads it fine after that.
    return Path(fd.name)
