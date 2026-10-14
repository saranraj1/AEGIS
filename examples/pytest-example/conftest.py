"""Example: driving `aegis.verify()` from pytest with a stub binary.

The `python/aegis` package is not assumed to be pip-installed, so this
conftest prepends its parent to sys.path when importing `aegis` fails
(`pip install -e "python[dev]"` environments skip this entirely).

The `aegis_stub` fixture points AEGIS_BINARY at a tiny fake `aegis`
executable — no model, no subprocess chain. Add the `aegis_fail`
marker to a test to get a report whose assertions fail.
"""

from __future__ import annotations

import os
import stat
import sys
from pathlib import Path

import pytest

_here = Path(__file__).resolve()
_repo_root = _here.parents[2]
try:
    import aegis  # noqa: F401
except ModuleNotFoundError:  # not installed: use the sibling package
    sys.path.insert(0, str(_repo_root / "python"))

import aegis  # noqa: E402

STUB_REPORT_OK = {
    "id": "example-job",
    "ok": True,
    "exit_reason": "ok",
    "prompt": "p",
    "output": "o",
    "claims": [
        {"id": "c0", "text": "o", "stance": "asserted",
         "grounding": "supported", "grounding_reason": "", "evidence": []}
    ],
    "steps": [],
    "critique": {"verdict": "agree", "notes": "", "disputed_claim_ids": []},
    "code_checks": [],
    "failed_assertions": [],
    "warnings": [],
    "cache": {"hit": False, "key": "0" * 64},
}

STUB_REPORT_FAIL = {
    "id": "example-job",
    "ok": False,
    "exit_reason": "assertions_failed",
    "prompt": "p",
    "output": "unrelated text",
    "claims": [
        {"id": "c0", "text": "unrelated text", "stance": "asserted",
         "grounding": "contradicted", "grounding_reason": "", "evidence": []}
    ],
    "steps": [],
    "critique": {"verdict": "disagree", "notes": "",
                 "disputed_claim_ids": ["c0"]},
    "code_checks": [],
    "failed_assertions": [
        {"type": "must_claim", "expected": "required claim",
         "actual": "", "detail": ""},
        {"type": "grounded", "expected": "supported",
         "actual": "contradicted", "detail": ""},
    ],
    "warnings": [],
    "cache": {"hit": False, "key": "0" * 64},
}


def _make_stub(bin_dir: Path, report: dict) -> str:
    """Write a fake `aegis` binary that prints `report` on stdout."""
    payload = bin_dir / "aegis-payload.py"
    # Report embedded as a repr() literal: json.dumps would emit
    # true/false, valid JSON but invalid Python.
    payload.write_text(
        "import json, sys\n"
        f"report = {report!r}\n"
        "print(json.dumps(report))\n",
        encoding="utf-8",
    )
    if sys.platform == "win32":
        launcher = bin_dir / "aegis.cmd"
        launcher.write_text(
            f'@echo off\n"{sys.executable}" "{payload}" %*\n',
            encoding="utf-8",
        )
    else:
        launcher = bin_dir / "aegis"
        launcher.write_text(
            f"#!/bin/sh\nexec {sys.executable} {payload} \"$@\"\n",
            encoding="utf-8",
        )
    st = os.stat(launcher)
    os.chmod(launcher, st.st_mode | stat.S_IEXEC)
    return str(launcher)


def pytest_configure(config):
    config.addinivalue_line(
        "markers", "aegis_fail: use the failing stub report for this test"
    )


@pytest.fixture
def aegis_stub(tmp_path_factory, monkeypatch, request):
    """Point AEGIS_BINARY at a stub; `aegis_fail` selects the bad report."""
    failing = request.node.get_closest_marker("aegis_fail") is not None
    report = STUB_REPORT_FAIL if failing else STUB_REPORT_OK
    bin_dir = tmp_path_factory.mktemp("stub-bin")
    monkeypatch.setenv("AEGIS_BINARY", _make_stub(bin_dir, report))
    return aegis


__all__ = ["aegis_stub", "STUB_REPORT_OK"]
