// Release Gate for pleiades.builtin.project.write through the real binary:
// a run reads a value off a real sshd, registers it, and keeps it as a
// project file; a second run changes nothing; a check predicts without
// writing; and a path outside the project, into .pleiades or through a
// symbolic link is refused before anything is written.
package main_test

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"
)

// projectWriteRunbook reads a line off the device and keeps it in the
// project, the way a running configuration is kept as a candidate file.
const projectWriteRunbook = `# Keeps a value read off a device as a file in the project.
# Blast radius: the release gate's own sshd container, read only.
# Changes: configs/box.conf in the project.
# Undo: none; the project is under version control.
id: project-write-gate
hosts: box
tasks:
  - name: read it
    exec.command:
      cmd: echo "hostname box-config"
    register: box
  - name: keep it
    pleiades.builtin.project.write:
      path: configs/box.conf
      content: "{{ result.box.stdout }}"
`

// TestCLI_ProjectWriteKeepsARegisteredResult is the gate.
func TestCLI_ProjectWriteKeepsARegisteredResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sshd container test in short mode")
	}
	host, port := startReleaseGateContainer(t)
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{addr}, captureRealHostKey(t, addr))
	if err := os.WriteFile(filepath.Join(home, ".ssh", "known_hosts"), []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "box", "--type", "linux_server", "--set", "host=" + host, "--set", "port=" + strconv.Itoa(port)},
		{"add-credential", "box", "--username", releaseGateSSHUser, "--password", releaseGateSSHPassword},
	} {
		if out, err := runPleiadesWithHome(t, dir, home, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "keep.yaml"), []byte(projectWriteRunbook), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithHome(t, dir, home, "validate", "runbooks/keep.yaml"); err != nil || !strings.Contains(out, "no issues found") {
		t.Fatalf("validate: %v\n%s", err, out)
	}

	kept := filepath.Join(dir, "configs", "box.conf")
	statuses := func(rep jsonReport) (string, string) {
		var read, keep string
		for _, task := range rep.Tasks {
			switch task.ID {
			case "tasks[0]":
				read, _ = task.Stats["stdout"].(string)
			case "tasks[1]":
				keep = task.Status
				if task.Host != "" {
					t.Errorf("the write ran against %q; it acts on no device", task.Host)
				}
			}
		}
		return read, keep
	}
	rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/keep.yaml")
	read, keep := statuses(rep)
	if code != 0 || keep != "changed" || !strings.Contains(read, "hostname box-config") {
		t.Fatalf("first run: exit %d, write %q, read %q:\n%s", code, keep, read, raw)
	}
	got, err := os.ReadFile(kept)
	if err != nil || string(got) != read {
		t.Fatalf("the project file holds %q (%v), want the registered %q", got, err, read)
	}
	if info, _ := os.Stat(kept); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the project file's mode is %v, want 0600", info.Mode().Perm())
	}

	// The same value again changes nothing.
	rep, raw, code = runPleiadesJSONWithHome(t, dir, home, "run", "runbooks/keep.yaml")
	if _, keep := statuses(rep); code != 0 || keep != "ok" {
		t.Errorf("second run: exit %d, write %q:\n%s", code, keep, raw)
	}

	// A check predicts a new file without writing it, and a real ad-hoc
	// call writes it, a directory on the way included.
	rep, raw, code = runPleiadesJSONWithHome(t, dir, home, "adhoc", "box", "pleiades.builtin.project.write", "path=configs/new/one.conf", "content=one", "--mode", "check")
	if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "would_change" {
		t.Errorf("check of a new file: exit %d:\n%s", code, raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "configs", "new")); !os.IsNotExist(err) {
		t.Errorf("a check created the directory: %v", err)
	}
	rep, raw, code = runPleiadesJSONWithHome(t, dir, home, "adhoc", "box", "pleiades.builtin.project.write", "path=configs/new/one.conf", "content=one", "mode=0640")
	if code != 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "changed" {
		t.Errorf("an ad-hoc write: exit %d:\n%s", code, raw)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "configs", "new", "one.conf")); err != nil || string(got) != "one" {
		t.Errorf("the ad-hoc write left %q, %v", got, err)
	}

	// Refused, before anything is written: outside the project, into
	// .pleiades, and through a link that points into .pleiades.
	if err := os.Symlink(filepath.Join("..", ".pleiades"), filepath.Join(dir, "configs", "hidden")); err != nil {
		t.Fatal(err)
	}
	masterKey := filepath.Join(dir, ".pleiades", "master.key")
	before, _ := os.ReadFile(masterKey)
	for path, want := range map[string]string{
		"../x":                      "leaves the project directory",
		".pleiades/master.key":      "holds its credentials",
		"configs/hidden/master.key": "symbolic link",
	} {
		rep, raw, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "box", "pleiades.builtin.project.write", "path="+path, "content=x")
		if code == 0 || len(rep.Tasks) != 1 || rep.Tasks[0].Status != "failed" || !strings.Contains(rep.Tasks[0].Error, want) {
			t.Errorf("path %s: exit %d, want a failed task saying %q:\n%s", path, code, want, raw)
		}
	}
	if after, _ := os.ReadFile(masterKey); string(after) != string(before) {
		t.Error("the master key changed")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "x")); err == nil {
		t.Error("a file was written outside the project")
	}
}
