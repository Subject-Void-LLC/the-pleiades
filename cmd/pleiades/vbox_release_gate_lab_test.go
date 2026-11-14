// Phase 112's Release Gate against a real VirtualBox host, through the real
// binary: a base imported from a generalized Windows disk image, a linked
// clone of it proven by its own login, the guest reached, a snapshot taken
// and restored, and then each run's recorded undos replayed until nothing
// the gate made is left.
//
// The guest is reached over WinRM, not SSH: the one importable image on the
// lab host is Microsoft's Windows Server evaluation VHDX, and the Linux
// path (clone, start, host keys, SSH, rollback) is TestLab_FreeBSDVMRollback.
// It skips, saying what to set, where there is no such host.
//
// Before 2026-11-13 the snapshot run's undo deleted the clone's snapshot
// while the clone ran, and on the lab host that twice left the clone
// inaccessible, its settings holding no media registry. Now
// virt.vbox.snapshot.delete refuses a running VM, a snapshot of a running
// VM records no undo, and this gate holds the rollback to refusing that
// change by name before contact.
package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winunattend"
)

// envVBoxWindowsImage names a generalized Windows disk image on the host,
// such as Microsoft's Windows Server evaluation VHDX.
const envVBoxWindowsImage = "PLEIADES_VBOX_WINDOWS_IMAGE"

// The gate's own VMs and the device that reaches its clone, which nothing
// else uses; its address is kept free of the lab's.
const (
	gateBase    = "vbgate-base"
	gateClone   = "vbgate"
	gateAddress = "192.168.56.63"
)

