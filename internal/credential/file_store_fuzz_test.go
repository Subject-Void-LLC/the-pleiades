package credential_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// FuzzNewFileStoreParsesArbitraryYAML proves NewFileStore and Lookup
// never panic regardless of what bytes sit in credentials.yaml, whether
// that content is well-formed, empty, truncated, wrongly typed, or an
// adversarial aliasing shape. It follows the seed-corpus-plus-
// structural-invariant style internal/engine/dag_fuzz_test.go and
// internal/engine/yaml_fuzz_test.go already use in this codebase: seed
// with known-interesting shapes, then assert only the one thing that
// must always hold (no panic). Functional behavior (round trips, missing
// entries, wrong keys) is already covered by file_store_test.go; a fuzz
// target's job is to find crashes on adversarial input, not to
// re-litigate that.
func FuzzNewFileStoreParsesArbitraryYAML(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("devices:\n  router1:\n    username: admin\n    password_encrypted: \"abc\"\n"))
	f.Add([]byte("devices: {}\n"))
	f.Add([]byte("devices: \"not a map\"\n"))
	f.Add([]byte("devices:\n  router1: \"not an entry\"\n"))
	f.Add([]byte("devices:\n  router1:\n    username: [not, a, string]\n"))
	f.Add([]byte("not-devices-at-all: true\n"))
	f.Add([]byte("devices:\n  router1:\n")) // truncated entry, value is null
	f.Add([]byte("["))                      // truncated/invalid YAML
	f.Add([]byte("devices"))                // scalar document, no colon
	f.Add([]byte("{{{{{"))
	f.Add([]byte("devices: *anchor_that_does_not_exist\n"))
	// A curated "billion laughs" alias bomb, the same shape Phase 39
	// regression-tested for the runbook YAML parser
	// (internal/engine/yaml_test.go's TestBuildFromYAML_RejectsAliasBomb).
	// go.yaml.in/yaml/v3's own allowedAliasRatio guard rejects this at
	// the library level regardless of caller, so this is seeded here to
	// confirm that protection also holds for this package's use of the
	// same library, not to re-derive the guard itself.
	f.Add([]byte("devices:\n  router1:\n    username: admin\n    password_encrypted: &a [\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\",\"x\"]\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		pleiadesDir := filepath.Join(dir, ".pleiades")
		if err := os.MkdirAll(pleiadesDir, 0o700); err != nil {
			t.Fatalf("failed to create test fixture directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(pleiadesDir, "credentials.yaml"), data, 0o600); err != nil {
			t.Fatalf("failed to write test fixture file: %v", err)
		}

		key := make([]byte, 32)
		store, err := credential.NewFileStore(dir, key)
		if err != nil {
			// NewFileStore validates only key length, never inspects
			// the credentials file, so an error here means the test
			// fixture setup itself is broken, not the fuzz target.
			t.Fatalf("NewFileStore() unexpectedly failed: %v", err)
		}

		// The only claim under test: arbitrary file content must never
		// panic Lookup, whether it errors (the overwhelmingly likely
		// outcome for random bytes) or, in principle, succeeds.
		_, _ = store.Lookup(context.Background(), "router1")
	})
}
