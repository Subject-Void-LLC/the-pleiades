// Release gate for the Runner's plan-time validation of a dispatched
// runbook (FAILURE_PATTERNS 322), over a real NATS broker, the real Agent
// and native Adapter, a real per-task child process and a real sshd
// container.
package main_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// validateGateMarker is the file the refused runbook's first task would
// create on the device if it ran.
const validateGateMarker = "/tmp/pleiades-validate-gate-marker"

// TestValidateDispatchReleaseGate_NoTaskRunsBeforeARefusal proves, on the
// device itself, that a dispatched runbook failing validation changes
// nothing. Its first task would create a marker file and its second
// calls a method that is only declared; before the Runner validated a
// dispatch, the first task ran and only the second failed. The probe that
// checks for the marker is shown to see a marker that does exist, so its
// passing is evidence rather than a probe that can never fail.
func TestValidateDispatchReleaseGate_NoTaskRunsBeforeARefusal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInHomeDir, map[string]string{
		"refused.yaml": "id: refused\ntasks:\n" +
			"  - name: create the marker\n    fqcn: file.touch\n    params:\n      path: " + validateGateMarker + "\n" +
			"  - name: call a method that is only declared\n    fqcn: file.template\n",
		"touch.yaml": "id: touch\ntasks:\n" +
			"  - name: create the marker\n    fqcn: file.touch\n    params:\n      path: " + validateGateMarker + "\n",
		"absent.yaml": "id: absent\ntasks:\n" +
			"  - name: the marker must not exist\n    fqcn: exec.command\n    params:\n      cmd: test ! -e " + validateGateMarker + "\n",
	})
	payload := func(runbookID string) wire.DispatchPayload {
		return wire.DispatchPayload{
			JobID:        uuid.New().String(),
			RunbookID:    runbookID,
			DeviceID:     "release-gate-device",
			DeviceName:   "release-gate-device",
			DeviceHost:   h.sshHost,
			SSHPort:      h.sshPort,
			Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem, capability.NameCommandExec},
			Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		}
	}

	refused := h.dispatch(t, payload("refused"))
	if refused.Status != "failed" || !strings.Contains(refused.EventData.Message, "fails validation, so no task ran") ||
		!strings.Contains(refused.EventData.Message, `"file.template", which is declared but not yet implemented`) {
		t.Errorf("refused runbook ended %q: %q, want failed naming the declared-only method", refused.Status, refused.EventData.Message)
	}

	if absent := h.dispatch(t, payload("absent")); absent.Status == "failed" {
		t.Fatalf("the marker exists after a refused dispatch: its first task ran (%q)", absent.EventData.Message)
	}

	// The control: create the marker for real, and the same probe fails.
	if touched := h.dispatch(t, payload("touch")); touched.Status == "failed" {
		t.Fatalf("the control could not create the marker: %q", touched.EventData.Message)
	}
	if present := h.dispatch(t, payload("absent")); present.Status != "failed" {
		t.Fatalf("the probe passed with the marker present (%q), so it proves nothing", present.Status)
	}
}
