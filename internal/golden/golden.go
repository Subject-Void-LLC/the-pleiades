// Package golden compares a test's output with a file committed beside the
// test, so a change to a contract that has no schema fails until someone
// updates the file on purpose.
//
// A test calls Assert (or JSON) with the path of its golden file. Run with
// -update-golden, the test binary writes the file instead of comparing, and
// the diff of that file is what a reviewer reads. Run without it, any
// difference fails the test with the first lines that differ and the
// command that rewrites the file.
package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update is set by the -update-golden flag, which every test binary that
// links this package accepts.
var update = flag.Bool("update-golden", false, "write golden files instead of comparing with them")

// contextLines is how many equal lines a diff shows before the first
// difference, and shownLines how many lines of each side it shows from it.
const (
	contextLines = 2
	shownLines   = 6
)

// Assert compares got with the file at path, or writes got to path when the
// test runs with -update-golden. A missing file or any difference fails t.
func Assert(t testing.TB, path string, got []byte) {
	t.Helper()
	if *update {
		// The directory may not exist the first time a golden file is made.
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Errorf("golden: creating the directory of %s: %v", path, err)
			return
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Errorf("golden: writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path) // #nosec G304 -- a path the calling test names
	if err != nil {
		t.Errorf("golden: reading %s: %v\ncreate it with: %s", path, err, updateCommand(t))
		return
	}
	if bytes.Equal(want, got) {
		return
	}
	t.Errorf("golden: %s differs from what the test produced:\n%s\nif the change is intended, update it with: %s",
		path, diff(string(want), string(got)), updateCommand(t))
}

// JSON marshals v with two-space indentation and a trailing newline, then
// compares it with the file at path as Assert does.
func JSON(t testing.TB, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Errorf("golden: marshaling the value for %s: %v", path, err)
		return
	}
	Assert(t, path, append(data, '\n'))
}

// diff returns a short unified-style view of where want and got first part:
// a few equal lines for context, then up to shownLines of each side, marked
// "-" for the golden file and "+" for the test's output.
func diff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	first := 0
	// Find the first line index at which the two sides disagree.
	for first < len(w) && first < len(g) && w[first] == g[first] {
		first++
	}
	// The differing run ends where the two sides agree again, or after
	// shownLines lines, whichever comes first.
	end := first
	for end-first < shownLines && (end < len(w) || end < len(g)) &&
		(end >= len(w) || end >= len(g) || w[end] != g[end]) {
		end++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@@ first difference at line %d, column %d @@\n", first+1, column(w, g, first))
	for i := max(0, first-contextLines); i < first; i++ {
		fmt.Fprintf(&b, "  %s\n", w[i])
	}
	for i := first; i < min(end, len(w)); i++ {
		fmt.Fprintf(&b, "- %s\n", w[i])
	}
	for i := first; i < min(end, len(g)); i++ {
		fmt.Fprintf(&b, "+ %s\n", g[i])
	}
	return b.String()
}

// column returns the 1-based byte column where line i of w and g first
// differ, which on a long JSON line is where to look.
func column(w, g []string, i int) int {
	if i >= len(w) || i >= len(g) {
		return 1
	}
	c := 0
	for c < len(w[i]) && c < len(g[i]) && w[i][c] == g[i][c] {
		c++
	}
	return c + 1
}

// updateCommand names the go test command that rewrites this test's golden
// file: the package as a path from the module root, and the top-level test.
func updateCommand(t testing.TB) string {
	test, _, _ := strings.Cut(t.Name(), "/")
	return fmt.Sprintf("go test ./%s -run '^%s$' -update-golden", packageDir(), test)
}

// packageDir returns the test's working directory (its package's directory,
// where go test runs it) relative to the module root, or "..." when no
// go.mod is found above it.
func packageDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return "..."
	}
	// Walk up from the package directory to the first one holding go.mod.
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			rel, err := filepath.Rel(dir, wd)
			if err != nil {
				return "..."
			}
			return filepath.ToSlash(rel)
		}
		if filepath.Dir(dir) == dir {
			return "..."
		}
	}
}
