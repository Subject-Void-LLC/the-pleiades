// Package main_test: the Release Gate for the runbook check_mode key
// (IMPLEMENTATION.md Phase 46, FAILURE_PATTERNS 252).
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

// The paths the runbook-key gate's tasks name on the device.
const (
	keyGateRoot    = "/tmp/pleiades-key-gate"
	keyGateChecked = keyGateRoot + "/checked"
	keyGateRun     = keyGateRoot + "/run"
	keyGateWhole   = keyGateRoot + "/whole"
	// keyGateCommand is what the guarded command would create if it ran.
	keyGateCommand = "/tmp/pleiades-key-gate-command"
)

// keyGateTask is one file.directory task against container1, with extra
// keys (such as check_mode) placed before fqcn.
func keyGateTask(name, path, extra string) string {
	return "  - name: " + name + "\n" + extra +
		"    fqcn: file.directory\n" +
		"    params:\n" +
		"      target: container1\n" +
		"      path: " + path + "\n"
}

// journalText is every journal file the project holds, concatenated, and
// how many files there are.
func journalText(t *testing.T, dir string) (string, int) {
	t.Helper()
	root := filepath.Join(dir, ".pleiades", "journal")
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", 0
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String(), len(entries)
}

// TestCLI_CheckModeKeyAgainstARealDevice is the Release Gate for the
// runbook check_mode key, through the real binary against a real sshd. A
// task carrying check_mode is only checked inside a real run while the
// task beside it really runs, as the device itself shows; only the real
// one reaches the journal; a runbook-level key makes the whole run a
// check; a command guarded by creates is checked rather than run; and
// validation refuses the uses that would break a check's promise,
// including an unguarded command.
func TestCLI_CheckModeKeyAgainstARealDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the check_mode key Release Gate container test in short mode")
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
			t.Fatalf("%s failed: %v\n%s", args[0], err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "runbooks", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// A real run with one task only checked, and a command whose creates
	// guard makes it checkable only checked too.
	write("mixed.yaml", "id: check-mode-key-gate\ntasks:\n"+
		keyGateTask("only-checked", keyGateChecked, "    check_mode: true\n")+
		keyGateTask("really-run", keyGateRun, "")+
		"  - name: guarded-command\n    check_mode: true\n    fqcn: exec.command\n    params:\n      target: container1\n"+
		"      cmd: touch "+keyGateCommand+"\n      creates: "+keyGateCommand+"\n")
	out, err := runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/mixed.yaml")
	if err != nil {
		t.Fatalf("the mixed run failed: %v\n%s", err, out)
	}
	if l := nodeLine(t, out, "tasks[0]"); !strings.HasSuffix(l, "would change") {
		t.Errorf("the check_mode task should report a prediction, got %q", l)
	}
	if l := nodeLine(t, out, "tasks[1]"); !strings.HasSuffix(l, ": changed") {
		t.Errorf("the other task should really change the device, got %q", l)
	}
	if l := nodeLine(t, out, "tasks[2]"); !strings.HasSuffix(l, "would change") {
		t.Errorf("the guarded command should report that it would run, got %q", l)
	}
	if existsOnDevice(t, addr, keyGateChecked) {
		t.Fatal("the check_mode task created its directory on the device")
	}
	if existsOnDevice(t, addr, keyGateCommand) {
		t.Fatal("the checked command ran on the device")
	}
	if !existsOnDevice(t, addr, keyGateRun) {
		t.Fatalf("the real task did not create its directory, so the assertion above proves nothing:\n%s", out)
	}
	journal, files := journalText(t, dir)
	if !strings.Contains(journal, `"tasks[1]"`) {
		t.Errorf("the real task is not in the journal:\n%s", journal)
	}
	if strings.Contains(journal, `"tasks[0]"`) || strings.Contains(journal, `"tasks[2]"`) {
		t.Errorf("a checked task was journaled:\n%s", journal)
	}

	// A runbook-level key: run without --mode, and nothing changes.
	write("whole.yaml", "id: check-mode-key-whole\ncheck_mode: true\ntasks:\n"+keyGateTask("whole", keyGateWhole, ""))
	out, err = runPleiadesWithHome(t, dir, homeDir, "run", "runbooks/whole.yaml")
	if err != nil {
		t.Fatalf("the runbook-level check failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "check complete: nothing was changed") {
		t.Errorf("a runbook-level check_mode did not run as a check:\n%s", out)
	}
	if existsOnDevice(t, addr, keyGateWhole) {
		t.Fatal("a runbook-level check created its directory on the device")
	}
	if _, after := journalText(t, dir); after != files {
		t.Errorf("a runbook-level check wrote a journal file (%d before, %d after)", files, after)
	}

	// The refusals, through the real validate command.
	for name, tc := range map[string]struct{ body, want string }{
		"false.yaml": {
			body: "id: false-key\ntasks:\n" + keyGateTask("a", keyGateRoot+"/false", "    check_mode: false\n"),
			want: "check_mode: false is refused",
		},
		"uncheckable.yaml": {
			body: "id: uncheckable\ntasks:\n  - name: cmd\n    check_mode: true\n    fqcn: exec.command\n    params:\n      target: container1\n      cmd: /bin/true\n",
			want: "exec.command cannot check this call",
		},
		"prediction.yaml": {
			body: "id: prediction\ntasks:\n" + keyGateTask("probe", keyGateRoot+"/probe", "    check_mode: true\n    register: probe\n") +
				"  - name: act\n    when: stat.probe[\"\"].changed\n    fqcn: noop\n",
			want: "only checked",
		},
		"unknown.yaml": {
			body: "id: unknown\nchek_mode: true\ntasks:\n" + keyGateTask("a", keyGateRoot+"/unknown", ""),
			want: `did you mean "check_mode"`,
		},
	} {
		write(name, tc.body)
		out, err := runPleiadesWithHome(t, dir, homeDir, "validate", "runbooks/"+name)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("validate %s = %v, want a refusal containing %q:\n%s", name, err, tc.want, out)
		}
	}
}
