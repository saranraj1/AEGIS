package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aegis-verify/aegis/internal/provider"
	"github.com/aegis-verify/aegis/internal/report"
	"github.com/aegis-verify/aegis/internal/testprov"
)

// cacheTestDir pins the store to a temp dir so tests never write into
// the repo working tree.
func cacheTestDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".aegis", "cache")
}

func TestRunCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	j := loadJob(t, fullJobYAML)
	cdir := cacheTestDir(t)

	rep1, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir})
	if err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	if rep1.Cache.Hit {
		t.Fatal("first run must be a miss")
	}

	// Entry exists now.
	if _, err := os.Stat(filepath.Join(cdir, rep1.Cache.Key+".json")); err != nil {
		t.Fatalf("entry not written: %v", err)
	}

	rep2, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir})
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if !rep2.Cache.Hit {
		t.Fatalf("second run should hit cache: %+v", rep2.Cache)
	}
	if rep2.Cache.Key != rep1.Cache.Key {
		t.Fatalf("keys differ: %s vs %s", rep1.Cache.Key, rep2.Cache.Key)
	}
	// Full fidelity: compare serialized payloads with only cache.hit reset.
	if !fidelityEqual(rep1, rep2) {
		t.Fatalf("replay report differs from original:\nrep1=%+v\nrep2=%+v", rep1, rep2)
	}
}

func TestRunCacheOnlyMiss(t *testing.T) {
	j := loadJob(t, fullJobYAML)
	cdir := cacheTestDir(t)

	// Scripted to explode if ever called: cache-only must make NO calls.
	prov := testprov.NewSeq().ThenErr(fmt.Errorf("provider must not be called"))
	_, err := Run(context.Background(), j, prov, Options{Cache: CacheOnly, CacheDir: cdir})
	if err == nil {
		t.Fatal("cache-only miss must be an error")
	}
	var engErr *Error
	if !errors.As(err, &engErr) || engErr.Kind != KindCacheMiss {
		t.Fatalf("want cache-miss kind, got %v", err)
	}
	if got := prov.Calls(); got != 0 {
		t.Fatalf("provider called %d times in cache-only mode", got)
	}
}

func TestRunCacheOnlyHit(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	j := loadJob(t, fullJobYAML)
	cdir := cacheTestDir(t)

	if _, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir}); err != nil {
		t.Fatalf("seeding run: %v", err)
	}
	prov := testprov.NewSeq().ThenErr(fmt.Errorf("provider must not be called"))
	rep, err := Run(context.Background(), j, prov, Options{Cache: CacheOnly, CacheDir: cdir})
	if err != nil {
		t.Fatalf("Run cache-only: %v", err)
	}
	if !rep.Cache.Hit {
		t.Fatalf("want hit: %+v", rep.Cache)
	}
	if got := prov.Calls(); got != 0 {
		t.Fatalf("provider called %d times on cache-only hit", got)
	}
}

func TestRunCacheOff(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	j := loadJob(t, fullJobYAML)
	cdir := cacheTestDir(t)

	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{Cache: CacheOff, CacheDir: cdir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Cache.Hit {
		t.Fatal("no-cache run cannot be a hit")
	}
	if _, err := os.Stat(filepath.Join(cdir, rep.Cache.Key+".json")); !os.IsNotExist(err) {
		t.Fatalf("no-cache must not write an entry (stat err=%v)", err)
	}
	// A subsequent default-mode run must also miss (nothing written).
	rep2, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir})
	if err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	if rep2.Cache.Hit {
		t.Fatal("entry should not exist after no-cache run")
	}
}

func TestRunAssertionsPartOfKey(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	j := loadJob(t, fullJobYAML)
	cdir := cacheTestDir(t)

	if _, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Tighten the spec: must_claim is in the hash, so a cache-only replay
	// with different assertions must MISS (spec: no replay-old-report-
	// with-new-assertions path).
	j2 := loadJob(t, strings.Replace(fullJobYAML,
		"must_claim:\n    - cache key is sha256",
		"must_claim:\n    - cache key is sha256\n    - also explain streaming", 1))
	_, err := Run(context.Background(), j2,
		testprov.NewSeq().ThenErr(fmt.Errorf("must miss")),
		Options{Cache: CacheOnly, CacheDir: cdir})
	if err == nil {
		t.Fatal("changed assertions must miss cache")
	}
	var engErr *Error
	if !errors.As(err, &engErr) || engErr.Kind != KindCacheMiss {
		t.Fatalf("want cache-miss, got %v", err)
	}
}

func TestRunCorruptedEntryProceedsLive(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	j := loadJob(t, fullJobYAML)
	cdir := cacheTestDir(t)

	if _, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	key := findKeyFile(t, cdir)
	if err := os.WriteFile(filepath.Join(cdir, key), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir})
	if err != nil {
		t.Fatalf("corrupt entry must fall back live: %v", err)
	}
	if rep.Cache.Hit {
		t.Fatal("corrupt entry cannot be a hit")
	}
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "cache entry unreadable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want unreadable-entry warning, got %+v", rep.Warnings)
	}
}

func TestRunCacheOnlyCorruptedEntryIsFatal(t *testing.T) {
	dir := t.TempDir()
	fullFixtures(t, dir, "explain-cache-key")
	j := loadJob(t, fullJobYAML)
	cdir := cacheTestDir(t)

	if _, err := Run(context.Background(), j, provider.NewFake(dir), Options{CacheDir: cdir}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	key := findKeyFile(t, cdir)
	if err := os.WriteFile(filepath.Join(cdir, key), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), j,
		testprov.NewSeq().ThenErr(fmt.Errorf("no calls")),
		Options{Cache: CacheOnly, CacheDir: cdir})
	if err == nil {
		t.Fatal("corrupt entry in cache-only mode must be fatal")
	}
	var engErr *Error
	if !errors.As(err, &engErr) || engErr.Kind != KindCacheStore {
		t.Fatalf("want cache-store-error kind, got %v", err)
	}
}

func TestRunRecordAliasDefault(t *testing.T) {
	// --record is semantically identical to default: the CLI maps both to
	// CacheDefault. Pin the constant relationship so the alias stays true.
	if CacheDefault != CacheMode(0) || (CacheOff == CacheDefault) || (CacheOnly == CacheDefault) {
		t.Fatal("CacheMode enum drifted")
	}
	if DefaultCacheDir != ".aegis/cache" {
		t.Fatalf("DefaultCacheDir = %q", DefaultCacheDir)
	}
}

// --- helpers ------------------------------------------------------------	// fidelityEqual reports whether two reports are identical except for
// their cache.hit flag (replays flip hit to true and keep everything
// else byte-for-byte).
func fidelityEqual(a, b *report.Report) bool {
	ca, cb := a.Cache, b.Cache
	a.Cache, b.Cache = report.CacheInfo{}, report.CacheInfo{}
	defer func() { a.Cache, b.Cache = ca, cb }()
	eqa, err1 := json.Marshal(a)
	eqb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(eqa) == string(eqb)
}

func findKeyFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			return e.Name()
		}
	}
	t.Fatal("no entry in cache dir")
	return ""
}
