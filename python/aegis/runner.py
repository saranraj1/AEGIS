"""Subprocess runner: invokes the aegis binary, never imports an HTTP client."""

from __future__ import annotations

import os
import shutil
import subprocess  # noqa: S404 - by design, the shim runs the CLI
from pathlib import Path

from .report import Report

__all__ = ["AegisEngineError", "run_binary"]


class AegisEngineError(RuntimeError):
    """Exit-2 from the engine: bad job, provider failure, cache miss, ..."""

    def __init__(self, kind: str, message: str, stderr: str):
        super().__init__(f"error: {kind}: {message}")
        self.kind = kind
        self.stderr = stderr


def resolve_binary(explicit: str | None = None) -> str:
    """binary= kwarg, else AEGIS_BINARY, else 'aegis' on PATH."""
    if explicit:
        if shutil.which(explicit) is None and not Path(explicit).exists():
            raise FileNotFoundError(f"aegis binary not found: {explicit}")
        return explicit
    env = os.environ.get("AEGIS_BINARY")
    if env:
        if shutil.which(env) is None and not Path(env).exists():
            raise FileNotFoundError(f"AEGIS_BINARY not found: {env}")
        return env
    found = shutil.which("aegis")
    if not found:
        raise FileNotFoundError(
            "aegis binary not found on PATH and AEGIS_BINARY is unset; "
            "build it with `go build -o aegis ./cmd/aegis`"
        )
    return found


def run_binary(
    binary: str,
    job_path: Path,
    cache_dir: str | None = None,
    cache_only: bool = False,
    no_cache: bool = False,
    timeout: float | None = None,
    fake_provider: bool = False,
    fake_dir: str | None = None,
) -> Report | None:
    """Run the CLI and return the parsed Report.

    - exit 0 → Report(ok=True)
    - exit 1 → Report(ok=False) (assertion failures; caller decides)
    - exit 2 → AegisEngineError with the D2 `error: <kind>: <message>` line
    - anything else → AegisEngineError(kind="unknown", ...)
    """
    cmd = [binary, "run", str(job_path)]
    if cache_dir:
        cmd += ["--cache-dir", str(cache_dir)]
    if cache_only:
        cmd.append("--cache-only")
    if no_cache:
        cmd.append("--no-cache")
    if fake_provider:
        cmd.append("--fake-provider")
        if fake_dir:
            cmd += ["--fake-dir", str(fake_dir)]
    if timeout and timeout > 0:
        cmd += ["--timeout", f"{int(timeout * 1_000_000_000)}ns"]

    proc_timeout = (timeout + 5.0) if (timeout and timeout > 0) else None
    try:
        proc = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=proc_timeout,
            check=False,
            shell=False,
        )
    except subprocess.TimeoutExpired as e:
        raise AegisEngineError(
            "run-timeout",
            f"process execution timed out after {proc_timeout}s",
            e.stderr or "",
        ) from e
    if proc.returncode in (0, 1):
        try:
            return Report.from_json(proc.stdout)
        except ValueError as e:
            # Empty / non-JSON report with exit 0/1 is a shim contract
            # violation: the engine prints the full report on stdout.
            raise AegisEngineError(
                "report-parse",
                f"exit {proc.returncode} but stdout is not a report ({e}); "
                f"stderr={proc.stderr.strip()[:200]}",
                proc.stderr,
            ) from e
    if proc.returncode == 2:
        # D2: exit-2 paths print exactly one "error: <kind>: <message>"
        # line; regex-free prefix match keeps the shim dependency-free.
        parts = proc.stderr.strip().split(":", 2)
        kind = parts[1].strip() if len(parts) >= 3 else "unknown"
        msg = parts[2].strip() if len(parts) >= 3 else proc.stderr.strip()
        raise AegisEngineError(kind, msg, proc.stderr)
    raise AegisEngineError(
        "unknown", f"aegis exited with unexpected code {proc.returncode}", proc.stderr
    )
