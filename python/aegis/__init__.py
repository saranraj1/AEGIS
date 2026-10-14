"""aegis — subprocess-only Python shim for the aegis verification CLI.

The Python side never calls an LLM and imports no HTTP client: it writes
a job file, runs the binary, parses the JSON report, and raises
assertion failures with the disagreeing claims inlined.
"""

from .api import AegisEngineError, Report, verify

__all__ = ["verify", "AegisEngineError", "Report"]
