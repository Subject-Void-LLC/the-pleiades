// The lab half of Phase 40's rollback, and the SSH half of Phase 112's
// Release Gate: a FreeBSD VM made on a real VirtualBox host by a run that
// then fails on a step that only reads, its guest reached over SSH as the
// login the clone seeded, at the host keys its own console printed, and the
// run rolled back from its journal, all through the real binary.
//
// It runs in the project that manages the lab's VirtualBox host, as a user
// runs the binary there, so the host's credential comes from the project's
// own vault. The gate adds its own device and generated login there for the
// length of the run, and removes both after. It skips, saying what to set,
// where there is no such host. The VM's state is read back with
// virt.vbox.vm.list, a different method from the clone, start, stop and
// delete under test, as the BSD gate reads a mode back with exec.shell.
//
// Until 2026-11-13 the run's failing step was virt.vbox.vm.host_keys on a VM
// that is not there, and nothing reached the guest over SSH. Since Phase 131
// (2026-10-28) that read answers exists: false and succeeds, so the run
// passed, the gate failed, and with no cleanup it left its VM running.
package main_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// PLEIADES_VBOX_PROJECT and PLEIADES_VBOX_HOST name a project that manages
// a VirtualBox host holding the lab's freebsd-base (with its base
// snapshot), and the host's inventory name.
const (
	envVBoxProject = "PLEIADES_VBOX_PROJECT"
	envVBoxHost    = "PLEIADES_VBOX_HOST"
)

// The gate's own VM, which is also the name of the device that reaches its
// guest, and that device's address, which nothing else in the lab uses
// (bsd-check holds .42, the address this gate cloned at before).
const (
	labRollbackVM      = "bsd-rollback-gate"
	labRollbackAddress = "192.168.56.44"
)

func TestLab_FreeBSDVMRollback(t *testing.T) {
	dir, host := os.Getenv(envVBoxProject), os.Getenv(envVBoxHost)
	if dir == "" || host == "" {
		t.Skipf("this lab gate needs a VirtualBox host holding freebsd-base (snapshot base), with %s free: set %s to the project that manages it and %s to its inventory name "+
			"(examples/virtualbox_lab's freebsd runbooks make the base)", labRollbackAddress, envVBoxProject, envVBoxHost)
	}
	before := labVMs(t, dir, host)
	if _, left := before[labRollbackVM]; left {
		t.Fatalf("%s already exists on %s, left by an earlier run of this gate; delete it (virt.vbox.vm.stop, then virt.vbox.vm.delete) and run again", labRollbackVM, host)
	}
	if holders := runningAt(t, dir, host, labRollbackAddress); len(holders) > 0 {
		t.Fatalf("%s is the gate's address, and %v running on %s records it", labRollbackAddress, holders, host)
	}

	// The device that reaches the guest, with a login made for this run, which
	// the clone seeds. Both go when the gate ends.
	if out, err := runPleiades(t, dir, "add-host", labRollbackVM, "--type", "generic_ssh", "--set", "host="+labRollbackAddress, "--tags", "bsdgate"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := runPleiades(t, dir, "remove-host", labRollbackVM, "--credential"); err != nil {
			t.Errorf("remove-host: %v\n%s", err, out)
		}
	})
	if out, err := runPleiades(t, dir, "add-credential", labRollbackVM, "--username", "pleiades", "--generate"); err != nil {
		t.Fatalf("add-credential: %v\n%s", err, out)
	}
	// Whatever happens, the VM does not outlive the gate: shut down by its
	// power button (its power cut only if that fails) and deleted. After a
	// rollback that worked, it is already gone.
	t.Cleanup(func() { deleteLabVM(t, dir, host, labRollbackVM) })

	runbook := filepath.Join(t.TempDir(), "lab-rollback-gate.yaml")
	if err := os.WriteFile(runbook, []byte(`# Phase 112 lab gate: a FreeBSD VM cloned and booted, then a step that only reads fails.
# Blast radius: `+host+`, the VirtualBox host; the gate's own VM.
# Changes: through virt.vbox.vm.clone and virt.vbox.vm.start.
# Undo: the gate rolls this run back.
id: lab-freebsd-rollback-gate
hosts: `+host+`
tasks:
  - name: make a FreeBSD VM
    virt.vbox.vm.clone:
      name: `+labRollbackVM+`
      from: freebsd-base
      snapshot: base
      login: `+labRollbackVM+`
      address: `+labRollbackAddress+`/24
      size: small
  - name: start it
    virt.vbox.vm.start:
      name: `+labRollbackVM+`
  - name: wait for its first boot to print its SSH host keys
    virt.vbox.vm.host_keys:
      name: `+labRollbackVM+`
      timeout: 600
  - name: read its log with a pattern that does not compile, which fails and changes nothing
    virt.vbox.vm.log:
      name: `+labRollbackVM+`
      pattern: "("
`), 0o600); err != nil {
		t.Fatal(err)
	}

	run, raw, code := runPleiadesJSON(t, dir, "run", runbook)
	made, exists := labVMs(t, dir, host)[labRollbackVM]
	if code != 1 || run.RunID == "" || len(run.Tasks) != 4 || !exists || made[0] != "running" {
		t.Fatalf("the run exited %d with run id %q, and left the VM %v (exists %v); want it failed on its last step with the VM running:\n%s", code, run.RunID, made, exists, raw)
	}
	if last := run.Tasks[len(run.Tasks)-1]; last.Status != "failed" || !strings.Contains(last.Error, "pattern") {
		t.Fatalf("the run's last task ended %s (%s); want it failed on its pattern", last.Status, last.Error)
	}

	reachOverSSH(t, dir, host)

	rollback, raw, code := runPleiadesJSON(t, dir, "rollback", run.RunID)
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
	after := labVMs(t, dir, host)
	if _, still := after[labRollbackVM]; still {
		t.Errorf("%s is still on %s after the rollback", labRollbackVM, host)
	}
	for name, was := range before {
		if now, ok := after[name]; !ok || now != was {
			t.Errorf("%s was %v before the gate and is %v (present %v) after", name, was, now, ok)
		}
	}

	// Undone in full, it is not undone twice.
	if _, raw, code := runPleiadesJSON(t, dir, "rollback", run.RunID); code != 1 || !strings.Contains(raw, "already been undone") {
		t.Errorf("a second rollback: exit %d\n%s", code, raw)
	}
}

