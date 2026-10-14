# pytest example — aegis verify() with a stub binary

Self-contained demonstration of the aegis Python shim: pytest drives
`aegis.verify()` against a tiny fake `aegis` executable, so the full
loop (job file → subprocess → JSON report → assertion) runs green in
CI with **zero model access**.

## Run it

From the repo root:

```sh
pytest examples/pytest-example -q
```

From any environment where the package is installed (`pip install -e
python[dev]`), this directory runs standalone; otherwise `conftest.py`
prepends the sibling `python/` directory to `sys.path`.

## What it shows

- `verify()` returns a `Report`; assert `r.ok` and inspect claims.
- `raise_on_fail=True` raises an `AssertionError` with the failed
  assertions and the disagreeing claims inlined.
- Without it, a plain `assert r.ok` still fails; the optional pytest
  plugin (`pytest11` entry point) appends a friendlier hint to that
  failure in the result report.
- `AEGIS_BINARY` points the shim at the stub — the same override a
  real CI job or a developer without a built binary uses.

The stub lives in `conftest.py` (`_make_stub`): a `python` payload that
prints a canned JSON report, wrapped in an `aegis.cmd` launcher on
Windows or a shebang `aegis` script on POSIX.
