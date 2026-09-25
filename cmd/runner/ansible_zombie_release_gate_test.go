// Release gate for the Ansible adapter's init: a real playbook of many
// tasks, run by the real adapter against a real sshd, must leave no
// zombie process in its container (FAILURE_PATTERNS 343).
package main_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// zombieCheckTask is the play's last task, run in the adapter's own
// container: it fails when any zombie is left there. It waits a second
// first, so a process between exiting and being reaped is not counted;
// an orphan nothing reaps is still there.
const zombieCheckTask = "no zombie is left in the control container"

// zombiePlaybook runs tasks raw commands on the target, then the check
// on the control container.
func zombiePlaybook(tasks int) string {
	var b strings.Builder
	b.WriteString("---\n- hosts: all\n  gather_facts: false\n  tasks:\n")
	for i := 1; i <= tasks; i++ {
		fmt.Fprintf(&b, "    - name: remote command %d\n      raw: echo %d\n      changed_when: false\n", i, i)
	}
	b.WriteString("- hosts: localhost\n  connection: local\n  gather_facts: false\n  tasks:\n")
	fmt.Fprintf(&b, "    - name: %s\n      raw: sleep 1; z=$(grep -l '^State:.Z' /proc/[0-9]*/status 2>/dev/null | wc -l); echo zombies=$z; test \"$z\" -eq 0\n      changed_when: false\n", zombieCheckTask)
	return b.String()
}

// TestAnsibleReleaseGate_LeavesNoZombies runs twenty tasks through the
// real adapter and its real Docker orchestrator against a real sshd, and
// requires the job to succeed, which it does only when the check task
// finds no zombie in the container ansible-playbook ran in. Run with
// ansible-playbook as PID 1 the same playbook leaves about one zombie per
// task, and the check fails the job.
func TestAnsibleReleaseGate_LeavesNoZombies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newAnsibleReleaseGateHarness(t, zombiePlaybook(20))
	events := h.dispatch(t, wire.DispatchPayload{
		JobID:        uuid.New().String(),
		RunbookID:    "upgrade.yml",
		DeviceID:     "release-gate-device",
		DeviceName:   "sw1",
		DeviceHost:   ansibleGateNetworkAlias,
		SSHPort:      2222,
		Capabilities: []capability.Name{capability.NameCiscoIOS},
		Secrets:      credential.Flatten(credential.Credential{Username: ansibleGateSSHUser, Password: ansibleGateSSHPassword}),
	})
	final := events[len(events)-1]
	if final.Status != "ok" {
		t.Fatalf("final status = %q, want ok: the zombie check or a remote command failed: events=%+v", final.Status, events)
	}
	ran := false
	for _, evt := range events {
		ran = ran || evt.Task == zombieCheckTask
	}
	if !ran {
		t.Fatalf("no event for %q, so nothing was checked: events=%+v", zombieCheckTask, events)
	}
}