func TestLab_VBoxReleaseGate(t *testing.T) {
	dir, host, image := os.Getenv(envVBoxProject), os.Getenv(envVBoxHost), os.Getenv(envVBoxWindowsImage)
	if dir == "" || host == "" || image == "" {
		t.Skipf("this release gate needs a VirtualBox host holding a generalized Windows disk image, and %s free: set %s to the project that manages it, %s to its inventory name and %s to the image's path on the host",
			gateAddress, envVBoxProject, envVBoxHost, envVBoxWindowsImage)
	}
	before := labVMs(t, dir, host)
	for _, vm := range []string{gateBase, gateClone} {
		if _, left := before[vm]; left {
			t.Fatalf("%s already exists on %s, left by an earlier run of this gate; delete it and run again", vm, host)
		}
	}
	password, err := winunattend.NewPassword()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiades(t, dir, "add-host", gateClone, "--type", "windows_server", "--set", "host="+gateAddress, "--tags", "vbgate"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := runPleiades(t, dir, "remove-host", gateClone, "--credential"); err != nil {
			t.Errorf("remove-host: %v\n%s", err, out)
		}
	})
	if out, err := runPleiadesWithStdin(t, dir, password+"\n", "add-credential", gateClone, "--username", "Administrator", "--password-stdin"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	// Whatever happens, nothing the gate made outlives it.
	t.Cleanup(func() {
		deleteLabVM(t, dir, host, gateClone)
		deleteLabVM(t, dir, host, gateBase)
	})

	header := func(id, what, changes string) string {
		return fmt.Sprintf("# Phase 112 release gate: %s.\n# Blast radius: %s, the VirtualBox host; the gate's own VMs.\n# Changes: through %s.\n# Undo: the gate rolls this run back.\nid: %s\nhosts: %s\ntasks:\n", what, host, changes, id, host)
	}
	makeRun := header("vbox-release-gate-make", "a base from the image, its snapshot, a clone proven by its login, booted",
		"virt.vbox.vm.import_disk, virt.vbox.snapshot.take, virt.vbox.vm.clone and virt.vbox.vm.start") +
		fmt.Sprintf(`  - name: import the image as a base
    virt.vbox.vm.import_disk:
      name: %[1]s
      path: %[2]s
      os_type: Windows2025_64
  - name: snapshot the base, never booted
    virt.vbox.snapshot.take:
      vm: %[1]s
      name: base
  - name: clone it, proven by its own Administrator's login
    virt.vbox.vm.clone:
      name: %[3]s
      from: %[1]s
      snapshot: base
      login: %[3]s
      address: %[4]s/24
      memory_mb: 1536
      cpus: 1
  - name: boot the clone
    virt.vbox.vm.start:
      name: %[3]s
`, gateBase, image, gateClone, gateAddress)
	first := runGateRunbook(t, dir, "make", makeRun)

	// The guest, reached as its own Administrator.
	if _, raw, code := runPleiadesJSON(t, dir, "adhoc", gateClone, "wait.connection", "timeout=900", "sleep=10"); code != 0 {
		t.Fatalf("reaching the clone exited %d:\n%s", code, raw)
	}
	reached, raw, code := runPleiadesJSON(t, dir, "adhoc", gateClone, "exec.winrm.shell", "shell=cmd", "command=hostname")
	if got, _ := reached.Tasks[0].Stats["stdout"].(string); code != 0 || !strings.EqualFold(strings.TrimSpace(got), gateClone) {
		t.Fatalf("the clone answered %q (exit %d):\n%s", got, code, raw)
	}

	change := header("vbox-release-gate-snapshot", "a snapshot of the clone, taken and restored", "virt.vbox.snapshot.take, virt.vbox.vm.stop and virt.vbox.snapshot.restore") +
		fmt.Sprintf(`  - name: snapshot the running clone
    virt.vbox.snapshot.take:
      vm: %[1]s
      name: gate
  - name: shut it down
    virt.vbox.vm.stop:
      name: %[1]s
      mode: acpi
  - name: restore the snapshot
    virt.vbox.snapshot.restore:
      vm: %[1]s
      name: gate
`, gateClone)
	second := runGateRunbook(t, dir, "snapshot", change)

	// Newest run first. The snapshot of the running clone recorded no undo:
	// deleting it would mean deleting a running VM's snapshot, which
	// virt.vbox.snapshot.delete refuses, so the rollback refuses that change
	// by name before it contacts anything.
	before2 := labVMs(t, dir, host)
	_, raw, code = runPleiadesJSON(t, dir, "rollback", second, "--leave", "tasks[2]")
	if code == 0 || !strings.Contains(raw, "recorded no undo") || !strings.Contains(raw, "--leave tasks[0]") {
		t.Fatalf("a rollback of the snapshot run that leaves only the restore exited %d; want a refusal naming tasks[0]:\n%s", code, raw)
	}
	if now := labVMs(t, dir, host); now[gateClone] != before2[gateClone] {
		t.Fatalf("the refused rollback changed the clone from %v to %v", before2[gateClone], now[gateClone])
	}
	// The restore and the snapshot of the running clone are left by name;
	// the stop is undone, starting the clone again. The clone was stopped
	// before the restore, so no step deletes a running VM's snapshot.
	if _, raw, code := runPleiadesJSON(t, dir, "rollback", second, "--leave", "tasks[0]", "--leave", "tasks[2]"); code != 0 {
		t.Fatalf("rolling back the snapshot run exited %d:\n%s", code, raw)
	}
	// The restore left in place is still a change of the later run, so the
	// earlier run is undone beneath it by name.
	if _, raw, code := runPleiadesJSON(t, dir, "rollback", first, "--despite-run", second); code != 0 {
		t.Fatalf("rolling back the make run exited %d:\n%s", code, raw)
	}
	after := labVMs(t, dir, host)
	for _, vm := range []string{gateBase, gateClone} {
		if _, left := after[vm]; left {
			t.Errorf("%s is still on %s after both rollbacks", vm, host)
		}
	}
	for name, was := range before {
		if now, ok := after[name]; !ok || now != was {
			t.Errorf("%s was %v before the gate and is %v (present %v) after", name, was, now, ok)
		}
	}
	// Undone in full, the first run is not undone twice.
	if _, raw, code := runPleiadesJSON(t, dir, "rollback", first, "--despite-run", second); code != 1 || !strings.Contains(raw, "already been undone") {
		t.Errorf("a second rollback of the make run: exit %d\n%s", code, raw)
	}
}

// runGateRunbook writes body as a runbook, runs it, and returns its run ID,
// failing the test unless every task succeeded.
func runGateRunbook(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, raw, code := runPleiadesJSON(t, dir, "run", path)
	if code != 0 || rep.RunID == "" {
		t.Fatalf("the %s run exited %d:\n%s", name, code, raw)
	}
	return rep.RunID
}
