// The lab half of Phase 112's Windows clones: two linked clones of one
// generalized Windows base, each seeded with its own Administrator
// password, made on a real VirtualBox host through the real binary. Each
// clone must let in its own password and refuse the other's.
//
// It runs in the project that manages the lab's VirtualBox host, as
// TestLab_FreeBSDVMRollback does, and skips, saying what to set, where
// there is none. The clones run one at a time, since the lab host has room
// for one Windows guest.
package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winunattend"
)

// envVBoxWindowsBase names a generalized Windows VM on the host with a
// snapshot named base, such as one virt.vbox.vm.import_disk made from
// Microsoft's evaluation VHDX.
const envVBoxWindowsBase = "PLEIADES_VBOX_WINDOWS_BASE"

// windowsGateClone is one of the gate's clones: its VM and inventory name
// (also its computer name), its fixed address, and the inventory device
// that reaches its address with the other clone's password.
type windowsGateClone struct {
	name, address, impostor string
	password                string
}

func TestLab_WindowsClonesTakeTheirOwnAnswerFiles(t *testing.T) {
	dir, host, base := os.Getenv(envVBoxProject), os.Getenv(envVBoxHost), os.Getenv(envVBoxWindowsBase)
	if dir == "" || host == "" || base == "" {
		t.Skipf("this lab gate needs a VirtualBox host holding a generalized Windows base with a snapshot named base, and 192.168.56.61 and .62 free: set %s to the project that manages it, %s to its inventory name and %s to the base",
			envVBoxProject, envVBoxHost, envVBoxWindowsBase)
	}
	clones := []*windowsGateClone{
		{name: "wingate-a", address: "192.168.56.61", impostor: "wingate-a-wrong"},
		{name: "wingate-b", address: "192.168.56.62", impostor: "wingate-b-wrong"},
	}
	for _, c := range clones {
		p, err := winunattend.NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		c.password = p
	}
	vms := labVMs(t, dir, host)
	for _, c := range clones {
		if _, left := vms[c.name]; left {
			t.Fatalf("%s already exists on %s, left by an earlier run of this gate; delete it (virt.vbox.vm.stop, then virt.vbox.vm.delete) and run again", c.name, host)
		}
	}

	// Each clone's device holds its own password; its impostor reaches the
	// same address with the other clone's.
	for i, c := range clones {
		other := clones[1-i]
		for _, d := range []struct{ device, password string }{{c.name, c.password}, {c.impostor, other.password}} {
			if out, err := runPleiades(t, dir, "add-host", d.device, "--type", "windows_server", "--set", "host="+c.address, "--tags", "wingate"); err != nil {
				t.Fatalf("add-host %s: %v\n%s", d.device, err, out)
			}
			device := d.device
			t.Cleanup(func() {
				if out, err := runPleiades(t, dir, "remove-host", device, "--credential"); err != nil {
					t.Errorf("remove-host %s: %v\n%s", device, err, out)
				}
			})
			if out, err := runPleiadesWithStdin(t, dir, d.password+"\n", "add-credential", d.device, "--username", "Administrator", "--password-stdin"); err != nil {
				t.Fatalf("add-credential %s: %v\n%s", d.device, err, out)
			}
		}
	}

	for _, c := range clones {
		name := c.name
		t.Cleanup(func() { deleteLabVM(t, dir, host, name) })
		runbook := filepath.Join(t.TempDir(), name+".yaml")
		if err := os.WriteFile(runbook, []byte(fmt.Sprintf(`# Makes the gate's Windows clone %[1]s, proven by its own login, and boots it.
# Blast radius: %[2]s, the VirtualBox host; %[1]s, the gate's own VM.
# Changes: through virt.vbox.vm.clone and virt.vbox.vm.start.
# Undo: the gate deletes the VM; pleiades rollback of this run does too.
id: lab-windows-clone-gate
hosts: %[2]s
tasks:
  - name: make the clone, proven to let in its own Administrator
    virt.vbox.vm.clone:
      name: %[1]s
      from: %[3]s
      snapshot: base
      login: %[1]s
      address: %[4]s/24
      memory_mb: 1536
      cpus: 1
  - name: boot it for use
    virt.vbox.vm.start:
      name: %[1]s
`, name, host, base, c.address)), 0o600); err != nil {
			t.Fatal(err)
		}
		if rep, raw, code := runPleiadesJSON(t, dir, "run", runbook); code != 0 || len(rep.Tasks) != 2 {
			t.Fatalf("making %s exited %d:\n%s", name, code, raw)
		}

		// Its own password: in, under its own computer name.
		wait, raw, code := runPleiadesJSON(t, dir, "adhoc", name, "wait.connection", "timeout=900", "sleep=10")
		if code != 0 || len(wait.Tasks) != 1 {
			t.Fatalf("%s's own login after its second boot exited %d:\n%s", name, code, raw)
		}
		own, raw, code := runPleiadesJSON(t, dir, "adhoc", name, "exec.winrm.shell", "shell=cmd", "command=hostname")
		if code != 0 || len(own.Tasks) != 1 {
			t.Fatalf("%s's own login exited %d:\n%s", name, code, raw)
		}
		if got, _ := own.Tasks[0].Stats["stdout"].(string); !strings.EqualFold(strings.TrimSpace(got), name) {
			t.Errorf("%s names itself %q", name, got)
		}

		// The other clone's password: refused.
		wrong, raw, code := runPleiadesJSON(t, dir, "adhoc", c.impostor, "exec.winrm.shell", "shell=cmd", "command=hostname")
		if code == 0 || len(wrong.Tasks) != 1 || wrong.Tasks[0].Status == "ok" || wrong.Tasks[0].Status == "changed" {
			t.Errorf("%s let in the other clone's password (exit %d):\n%s", name, code, raw)
		} else if msg := strings.ToLower(wrong.Tasks[0].Error); !strings.Contains(msg, "401") && !strings.Contains(msg, "auth") {
			t.Errorf("%s refused the other clone's password for another reason than its credential: %s", name, wrong.Tasks[0].Error)
		}

		// One Windows guest at a time: this one is deleted before the next.
		deleteLabVM(t, dir, host, name)
	}
}

