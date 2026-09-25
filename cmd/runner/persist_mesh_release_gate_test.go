// Release gate for connection persistence on the Walk tier: a real
// dispatch over real NATS, through the real Agent and the per-dispatch
// session child, against a real sshd whose own log counts the logins.
package main_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// meshSSHDLog is where the pinned openssh-server image's sshd logs.
const meshSSHDLog = "/config/logs/openssh/current"

// meshLogins counts the password logins the device's sshd has logged.
func (h *releaseGateHarness) meshLogins(t *testing.T) int {
	t.Helper()
	code, r, err := h.sshd.Exec(context.Background(), []string{"cat", meshSSHDLog})
	if err != nil || code != 0 {
		t.Fatalf("reading %s: exit %d, %v", meshSSHDLog, code, err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(out), "Accepted password for "+releaseGateSSHUser)
}

// settledLogins waits for the log to stop growing and returns its count,
// so a late line is counted rather than missed.
func (h *releaseGateHarness) settledLogins(t *testing.T) int {
	t.Helper()
	last, still := -1, 0
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && still < 3; {
		time.Sleep(200 * time.Millisecond)
		n := h.meshLogins(t)
		if n == last {
			still++
		} else {
			last, still = n, 0
		}
	}
	return last
}

// TestSSHMeshReleaseGate_PersistentConnectionLogsInOnce dispatches a
// runbook of ten exec.shell tasks twice: with PersistConnections the
// device logs one login, without it ten. Each task's write is then read
// back on the device, so fewer logins by doing less would fail.
func TestSSHMeshReleaseGate_PersistentConnectionLogsInOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	var rb strings.Builder
	rb.WriteString("id: shell10\ntasks:\n")
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(&rb, "  - name: append %d\n    fqcn: exec.shell\n    params:\n      cmd: echo %d >> /tmp/mesh-persist\n", i, i)
	}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{"shell10.yaml": rb.String()})

	for _, tc := range []struct {
		persist bool
		want    int
	}{{true, 1}, {false, 10}} {
		before := h.settledLogins(t)
		final := h.dispatch(t, wire.DispatchPayload{
			JobID:              uuid.New().String(),
			RunbookID:          "shell10",
			DeviceID:           "release-gate-device",
			DeviceName:         "release-gate-device",
			DeviceHost:         h.sshHost,
			SSHPort:            h.sshPort,
			Capabilities:       []capability.Name{capability.NameSSHTransport, capability.NameShellExec},
			Secrets:            credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
			PersistConnections: tc.persist,
		})
		if final.Status != "changed" {
			t.Fatalf("persist %v: status %q, want changed: %s", tc.persist, final.Status, final.EventData.Message)
		}
		if got := h.settledLogins(t) - before; got != tc.want {
			t.Errorf("persist %v: %d logins for ten tasks, want %d", tc.persist, got, tc.want)
		}
	}
	code, r, err := h.sshd.Exec(context.Background(), []string{"cat", "/tmp/mesh-persist"})
	if err != nil || code != 0 {
		t.Fatalf("reading the tasks' file on the device: exit %d, %v", code, err)
	}
	written, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Fields(string(written)); len(lines) != 20 {
		t.Fatalf("the device holds %d appended lines, want 20: a task did not run", len(lines))
	}
}
