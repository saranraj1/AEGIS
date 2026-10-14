"""Import-hygiene gate (phase 6 verification): the shim must never
import an HTTP client or an SDK.

Enforced statically by parsing every aegis module's source with ast and
inspecting each import statement / import-from node. The gate audits
ONLY the aegis package — pytest and site-packages legitimately import
urllib pieces of their own and must not be swept into the check.
"""

from __future__ import annotations

import ast
import importlib
import pathlib
import sys

FORBIDDEN = {
    "http",
    "http.client",
    "httpx",
    "requests",
    "urllib.request",
    "aiohttp",
    "openai",
    "anthropic",
    "googleapiclient",
    "httplib",
    "urllib3",
}


def _aegis_modules():
    """Yield (module_name, source_path) for the installed aegis package."""
    import aegis as pkg

    pkg_path = pathlib.Path(pkg.__file__).parent
    for src in sorted(pkg_path.glob("*.py")):
        mod_name = f"aegis.{src.stem}" if src.name != "__init__.py" else "aegis"
        yield mod_name, src


def test_aegis_modules_exist():
    names = [n for n, _ in _aegis_modules()]
    assert "aegis" in names and any(n.startswith("aegis.") for n in names)


def test_no_http_client_imports():
    """ Every import statement in the package must be clean. """
    offenders = []
    for mod_name, src in _aegis_modules():
        tree = ast.parse(src.read_text(encoding="utf-8"), filename=str(src))
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                names = [a.name.split(".")[0] for a in node.names]
            elif isinstance(node, ast.ImportFrom):
                # 'from .runner import run_binary' → node.module may be
                # None (relative intra-package import, always safe).
                names = [] if node.module is None else [node.module.split(".")[0]]
            else:
                continue
            for name in names:
                if name in FORBIDDEN or name.startswith("urllib"):
                    offenders.append(
                        f"{mod_name}: imports forbidden {name!r} (line {getattr(node, 'lineno', '?')})"
                    )
    assert not offenders, "\n".join(offenders)


def test_loaded_symbols_are_clean():
    """Belt and braces: after importing the package + plugin, spot-check
    that no aegis module's dict holds an HTTP-client module reference."""
    importlib.import_module("aegis")
    importlib.import_module("aegis.plugin")
    loaded = {n: m for n, m in sys.modules.items() if n == "aegis" or n.startswith("aegis.")}
    assert loaded, "no aegis modules loaded after import"
    for name, mod in loaded.items():
        for val in vars(mod).values():
            mname = getattr(val, "__name__", "")
            if mname in FORBIDDEN:
                raise AssertionError(f"{name} binds forbidden module {mname!r}")


def test_runner_uses_only_subprocess():
    from aegis import runner

    src = pathlib.Path(runner.__file__).read_text(encoding="utf-8")
    for bad in ("import httpx", "import requests", "import openai", "urllib.request", "urllib3"):
        assert bad not in src, f"runner.py must not reference {bad}"
