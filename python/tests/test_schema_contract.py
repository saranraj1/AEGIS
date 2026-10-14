"""Schema contract (plan risk 1): one Go-generated snapshot, two consumers.

The committed golden report `testdata/reports/explain-cache-key.golden
json` is produced by the Go engine (TestRunGoldenE2E, diff-clean in CI).
These tests parse that exact artifact through python/aegis/report.py
and pin the cross-language JSON contract: field sets, enum vocabularies,
null-vs-omitted handling, and referential integrity of step deps.

If Go changes the report shape, these fail in the same python job the
same session the golden regenerates — contract drift is caught
immediately, not at integration time.
"""

from __future__ import annotations

import json
from pathlib import Path

import aegis

GOLDEN = Path(__file__).resolve().parents[2] / "testdata" / "reports" / "explain-cache-key.golden.json"

CLAIM_KEYS = {"id", "text", "stance", "grounding", "grounding_reason", "evidence"}
EVIDENCE_KEYS = {"path", "quote", "line"}
GROUNDINGS = {"supported", "contradicted", "not-in-context", "ungrounded"}
STANCES = {"asserted", "hedged"}  # invented claims are dropped before the report
EXIT_REASONS = {"ok", "assertions_failed", "engine_error"}
TOP_KEYS = {
    "id", "ok", "exit_reason", "prompt", "output", "claims", "steps",
    "critique", "code_checks", "failed_assertions", "warnings", "cache",
}


def test_golden_exists():
    assert GOLDEN.is_file(), (
        "testdata/reports/explain-cache-key.golden.json is missing: the "
        "Go golden e2e has never run in this checkout"
    )


def test_report_round_claims_from_go_json():
    rep = aegis.Report.from_json(GOLDEN.read_text(encoding="utf-8"))
    assert rep.id == "explain-cache-key"
    assert rep.ok is True
    assert rep.exit_reason == "ok"
    assert [c.grounding for c in rep.claims] == ["supported"]
    c0 = rep.claims[0]
    # the enum vocabularies the README documents are the contract
    assert c0.stance in STANCES
    assert c0.grounding in GROUNDINGS
    assert rep.exit_reason in EXIT_REASONS
    assert set(c0.__dict__) == CLAIM_KEYS, set(c0.__dict__) ^ CLAIM_KEYS
    assert c0.evidence[0].path == "<inline>"
    assert c0.evidence[0].line == 1
    assert set(c0.evidence[0].__dict__) == EVIDENCE_KEYS


def test_report_top_level_keys_match_go():
    """Every top-level Go report key parses into the Python dataclass —
    no silently dropped fields."""
    d = json.loads(GOLDEN.read_text(encoding="utf-8"))
    rep = aegis.Report.from_dict(d)
    assert set(d) == TOP_KEYS, set(d) ^ TOP_KEYS
    # every Go key lands in the dataclass (nothing silently dropped)
    assert set(rep.__dict__) == TOP_KEYS | {"raw"}


def test_failed_assertions_null_becomes_empty_list():
    """Go emits `"failed_assertions": null` on success; the shim must
    surface `[]`, not None, so callers can iterate unconditionally."""
    d = json.loads(GOLDEN.read_text(encoding="utf-8"))
    assert d["failed_assertions"] is None
    rep = aegis.Report.from_dict(d)
    assert rep.failed_assertions == []
    assert rep.failed_assertions is not None


def test_step_deps_reference_claim_ids():
    d = json.loads(GOLDEN.read_text(encoding="utf-8"))
    claim_ids = {c["id"] for c in d["claims"]}
    for s in d["steps"]:
        unknown = set(s["depends_on"]) - claim_ids
        assert not unknown, unknown


def test_cache_key_is_sha256_hex():
    rep = aegis.Report.from_json(GOLDEN.read_text(encoding="utf-8"))
    assert len(rep.cache.key) == 64
    int(rep.cache.key, 16)  # raises if not hexdigest
    assert rep.cache.hit is False