// labVMs reads the host's VMs by name, with their state and UUID, through
// virt.vbox.vm.list.
func labVMs(t *testing.T, dir, host string) map[string][2]string {
	t.Helper()
	rep, raw, code := runPleiadesJSON(t, dir, "adhoc", host, "virt.vbox.vm.list")
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("listing %s's VMs (exit %d):\n%s", host, code, raw)
	}
	out := map[string][2]string{}
	list, _ := rep.Tasks[0].Stats["vms"].([]any)
	for _, v := range list {
		vm, _ := v.(map[string]any)
		name, _ := vm["name"].(string)
		state, _ := vm["state"].(string)
		uuid, _ := vm["uuid"].(string)
		out[name] = [2]string{state, uuid}
	}
	return out
}

// deleteLabVM shuts name down through its power button, cutting the power
// if that fails, and deletes it; a VM already gone is no error.
func deleteLabVM(t *testing.T, dir, host, name string) {
	t.Helper()
	if _, there := labVMs(t, dir, host)[name]; !there {
		return
	}
	if _, raw, code := runPleiadesJSON(t, dir, "adhoc", host, "virt.vbox.vm.stop", "name="+name, "mode=acpi", "timeout=300"); code != 0 {
		if _, raw2, code := runPleiadesJSON(t, dir, "adhoc", host, "virt.vbox.vm.stop", "name="+name, "mode=poweroff"); code != 0 {
			t.Errorf("stopping %s:\n%s\n%s", name, raw, raw2)
		}
	}
	if _, raw, code := runPleiadesJSON(t, dir, "adhoc", host, "virt.vbox.vm.delete", "name="+name); code != 0 {
		t.Errorf("deleting %s exited %d:\n%s", name, code, raw)
	}
}
