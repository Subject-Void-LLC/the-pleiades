// Tests for doctor's readers, against the repository's own go.mod and
// Makefile as well as hand-written input.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRequiredToolchain prefers the toolchain line and falls back to go.
func TestRequiredToolchain(t *testing.T) {
	if got := requiredToolchain("module x\n\ngo 1.26.0\n\ntoolchain go1.26.6\n"); got != "go1.26.6" {
		t.Errorf("with a toolchain line: %q", got)
	}
	if got := requiredToolchain("module x\n\ngo 1.26.0\n"); got != "go1.26.0" {
		t.Errorf("with only a go line: %q", got)
	}
}

// TestAtLeast covers older, equal, newer and a development build.
func TestAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have, need string
		want       bool
	}{
		{"go1.26.8", "go1.26.6", true},
		{"go1.26.6", "go1.26.6", true},
		{"go1.25.3", "go1.26.6", false},
		{"devel go1.27-abc", "go1.26.6", true},
	} {
		if got := atLeast(tc.have, tc.need); got != tc.want {
			t.Errorf("atLeast(%q, %q) = %v, want %v", tc.have, tc.need, got, tc.want)
		}
	}
}

// TestPinnedTools_ReadsTheRealMakefile proves the reader finds every pin
// doctor compares, in the Makefile this repository actually has.
func TestPinnedTools_ReadsTheRealMakefile(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	pins := pinnedTools(string(raw))
	for _, tool := range []string{"gosec", "govulncheck", "actionlint"} {
		if pins[tool] == "" || pins[tool][0] != 'v' {
			t.Errorf("no pin read for %s: %v", tool, pins)
		}
	}
}

// TestModuleVersion reads the mod line of `go version -m`.
func TestModuleVersion(t *testing.T) {
	info := "/go/bin/gosec: go1.26.8\n\tpath\tgithub.com/securego/gosec/v2/cmd/gosec\n\tmod\tgithub.com/securego/gosec/v2\tv2.28.0\th1:abc=\n"
	if got := moduleVersion(info); got != "v2.28.0" {
		t.Errorf("moduleVersion = %q", got)
	}
	if got := moduleVersion("not a binary"); got != "" {
		t.Errorf("moduleVersion of junk = %q", got)
	}
}