// reachOverSSH trusts the gate VM's host keys from its console, as a user
// does (trust-host --from-console), onboards it over SSH as the login the
// clone seeded, and has it answer net.ssh.ping as that login. A known_hosts
// of the gate's own holds the keys, so nothing is written to the person's,
// and the same ping against an empty one is refused on the host key: the
// SSH that reached the guest verified the keys its console printed.
func reachOverSSH(t *testing.T, dir, host string) {
	t.Helper()
	known := filepath.Join(t.TempDir(), "known_hosts")
	env := map[string]string{remoteexec.KnownHostsEnv: known}
	if out, err := runPleiadesWithEnv(t, dir, env, "trust-host", labRollbackVM, "--from-console", host); err != nil {
		t.Fatalf("trust-host --from-console: %v\n%s", err, out)
	}
	if lines, err := os.ReadFile(known); err != nil || !strings.Contains(string(lines), labRollbackAddress) {
		t.Fatalf("trust-host wrote no key for %s (%v):\n%s", labRollbackAddress, err, lines)
	}
	out, err := runPleiadesWithEnv(t, dir, env, "onboard", labRollbackVM, "--json")
	if err != nil {
		t.Fatalf("onboarding the guest over SSH: %v\n%s", err, out)
	}
	var onboarded struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(out), &onboarded); err != nil || onboarded.State != "active" || !strings.Contains(out, "FreeBSD") {
		t.Fatalf("onboarding left the guest %q (%v), or it did not report a FreeBSD kernel:\n%s", onboarded.State, err, out)
	}

	ping, raw, code := runPleiadesJSON(t, dir, "--known-hosts", known, "adhoc", labRollbackVM, "net.ssh.ping")
	if reply, _ := ping.Tasks[0].Stats["reply"].(string); code != 0 || reply != "pong" {
		t.Fatalf("the guest answered %q over SSH (exit %d), want pong:\n%s", reply, code, raw)
	}

	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	refused, raw, code := runPleiadesJSON(t, dir, "--known-hosts", empty, "adhoc", labRollbackVM, "net.ssh.ping")
	if code == 0 || len(refused.Tasks) != 1 || !strings.Contains(strings.ToLower(refused.Tasks[0].Error), "key") {
		t.Fatalf("with no key trusted the guest still answered, or was refused for another reason (exit %d):\n%s", code, raw)
	}
}

// runningAt returns the running VMs on host whose recorded address is
// address, as virt.vbox.vm.list reports them.
func runningAt(t *testing.T, dir, host, address string) []string {
	t.Helper()
	rep, raw, code := runPleiadesJSON(t, dir, "adhoc", host, "virt.vbox.vm.list")
	if code != 0 || len(rep.Tasks) != 1 {
		t.Fatalf("listing %s's VMs (exit %d):\n%s", host, code, raw)
	}
	var holders []string
	list, _ := rep.Tasks[0].Stats["vms"].([]any)
	for _, v := range list {
		vm, _ := v.(map[string]any)
		if vm["address"] == address && vm["state"] == "running" {
			holders = append(holders, fmt.Sprint(vm["name"]))
		}
	}
	return holders
}
