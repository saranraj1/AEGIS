package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aegis-verify/aegis/internal/report"
)

// keyRe validates cache keys so the store never treats arbitrary paths
// as stored entries.
var keyRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Store is the file-backed replay store: one `<key>.json` per entry in
// dir, each holding the full report plus a `cached_at` RFC3339 stamp.
type Store struct {
	dir string
}

// NewStore returns a Store rooted at dir. The directory is created on
// the first Put; Get and GC read without creating it.
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir reports the store root.
func (s *Store) Dir() string { return s.dir }

// envelope is the on-disk shape: the report fields flattened at the top
// level plus the write timestamp.
type envelope struct {
	report.Report
	CachedAt string `json:"cached_at"`
}

// Put stores the report under key with a cached_at stamp of "now".
// Writes are atomic (temp file + rename) so a crash cannot leave a
// truncated entry. Invalid keys are rejected.
func (s *Store) Put(key string, rep *report.Report) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("cache: invalid key %q", key)
	}
	if rep == nil {
		return errors.New("cache: cannot store nil report")
	}
	env := envelope{Report: *rep, CachedAt: time.Now().UTC().Format(time.RFC3339)}
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("cache: marshal entry: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("cache: mkdir %s: %w", s.dir, err)
	}
	final := filepath.Join(s.dir, key+".json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("cache: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cache: rename %s: %w", final, err)
	}
	return nil
}

// Get returns the report stored under key and its cached_at time.
// A missing file reports an error matching os.ErrNotExist; a malformed
// entry reports a parse error. Invalid keys are reported as missing.
func (s *Store) Get(key string) (*report.Report, time.Time, error) {
	if !keyRe.MatchString(key) {
		return nil, time.Time{}, os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(s.dir, key+".json"))
	if err != nil {
		return nil, time.Time{}, err // wraps os.ErrNotExist via *PathError.
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, time.Time{}, fmt.Errorf("cache: malformed entry %s: %w", key, err)
	}
	when, err := time.Parse(time.RFC3339, env.CachedAt)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("cache: entry %s has bad cached_at %q: %w", key, env.CachedAt, err)
	}
	rep := env.Report
	return &rep, when, nil
}

// GC deletes entries whose cached_at is older than the cutoff duration
// and returns how many were removed. Malformed entries are skipped and
// left in place (they were never valid hits); they are not errors since
// a stray file must not break garbage collection.
func (s *Store) GC(olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("cache: gc read %s: %w", s.dir, err)
	}
	cutoff := time.Now().UTC().Add(-olderThan)
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		key := strings.TrimSuffix(name, ".json")
		if !keyRe.MatchString(key) {
			continue
		}
		_, when, err := s.Get(key)
		if err != nil {
			continue // malformed or unreadable: leave it.
		}
		if when.Before(cutoff) {
			if os.Remove(filepath.Join(s.dir, name)) == nil {
				removed++
			}
		}
	}
	return removed, nil
}
