package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/aegis-verify/aegis/internal/cache"
	"github.com/aegis-verify/aegis/internal/engine"
)

// cacheCmd implements `aegis cache gc --older-than <dur>`.
func cacheCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "gc" {
		fmt.Fprintln(stderr, "error: invalid-args: cache supports only the gc subcommand")
		return 2
	}
	fs := flag.NewFlagSet("cache gc", flag.ContinueOnError)
	fs.SetOutput(stderr)
	olderThan := fs.Duration("older-than", 0, "delete entries with cached_at older than this duration (e.g. 720h)")
	cacheDir := fs.String("cache-dir", engine.DefaultCacheDir, "replay cache directory")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(stderr, "error: invalid-args: %v\n", err)
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "error: invalid-args: cache gc takes no positional arguments")
		return 2
	}
	if *olderThan <= 0 {
		fmt.Fprintln(stderr, "error: invalid-args: --older-than is required and must be positive (e.g. --older-than 720h)")
		return 2
	}
	store := cache.NewStore(*cacheDir)
	n, err := store.GC(*olderThan)
	if err != nil {
		fmt.Fprintf(stderr, "error: cache-store-error: %v\n", err)
		return 2
	}
	fmt.Fprintf(stdout, "removed %d cache entries\n", n)
	return 0
}
