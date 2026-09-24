// Release gate for --tags and --skip-tags through the real binary, against
// a real sshd, with what ran read back from the device itself.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// tagsGateRunbook writes one marker file per task: tagged web, db, never
// and always, and one untagged.
const tagsGateRunbook = `id: tags-gate
hosts: container1
tasks:
  - name: plain
    fqcn: ssh_exec
    params:
      command: "touch /tmp/tagsgate-plain"
  - name: web
    fqcn: ssh_exec
    tags: web
    params:
      command: "touch /tmp/tagsgate-web"
  - name: db
    fqcn: ssh_exec
    tags: db
    params:
      command: "touch /tmp/tagsgate-db"
  - name: wipe
    fqcn: ssh_exec
    tags: [never, wipe]
    params:
      command: "touch /tmp/tagsgate-wipe"
  - name: audit
    fqcn: ssh_exec
    tags: always
    params:
      command: "touch /tmp/tagsgate-audit"
`

// TestCLI_TagsSelectWhatRuns proves on the device which tasks a filter
// ran: --tags web --skip-tags db runs web and the always task only; no
// filter runs everything but the never task; and a misspelled tag is
// refused before any task runs.
func TestCLI_TagsSelectWhatRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SSH Release Gate container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))

	homeDir := t.TempDir()
	sshDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(sshDir, "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "container1", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "container1", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, homeDir, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "tags.yaml"), []byte(tagsGateRunbook), 0o644); err != nil { // #nosec G306 -- a test runbook fixture
		t.Fatal(err)
	}

	// present lists which markers the device holds, read over this test's
	// own SSH connection, never from pleiades's output.
	present := func() string {
		return strings.Join(strings.Fields(verifyOverSSH(t, addr, "cd /tmp && ls tagsgate-* 2>/dev/null; true")), " ")
	}
	reset := func() { verifyOverSSH(t, addr, "rm -f /tmp/tagsgate-*") }

	// A misspelled tag refuses the run before any task.
	reset()
	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/tags.yaml", "--skip-tags", "dbb")
	if err == nil || !strings.Contains(out, `--skip-tags names "dbb", which no task carries`) {
		t.Errorf("a misspelled --skip-tags ran: %v\n%s", err, out)
	}
	if got := present(); got != "" {
		t.Errorf("a refused run left markers: %s", got)
	}

	// --tags web --skip-tags db: web, plus the always task.
	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/tags.yaml", "--tags", "web", "--skip-tags", "db")
	if err != nil {
		t.Fatalf("filtered run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "selection: --tags web --skip-tags db") || !strings.Contains(out, "plain  (not selected)") {
		t.Errorf("the plan does not show the selection:\n%s", out)
	}
	if got := present(); got != "tagsgate-audit tagsgate-web" {
		t.Errorf("filtered run left %q on the device, want tagsgate-audit tagsgate-web", got)
	}

	// No filter: every task but the never one.
	reset()
	if out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/tags.yaml"); err != nil {
		t.Fatalf("default run failed: %v\n%s", err, out)
	}
	if got := present(); got != "tagsgate-audit tagsgate-db tagsgate-plain tagsgate-web" {
		t.Errorf("default run left %q, want every marker but wipe", got)
	}

	// Naming the never task's own tag runs it.
	reset()
	if out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/tags.yaml", "--tags", "wipe"); err != nil {
		t.Fatalf("--tags wipe failed: %v\n%s", err, out)
	}
	if got := present(); got != "tagsgate-audit tagsgate-wipe" {
		t.Errorf("--tags wipe left %q, want tagsgate-audit tagsgate-wipe", got)
	}

	// validate takes the same flags, in either order around the runbook.
	if out, err := runPleiadesWithHome(t, dir, homeDir, "validate", "--tags", "web", "runbooks/tags.yaml"); err != nil {
		t.Errorf("validate --tags web: %v\n%s", err, out)
	}
	if out, err := runPleiadesWithHome(t, dir, homeDir, "validate", "runbooks/tags.yaml", "--tags", "wbe"); err == nil || !strings.Contains(out, "which no task carries") {
		t.Errorf("validate accepted a misspelled tag: %v\n%s", err, out)
	}
}
