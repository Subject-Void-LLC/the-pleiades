// Release gate for wait.routes's alternatives on the Walk tier (Phase 127b):
// a Runner's plan-time validation of a dispatch refuses a device that is
// neither an FRR router nor a Cisco IOS one before any task runs, over a
// real NATS broker, the real Agent and native Adapter, and a real sshd
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

// TestRoutesAdmissionReleaseGate_ADeviceOfNeitherKindIsRefusedBeforeTheRun:
// the same wait.routes runbook is dispatched twice to the same sshd
// container. Dispatched as a Linux server that runs commands over SSH, it
// fails validation naming both kinds, so no task ran; before the method
// declared its alternatives this passed validation and failed only inside
// the task. Dispatched as an FRR router, the control, it passes validation
// and reaches the device, where the container's missing vtysh fails the
// read: so the refusal above is the alternatives', not the harness's.
func TestRoutesAdmissionReleaseGate_ADeviceOfNeitherKindIsRefusedBeforeTheRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Release Gate container test in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInHomeDir, map[string]string{
		"settle.yaml": "id: settle\ntasks:\n" +
			"  - name: routes settle\n    wait.routes:\n      prefixes: [10.1.0.0/24]\n      stable_for: 0\n      timeout: 3\n",
	})
	payload := func(caps ...capability.Name) wire.DispatchPayload {
		return wire.DispatchPayload{
			JobID:        uuid.New().String(),
			RunbookID:    "settle",
			DeviceID:     "routes-gate-" + uuid.New().String()[:8],
			DeviceName:   "routes-gate-device",
			DeviceHost:   h.sshHost,
			SSHPort:      h.sshPort,
			Capabilities: caps,
			Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		}
	}

	linux := h.dispatch(t, payload(capability.NameSSHTransport, capability.NameLinux, capability.NameCommandExec))
	if linux.Status != "failed" || !strings.Contains(linux.EventData.Message, "fails validation, so no task ran") ||
		!strings.Contains(linux.EventData.Message, `is none of them`) ||
		!strings.Contains(linux.EventData.Message, "frr (FRRCapable and CommandExecCapable, over ssh); ios (CiscoIOSCapable") {
		t.Errorf("a Linux server ended %q: %q, want refused at validation naming both kinds", linux.Status, linux.EventData.Message)
	}

	frr := h.dispatch(t, payload(capability.NameSSHTransport, capability.NameFRR, capability.NameCommandExec))
	if frr.Status != "failed" || strings.Contains(frr.EventData.Message, "fails validation") || !strings.Contains(frr.EventData.Message, "vtysh") {
		t.Errorf("an FRR router ended %q: %q, want past validation and failed at the device's missing vtysh", frr.Status, frr.EventData.Message)
	}
}
