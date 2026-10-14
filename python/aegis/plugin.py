"""pytest plugin (optional sugar): failure polish + node-id pickup.

Registered via the `pytest11` entry point in pyproject.toml. With it,
`verify(id=None)` defaults the job id to the running test's node id.
Without it, `import aegis; r = aegis.verify(...); assert r.ok` works
identically — the plugin only adds polish, never behavior.
"""

from __future__ import annotations

import pytest

from .api import current_node_id, verify

__all__ = ["aegis_fixup", "pytest_configure"]


@pytest.fixture
def aegis(request):
    """Return the verify function bound to this test's node id.

    The plugin fixture re-exports the module-level verify, publishing
    the node id on a context var consumed by verify(id=None).
    """

    def _verify(*args, **kwargs):
        current_node_id(request.node.nodeid)
        return verify(*args, **kwargs)

    _verify.__name__ = verify.__name__
    _verify.__doc__ = verify.__doc__
    return _verify


def pytest_configure(config):
    cfg = getattr(config, "_aegis_cfg", None)
    if cfg is None:
        config._aegis_cfg = True


def pytest_runtest_makereport(item, call):
    """Polish hook: on a plain `assert r.ok` failure, add a hint."""
    if call.excinfo is not None and call.excinfo.errisinstance(AssertionError):
        extra = "aegis: pass raise_on_fail=True or use the `aegis` fixture for inlined failures"
        call.excinfo.value.args = (*call.excinfo.value.args, extra)  # type: ignore[union-attr]


# keep flake/ruff aware of the intentional re-export
aegis_fixup = pytest_runtest_makereport

# local import to satisfy the plugin's error-name usage without a cycle
