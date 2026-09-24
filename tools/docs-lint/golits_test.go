// Tests for the Go string literal pass.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanGoLiterals proves the pass reports a string literal citing a
// gitignored document, and ignores the same citation in a comment and in
// a test file, which only a contributor reads.
func TestScanGoLiterals(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("cmd/tool/main.go", "package main\n\n// See PLAN.md Section 1 for why.\nvar help = \"see PLAN.md Section 1\"\n")
	write("internal/x/x.go", "package x\n\nconst ok = `ships in docs/03-migrating-from-ansible.md`\n")
	write("pkg/y/y_test.go", "package y\n\nvar cite = \"IMPLEMENTATION.md\"\n")

	findings, files, err := scanGoLiterals(root)
	if err != nil {
		t.Fatal(err)
	}
	if files != 2 {
		t.Errorf("scanned %d files, want 2 (the test file is skipped)", files)
	}
	if len(findings) != 1 || findings[0].line != 4 || filepath.Base(findings[0].path) != "main.go" {
		t.Errorf("findings = %+v, want exactly the string literal on main.go line 4", findings)
	}
}
