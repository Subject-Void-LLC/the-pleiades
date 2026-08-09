package ent_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

// FuzzOpenEmbedded fuzzes the path argument to OpenEmbedded with
// adversarial strings: path-traversal-looking sequences, the empty string,
// unicode, NUL bytes, and very long strings.
//
// Every candidate path is nested inside a fresh t.TempDir() and has all
// path separators (and NUL bytes) stripped from the fuzzed component
// itself, so a fuzzed value can never make OpenEmbedded write outside its
// own per-iteration sandbox: filepath.Join(dir, "embedded", safe) can only
// ever resolve to somewhere at or under dir, regardless of what content the
// fuzzer generates, since the only '/' characters present come from the
// fixed prefix this test controls.
func FuzzOpenEmbedded(f *testing.F) {
	// Seed corpus: the specific adversarial shapes called out in the task.
	f.Add("")
	f.Add("../../../etc/passwd")
	f.Add("..")
	f.Add("normal-name.db")
	f.Add("unicode-é中文-\U0001F600")
	f.Add(strings.Repeat("a", 4096))
	f.Add("has\x00nul")
	f.Add("has?query#fragment&chars")
	f.Add("has spaces and\ttabs")

	f.Fuzz(func(t *testing.T, rawPath string) {
		ctx := context.Background()
		dir := t.TempDir()

		// Neutralize traversal and NUL by removing every '/' and NUL byte
		// from the fuzzed value. What survives is used purely as a
		// filename component, never as a directory-structuring path, so it
		// cannot escape dir no matter what the fuzzer produces.
		safe := strings.Map(func(r rune) rune {
			if r == '/' || r == 0 {
				return '_'
			}
			return r
		}, rawPath)
		path := filepath.Join(dir, "embedded", safe)

		client, err := ent.OpenEmbedded(ctx, path)

		// The contract under test: never both a usable client and an
		// error, never neither. Exactly one of the two must be non-nil.
		if err != nil {
			if client != nil {
				// Close it anyway so a violation of the contract does not
				// also leak a file descriptor for the rest of the fuzz run.
				client.Close()
				t.Fatalf("OpenEmbedded(%q) returned both a client and an error: %v", path, err)
			}
			return
		}
		if client == nil {
			t.Fatalf("OpenEmbedded(%q) returned neither a client nor an error", path)
		}
		defer client.Close()

		// A "valid usable client" means it can actually serve a query, not
		// merely that the pointer is non-nil.
		if _, err := client.Device.Query().Count(ctx); err != nil {
			t.Fatalf("client returned by OpenEmbedded(%q) is not usable: %v", path, err)
		}
	})
}
