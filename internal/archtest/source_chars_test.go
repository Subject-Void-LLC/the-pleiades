// Package archtest: that no Go source holds an invisible control or
// bidirectional character.
package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// skippedDirs are directories no Go source this module owns lives in.
var skippedDirs = map[string]bool{".git": true, "node_modules": true, ".claude": true}

// TestNoInvisibleControlCharactersInGoSource keeps every Go file in the
// module, tests and testdata included, free of the characters a terminal
// or an editor acts on instead of showing: control characters other than
// tab and newline, and the Unicode bidirectional overrides "Trojan Source"
// uses to make code read differently from how it compiles. Code that
// needs one writes an escape (a backslash and the code point) instead.
//
// This exists because it happened: a tool writing files in this
// repository decoded the escapes in its input, and real bidirectional
// overrides landed in internal/termsafe, the package written to catch
// them (FAILURE_PATTERNS 256).
func TestNoInvisibleControlCharactersInGoSource(t *testing.T) {
	root := repoRoot(t)
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 -- a file of this repository, found by walking it
		if err != nil {
			return err
		}
		scanned++
		for n, line := range strings.Split(string(data), "\n") {
			if err := termsafe.Check(strings.TrimSuffix(line, "\r")); err != nil {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d: %v; write it as an escape instead", rel, n+1, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %s, so the walk is misaimed", scanned, root)
	}
}
