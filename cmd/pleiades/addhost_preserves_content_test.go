package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_AddHostPreservesUnknownContent is the chain audit's regression
// test for "pleiades add-host destroys inventory content it does not
// understand" (.SPECIFICATION/IMPLEMENTATION.md, Phase W2). It starts from
// a real, hand-written inventory.yaml, exactly the shape PLAN.md Section 7
// and the scaffolded project.go comment ("Add hosts by hand below")
// promise a user can produce: a top-level key HostSpec does not model, a
// per-host key HostSpec does not model, and a comment. Run against the
// real built binary (RULE 0), not an in-process call, since the original
// defect was found empirically against the real binary and a mock would
// not have caught it (ParseHosts/EncodeHosts's whole-document rewrite is
// invisible to any test that never reads real bytes off disk).
func TestCLI_AddHostPreservesUnknownContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory.yaml")

	handWritten := `# top-of-file comment: the operator's own note
hosts:
  - id: 11111111-1111-1111-1111-111111111111
    name: webserver1
    type: linux_server
    # per-host comment on webserver1
    notes: do not reboot during business hours
    properties:
      host: 10.0.0.5
groups:
  web:
    - webserver1
vars:
  ansible_user: deploy
`
	if err := os.WriteFile(path, []byte(handWritten), 0o644); err != nil {
		t.Fatalf("failed to seed hand-written inventory: %v", err)
	}

	out, err := runPleiades(t, dir, "add-host", "webserver2", "--type", "linux_server", "--set", "host=10.0.0.6")
	if err != nil {
		t.Fatalf("add-host failed: %v\n%s", out, err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read inventory after add-host: %v", err)
	}
	got := string(after)

	for _, want := range []string{
		"# top-of-file comment",
		"# per-host comment on webserver1",
		"notes: do not reboot during business hours",
		"groups:",
		"web:",
		"vars:",
		"ansible_user: deploy",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected add-host to preserve %q, it did not survive; full file:\n%s", want, got)
		}
	}

	if !strings.Contains(got, "webserver2") {
		t.Errorf("expected the new host to actually be added; full file:\n%s", got)
	}
	if !strings.Contains(got, "webserver1") {
		t.Errorf("expected the original host to still be present; full file:\n%s", got)
	}
}
