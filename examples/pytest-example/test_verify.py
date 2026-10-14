"""Example tests: the full aegis pytest loop with zero model access.

Run from the repo root:

    pytest examples/pytest-example -q

or from this directory:

    pytest -q
"""

from __future__ import annotations

import aegis
import pytest


def test_verify_ok_returns_report(aegis_stub):
    """Plain usage: verify() returns the parsed Report; assert on it."""
    r = aegis.verify(prompt="p", output="o", must_claim=["o"])
    assert r.ok is True
    assert r.id == "example-job"  # the stub echoes its canned report
    assert r.claims[0].grounding == "supported"


@pytest.mark.aegis_fail
def test_raise_on_fail_inlines_assertions(aegis_stub):
    """raise_on_fail=True asserts for you, inlining failures + claims."""
    with pytest.raises(AssertionError) as ei:
        aegis.verify(prompt="p", output="unrelated text",
                     must_claim=["required claim"], raise_on_fail=True)
    msg = str(ei.value)
    assert "must_claim" in msg
    assert "unrelated text" in msg  # disagreeing claim inlined


@pytest.mark.aegis_fail
def test_plain_assert_still_reports_safely(aegis_stub):
    """Without raise_on_fail, a plain `assert r.ok` fails (with a
    plugin-appended hint in the *result report*, not the exception) and
    the caller can still inspect the failure details on the Report."""
    r = aegis.verify(prompt="p", output="unrelated text")
    assert not r.ok
    assert r.failed_assertions[0].type == "must_claim"
    assert r.claims[0].text == "unrelated text"
