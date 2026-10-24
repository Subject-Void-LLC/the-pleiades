// Package archtest: no test fixture hides behind an ignore rule.
package archtest

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestNoTestdataFileIsIgnored: a file under a testdata directory that
// .gitignore matches is never committed, so its tests pass in the tree that
// wrote it and fail in every other clone. *.log hid pkg/cloudinit's console
// capture (FAILURE_PATTERNS 401), and its path-by-path exception did not stop
// the same rule hiding tools/failures' captured logs two phases later (634).
// Add an exception for the fixture, or rename it.
func TestNoTestdataFileIsIgnored(t *testing.T) {
	root := repoRoot(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("needs git: ", err)
	}
	// A tree copied to another machine without its history has no ignore
	// rules to ask about; the leg that wrote the tree checks it.
	if out, err := exec.Command(git, "-C", root, "rev-parse", "--is-inside-work-tree").Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		t.Skip("needs a git work tree: ", root, " is not one")
	}
	out, err := exec.Command(git, "-C", root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z").Output()
	if err != nil {
		t.Fatalf("listing ignored files: %v", err)
	}
	for _, path := range bytes.Split(out, []byte{0}) {
		p := string(path)
		// A dot directory (.claude, .IGNORE) is local and never ships.
		if p == "" || strings.HasPrefix(p, ".") {
			continue
		}
		if strings.HasPrefix(p, "testdata/") || strings.Contains(p, "/testdata/") {
			t.Errorf("%s is a test fixture .gitignore hides, so no other clone has it: add an exception for it", p)
		}
	}
}
