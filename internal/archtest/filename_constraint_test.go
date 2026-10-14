// Package archtest: a file name that constrains a build only on purpose.
package archtest

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// filenameGOOS and filenameGOARCH are the suffixes Go reads from a file
// name as a build constraint: `_linux.go` compiles only on Linux,
// `_arm64_test.go` only on arm64. go/build's own lists, as of Go 1.26.
var (
	filenameGOOS = []string{"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js",
		"linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos"}
	filenameGOARCH = []string{"386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips",
		"mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv", "riscv64",
		"s390", "s390x", "sparc", "sparc64", "wasm"}
)

// impliedConstraint returns the OS or architecture name a Go file name
// constrains its build to, or "" for none.
func impliedConstraint(name string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(name, ".go"), "_test")
	parts := strings.Split(stem, "_")
	if len(parts) < 2 {
		return ""
	}
	last := parts[len(parts)-1]
	for _, known := range append(append([]string{}, filenameGOOS...), filenameGOARCH...) {
		if last == known {
			return last
		}
	}
	return ""
}

// TestFileNamesConstrainBuildsOnlyOnPurpose: a file whose name ends in an
// OS or architecture (probe_linux.go) compiles only there, which Go applies
// silently. One did, by accident: the Linux server's onboarding prober,
// which probes a remote server over SSH, was missing from every build but
// Linux's, so `pleiades onboard` on a Mac answered that linux_server has no
// prober. Such a name is therefore allowed only with a //go:build line
// saying the same thing on purpose.
func TestFileNamesConstrainBuildsOnlyOnPurpose(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		checked++
		implied := impliedConstraint(d.Name())
		if implied == "" {
			return nil
		}
		if !hasBuildLine(t, path, implied) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s compiles only on %s because of its name, and carries no //go:build line naming %s: "+
				"rename it, or state the constraint on purpose", rel, implied, implied)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no Go file was checked, so this rule examined nothing")
	}
}

// hasBuildLine reports whether path's header has a //go:build line that
// names constraint.
func hasBuildLine(t *testing.T, path, constraint string) bool {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 -- a file under this repository's own root
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "package ") {
			return false
		}
		if strings.HasPrefix(line, "//go:build") && strings.Contains(line, constraint) {
			return true
		}
	}
	return false
}

// TestImpliedConstraint pins the file-name reading the rule above relies on.
func TestImpliedConstraint(t *testing.T) {
	for name, want := range map[string]string{
		"probe_linux.go":             "linux",
		"load_windows_test.go":       "windows",
		"x_arm64.go":                 "arm64",
		"probe_linux_server.go":      "",
		"linux.go":                   "",
		"probe_container.go":         "",
		"capabilities_linux_test.go": "linux",
	} {
		if got := impliedConstraint(name); got != want {
			t.Errorf("impliedConstraint(%q) = %q, want %q", name, got, want)
		}
	}
}
