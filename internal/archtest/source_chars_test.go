// Package archtest: that no Go source holds an invisible control,
// formatting or bidirectional character.
package archtest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
)

// skippedDirs are directories no Go source this module owns lives in.
var skippedDirs = map[string]bool{".git": true, "node_modules": true, ".claude": true}

// invisible reports whether r is a character Go source may not hold raw:
// one a terminal acts on (termsafe.Unsafe, the control characters and the
// bidirectional overrides "Trojan Source" uses), or one Unicode's own tables
// say renders as nothing or as a line break. Those are the format controls
// (Cf: zero-width spaces and joiners, the soft hyphen), the line and
// paragraph separators U+2028 and U+2029 (Zl, Zp: a viewer that breaks the
// line there shows a comment's tail as code), the variation selectors, and
// the default-ignorable code points, which hold the tag characters and the
// Hangul fillers Go accepts as letters in an identifier. termsafe.Unsafe is
// left as it is: it escapes device output, where a joiner or a variation
// selector is part of real text.
func invisible(r rune) bool {
	if r == '\t' {
		return false
	}
	return termsafe.Unsafe(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point)
}

// scanGoSource reports every invisible character in the Go files under root,
// as "path:line:column: U+XXXX", and how many files it read. It reads Go
// source only: a directory in skippedDirs, an entry that is not a regular
// file (an editor's lock-file link), a macOS AppleDouble file ("._x.go") and
// a file gone between the directory read and the file read are passed over.
// Any other error stops the scan.
func scanGoSource(root string) (findings []string, scanned int, err error) {
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || !d.Type().IsRegular() || strings.HasPrefix(d.Name(), "._") {
			return nil
		}
		data, err := os.ReadFile(path) // #nosec G304 -- a file of this repository, found by walking it
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		scanned++
		rel, _ := filepath.Rel(root, path)
		for n, line := range strings.Split(string(data), "\n") {
			for col, r := range []rune(strings.TrimSuffix(line, "\r")) {
				if invisible(r) {
					findings = append(findings, fmt.Sprintf("%s:%d:%d: U+%04X", rel, n+1, col+1, r))
				}
			}
		}
		return nil
	})
	return findings, scanned, err
}

// TestNoInvisibleControlCharactersInGoSource keeps every Go file in the
// module, tests and testdata included, free of invisible characters
// (invisible). Code that needs one writes an escape (a backslash and the
// code point) instead.
//
// This exists because it happened: a tool writing files in this
// repository decoded the escapes in its input, and real bidirectional
// overrides landed in internal/termsafe, the package written to catch
// them (FAILURE_PATTERNS 256).
func TestNoInvisibleControlCharactersInGoSource(t *testing.T) {
	root := repoRoot(t)
	findings, scanned, err := scanGoSource(root)
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	for _, f := range findings {
		t.Errorf("%s; write it as an escape instead", f)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %s, so the walk is misaimed", scanned, root)
	}
}

// TestScanGoSource_FindsWhatItMustAndOnlyThat is the scan's negative
// control, over a tree of its own: each kind of invisible character planted
// in a Go file is reported; the same under a skipped directory, in a file
// that is not Go, in an AppleDouble file and behind a dangling editor lock
// link is not, and neither the link nor a CRLF file stops or fails the scan.
func TestScanGoSource_FindsWhatItMustAndOnlyThat(t *testing.T) {
	root := t.TempDir()
	// Built from their code points: written raw, each would trip the check
	// on this very file.
	planted := map[string]rune{
		"bidi.go":         0x202E, // RIGHT-TO-LEFT OVERRIDE
		"linesep.go":      0x2028, // LINE SEPARATOR
		"zerowidth.go":    0x200B, // ZERO WIDTH SPACE
		"tag.go":          0xE0041,
		"selector.go":     0xFE0F,
		"hangul.go":       0x3164, // HANGUL FILLER, a letter to the compiler
		"softhyphen.go":   0x00AD,
		"paragraphsep.go": 0x2029,
	}
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, r := range planted {
		write(name, "package p\n// a"+string(r)+"b\n")
	}
	write(filepath.Join("node_modules", "skipped.go"), "package p\n// "+string(rune(0x202E))+"\n")
	write("notes.txt", "// "+string(rune(0x202E))+"\n")
	write("._apple.go", "\x00\x05\x16\x07"+string(rune(0x202E)))
	write("crlf.go", "package p\r\n// fine\r\n")
	err := os.Symlink(filepath.Join(root, "gone.go"), filepath.Join(root, ".#lock.go"))
	need.Require(t, "symlinks", err == nil, "this machine cannot make a symbolic link here")

	findings, scanned, err := scanGoSource(root)
	if err != nil {
		t.Fatalf("the scan stopped: %v", err)
	}
	if scanned != len(planted)+1 {
		t.Errorf("scanned %d files; want %d (the planted ones and the CRLF one)", scanned, len(planted)+1)
	}
	for name, r := range planted {
		want := fmt.Sprintf("%s:2:5: U+%04X", name, r)
		if !strings.Contains(strings.Join(findings, "\n"), want) {
			t.Errorf("U+%04X in %s was not reported; findings: %q", r, name, findings)
		}
	}
	if len(findings) != len(planted) {
		t.Errorf("findings = %q; want exactly the %d planted", findings, len(planted))
	}
}
