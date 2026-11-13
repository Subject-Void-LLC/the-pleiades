// Release gate for the Walk tier's refusal of a project file (Phase 127b):
// a Runner holds no project, so its plan-time validation of a dispatch
// refuses a task naming a project file before any task runs, over a real
// NATS broker, the real Agent and native Adapter, a real per-task child
// process and a real sshd container.
package main_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// projectGateMarker is the file the refused runbook's first task would
// create on the device if it ran.
const projectGateMarker = "/tmp/pleiades-project-file-gate-marker"

// TestProjectFileReleaseGate_NoTaskRunsBeforeAProjectFileIsRefused: a
// runbook whose first task creates a marker and whose second applies an FRR
// configuration from a project file is dispatched to an FRR router. Before
// the rule, validation passed it, the first task ran and only the second
// failed, with sdk.ErrNoProjectFiles. Now the dispatch fails validation
// naming the project file, and a probe shows the marker was never made; the
// probe is shown to fail once the marker exists, so its passing is evidence.
func TestProjectFileReleaseGate_NoTaskRunsBeforeAProjectFileIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInHomeDir, map[string]string{
		"apply.yaml": "id: apply\ntasks:\n" +
			"  - name: create the marker\n    file.touch:\n      path: " + projectGateMarker + "\n" +
			"  - name: apply the candidate\n    net.frr.config:\n      src: configs/r1.frr.conf\n",
		"touch.yaml": "id: touch\ntasks:\n" +
			"  - name: create the marker\n    file.touch:\n      path: " + projectGateMarker + "\n",
		"absent.yaml": "id: absent\ntasks:\n" +
			"  - name: the marker must not exist\n    exec.command:\n      cmd: test ! -e " + projectGateMarker + "\n",
	})
	payload := func(runbookID string) wire.DispatchPayload {
		return wire.DispatchPayload{
			JobID:      uuid.New().String(),
			RunbookID:  runbookID,
			DeviceID:   "project-gate-" + uuid.New().String()[:8],
			DeviceName: "r1",
			DeviceHost: h.sshHost,
			SSHPort:    h.sshPort,
			Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem,
				capability.NameCommandExec, capability.NameFRR},
			Secrets: credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		}
	}

	refused := h.dispatch(t, payload("apply"))
	if refused.Status != "failed" || !strings.Contains(refused.EventData.Message, "fails validation, so no task ran") ||
		!strings.Contains(refused.EventData.Message, "gives src, a file in the project, and this run has no project directory") {
		t.Errorf("the project file ended %q: %q, want refused at validation naming src", refused.Status, refused.EventData.Message)
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
