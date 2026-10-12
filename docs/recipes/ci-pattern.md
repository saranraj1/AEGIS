# Aegis CI recipes

Pattern: **record on main, replay on PRs.** LLM cost and flake live only
on `main`; PRs are deterministic re-replays of the recorded audit.

## 1. Record on main

```yaml
jobs:
  aegis-record:
    if: github.ref == 'refs/heads/main'
    runs-on: ubuntu-latest
    env:
      USER_LLM_API_KEY: ${{ secrets.USER_LLM_API_KEY }}
      USER_LLM_BASE_URL: ${{ secrets.USER_LLM_BASE_URL }}
      USER_LLM_MODEL: ${{ secrets.USER_LLM_MODEL }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod, cache: true }
      - run: go install ./cmd/aegis
      # write the store back so PRs can read it
      - uses: actions/cache/save@v4
        with:
          path: .aegis/cache
          key: aegis-cache-${{ github.run_id }}

aegis:
    needs: aegis-record
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/cache/restore@v4
        with:
          path: .aegis/cache
          key: aegis-cache-${{ github.run_id }}
          fail-on-cache-miss: true
```

## 2. Replay on PRs

```yaml
aegis-verify:
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    steps: [ checkout, setup-go, install, restore cache as above ]
      - run: aegis run apps/policy-job.yaml --offline --cache-dir .aegis/cache
```

Freeze the store with an exact key, not a rolling one, so a re-recorded
`main` never silently changes what PRs verify.

## 3. Fail closed on cache miss, fail open on asserts

- `--offline` on a **missing key** is exit 2 → the PR is red. That is
  the desired default: verify never claims a result it has no report
  for.
- Replayed reports keep their original `ok`/`failed_assertions`, so a
  job that was failing when recorded stays failing when replayed — the
  fix then requires a proper re-run on `main`, not a PR replay.
- JUnit: add `--junit junit.xml` to both modes and upload
  `actions/upload-artifact@v4` with `if: always()` for readable checks.

## 4. Golden rule: changing an assertion

Assertion text is hashed into the cache key. If you tighten
`must_claim` on a PR branch:

1. The PR replay misses cache (new key) → red with
   `error: cache-miss: no cached report for key …` — expected.
2. Merge after recording once on `main` with the same text (`--record`),
   or accept the one live run on the PR (`--record` in the PR job, still
   env-gated).
3. If assertions keep changing, run `aegis cache gc --older-than 168h`
   weekly on `main` so the store stays small.
