// The lab half of Phase 40's rollback: a FreeBSD VM made on a real
// VirtualBox host, by a run that then fails on a step that only reads, and
// rolled back from that run's journal, through the real binary.
//
// It runs in the project that manages the lab's VirtualBox host, as a user
// runs the binary there, so the host's credential comes from the project's
// own vault. It skips, saying what to set, where there is no such host.
// The VM's state is read back with virt.vbox.vm.list, a different method
// from the clone, start, stop and delete under test, as the BSD gate reads
// a mode back with exec.shell.
package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLEIADES_VBOX_PROJECT and PLEIADES_VBOX_HOST name a project that manages
// a VirtualBox host holding the lab's freebsd-base (with its base
// snapshot) and a bsd-lab login, and the host's inventory name.
const (
	envVBoxProject = "PLEIADES_VBOX_PROJECT"
	envVBoxHost    = "PLEIADES_VBOX_HOST"
)

// labRollbackVM is the gate's own VM, which nothing else uses.
const labRollbackVM = "bsd-rollback-gate"

func TestLab_FreeBSDVMRollback(t *testing.T) {
	dir, host := os.Getenv(envVBoxProject), os.Getenv(envVBoxHost)
	if dir == "" || host == "" {
		t.Skipf("this lab gate needs a VirtualBox host holding freebsd-base (snapshot base) and a bsd-lab login: set %s to the project that manages it and %s to its inventory name "+
			"(examples/virtualbox_lab's freebsd runbooks make both)", envVBoxProject, envVBoxHost)
	}
	// vms reads the host's VMs by name, with their state and UUID.
	vms := func() map[string][2]string {
		t.Helper()
		rep, raw, code := runPleiadesJSONWithHome(t, dir, "", "adhoc", host, "virt.vbox.vm.list")
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
	if _, left := vms()[labRollbackVM]; left {
		t.Fatalf("%s already exists on %s, left by an earlier run of this gate; delete it (virt.vbox.vm.stop, then virt.vbox.vm.delete) and run again", labRollbackVM, host)
	}

	runbook := filepath.Join(t.TempDir(), "lab-rollback-gate.yaml")
	if err := os.WriteFile(runbook, []byte(`id: lab-freebsd-rollback-gate
hosts: `+host+`
tasks:
  - name: make a FreeBSD VM
    virt.vbox.vm.clone:
      name: `+labRollbackVM+`
      from: freebsd-base
      snapshot: base
      login: bsd-lab
      address: 192.168.56.42/24
      size: small
  - name: start it
    virt.vbox.vm.start:
      name: `+labRollbackVM+`
  - name: wait for its first boot to print its SSH host keys
    virt.vbox.vm.host_keys:
      name: `+labRollbackVM+`
      timeout: 600
  - name: read a VM that is not there, which fails and changes nothing
    virt.vbox.vm.host_keys:
      name: `+labRollbackVM+`-absent
      timeout: 5
`), 0o600); err != nil {
		t.Fatal(err)
	}

	run, raw, code := runPleiadesJSONWithHome(t, dir, "", "run", runbook)
	made, exists := vms()[labRollbackVM]
	if code != 1 || run.RunID == "" || !exists || made[0] != "running" {
		t.Fatalf("the run exited %d with run id %q, and left the VM %v (exists %v); want it failed on its last step with the VM running:\n%s", code, run.RunID, made, exists, raw)
	}

	rollback, raw, code := runPleiadesJSONWithHome(t, dir, "", "rollback", run.RunID)
	if code != 0 {
		t.Fatalf("the rollback exited %d:\n%s", code, raw)
	}
	var plan struct {
		Levels [][]struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		} `json:"levels"`
	}
	if err := json.Unmarshal(rollback.Rollback, &plan); err != nil {
		t.Fatal(err)
	}
	var steps []string
	for _, level := range plan.Levels {
		for _, s := range level {
			steps = append(steps, s.Method)
		}
	}
	if got := strings.Join(steps, ","); got != "virt.vbox.vm.stop,virt.vbox.vm.delete" {
		t.Errorf("the rollback ran %s, want the stop and then the delete: the reads that failed and succeeded have nothing to undo", got)
	}
	if uuid := plan.Levels[len(plan.Levels)-1][0].Params["uuid"]; uuid != made[1] {
		t.Errorf("the delete was pinned to %v, want the VM the run made (%s)", uuid, made[1])
	}
	if _, still := vms()[labRollbackVM]; still {
		t.Errorf("%s is still on %s after the rollback", labRollbackVM, host)
	}

	// Undone in full, it is not undone twice.
	if _, raw, code := runPleiadesJSONWithHome(t, dir, "", "rollback", run.RunID); code != 1 || !strings.Contains(raw, "already been undone") {
		t.Errorf("a second rollback: exit %d\n%s", code, raw)
	}
}
