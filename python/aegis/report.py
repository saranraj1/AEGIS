"""Report dataclass parsing the Go engine's JSON output."""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any


@dataclass
class Evidence:
    path: str
    quote: str
    line: int = 0


@dataclass
class Claim:
    id: str
    text: str
    stance: str = "asserted"
    grounding: str = "ungrounded"
    grounding_reason: str = ""
    evidence: list[Evidence] = field(default_factory=list)


@dataclass
class Step:
    id: str
    text: str
    depends_on: list[str] = field(default_factory=list)


@dataclass
class Critique:
    verdict: str = "agree"
    notes: str = ""
    disputed_claim_ids: list[str] = field(default_factory=list)


@dataclass
class CodeCheck:
    kind: str
    language: str
    ok: bool
    detail: str = ""


@dataclass
class FailedAssertion:
    type: str
    expected: str = ""
    actual: str = ""
    detail: str = ""


@dataclass
class CacheInfo:
    hit: bool = False
    key: str = ""


@dataclass
class Report:
    """One run's machine-readable audit artifact."""

    id: str
    ok: bool
    exit_reason: str
    prompt: str
    output: str
    claims: list[Claim] = field(default_factory=list)
    steps: list[Step] = field(default_factory=list)
    critique: Critique = field(default_factory=Critique)
    code_checks: list[CodeCheck] = field(default_factory=list)
    failed_assertions: list[FailedAssertion] = field(default_factory=list)
    warnings: list[str] = field(default_factory=list)
    cache: CacheInfo = field(default_factory=CacheInfo)
    raw: dict[str, Any] = field(default_factory=dict)

    @property
    def failed(self) -> list[FailedAssertion]:
        """Alias used in error messages."""
        return self.failed_assertions

    def human_failures(self) -> str:
        lines = [f"{fa.type}: expected={fa.expected!r} actual={fa.actual!r}" for fa in self.failed_assertions]
        for fa in self.failed_assertions:
            if fa.type in ("must_not_claim", "max_ungrounded", "grounded", "critique_must_agree"):
                # inline the disagreeing claims so the developer does not
                # have to open the report (spec: claims inlined on failure)
                claims = [c.text for c in self.claims]
                lines.append("  claims considered: " + " | ".join(claims[:10]))
        return "\n".join(lines)

    @classmethod
    def from_json(cls, text: str) -> Report:
        return cls.from_dict(json.loads(text))

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> Report:
        claims = [
            Claim(
                id=str(c.get("id", "")),
                text=str(c.get("text", "")),
                stance=str(c.get("stance", "asserted")),
                grounding=str(c.get("grounding", "ungrounded")),
                grounding_reason=str(c.get("grounding_reason", "")),
                evidence=[
                    Evidence(
                        path=str(e.get("path", "")),
                        quote=str(e.get("quote", "")),
                        line=int(e.get("line") or 0),
                    )
                    for e in (c.get("evidence") or [])
                ],
            )
            for c in (d.get("claims") or [])
        ]
        steps = [
            Step(
                id=str(s.get("id", "")),
                text=str(s.get("text", "")),
                depends_on=list(s.get("depends_on") or []),
            )
            for s in (d.get("steps") or [])
        ]
        cr = d.get("critique") or {}
        cc = [
            CodeCheck(
                kind=str(k.get("kind", "")),
                language=str(k.get("language", "")),
                ok=bool(k.get("ok", False)),
                detail=str(k.get("detail", "")),
            )
            for k in (d.get("code_checks") or [])
        ]
        fa = [
            FailedAssertion(
                type=str(f.get("type", "")),
                expected=str(f.get("expected", "")),
                actual=str(f.get("actual", "")),
                detail=str(f.get("detail", "")),
            )
            for f in (d.get("failed_assertions") or [])
        ]
        cache_d = d.get("cache") or {}
        return cls(
            id=str(d.get("id", "")),
            ok=bool(d.get("ok", False)),
            exit_reason=str(d.get("exit_reason", "engine_error")),
            prompt=str(d.get("prompt", "")),
            output=str(d.get("output", "")),
            claims=claims,
            steps=steps,
            critique=Critique(
                verdict=str(cr.get("verdict", "agree")),
                notes=str(cr.get("notes", "")),
                disputed_claim_ids=list(cr.get("disputed_claim_ids") or []),
            ),
            code_checks=cc,
            failed_assertions=fa,
            warnings=list(d.get("warnings") or []),
            cache=CacheInfo(hit=bool(cache_d.get("hit", False)), key=str(cache_d.get("key", ""))),
            raw=d,
        )
