// The Walk tier's Release Gate for virt.cml (Phase 127d): a cml_controller
// task runs from a Runner, over real NATS, the real Agent, native Adapter and
// per-task child process, against an in-memory CML controller reached through
// the real TLS pin and login; and a device reached through a console server
// is refused on the Runner, naming why, since a Runner cannot resolve the
// server or its login.
package main_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cisco"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cml"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestCMLWalkReleaseGate_AControllerTaskRunsAndAConsoleDeviceIsRefused
// dispatches virt.cml.lab.create, then virt.cml.lab.list, to a Runner as a
// Controller does: the cml_controller's type and properties (its base URL and
// the controller certificate's fingerprint) and its own login. The lab is
// made on the controller and listed back, so both calls crossed the
// per-task child's real HTTPS client. A Cisco router whose transport is
// console is then dispatched with a host the Runner could dial: the task
// fails naming the console server as something only the pleiades CLI
// resolves, and the controller hears nothing more.
func TestCMLWalkReleaseGate_AControllerTaskRunsAndAConsoleDeviceIsRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Walk-tier CML gate, which runs real NATS and a real Runner, in short mode")
	}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{
		"cml-create.yaml": "id: cml-create\ntasks:\n  - name: make a lab\n    virt.cml.lab.create:\n      title: walk-gate\n",
		"cml-list.yaml":   "id: cml-list\ntasks:\n  - name: list the labs\n    virt.cml.lab.list: {}\n",
		"console.yaml":    "id: console\ntasks:\n  - name: read the version\n    net.ios.command:\n      command: show version\n",
	})
	controller := cmltest.New(t, "walk-admin", "walk-cml-password")
	// The device as the Controller holds it, and its type and properties as
	// the Controller sends them (record.Dispatched), so a key the Controller
	// leaves off a dispatch fails here as it would in a deployment.
	controllerDevice, err := cml.NewController(record.Record{ID: "cml", Name: "cml", Type: cml.TypeController,
		Properties: map[string]pkginventory.PropertyValue{
			cml.APIBaseProperty:           controller.URL,
			devicetls.FingerprintProperty: devicetls.Fingerprint(controller.Certificate()),
		}})
	if err != nil {
		t.Fatal(err)
	}
	controllerDispatch := func(runbook string) wire.DispatchPayload {
		jobID := uuid.New().String()
		payload := wire.DispatchPayload{
			JobID: jobID, RunbookID: runbook,
			DeviceID: "cml-gate-" + jobID[:8], DeviceName: "cml", DeviceHost: "127.0.0.1",
			Capabilities: controllerDevice.Capabilities(),
			Secrets:      credential.Flatten(credential.Credential{Username: "walk-admin", Password: "walk-cml-password"}),
		}
		payload.DeviceType, payload.DeviceProperties = record.Dispatched(controllerDevice)
		return payload
	}

	if _, done := h.run(t, controllerDispatch("cml-create")); done.Status == "failed" {
		t.Fatalf("virt.cml.lab.create from a Runner failed: %q", done.EventData.Message)
	}
	controller.Mu.Lock()
	var titles []string
	for _, lab := range controller.Labs {
		titles = append(titles, lab.Title)
	}
	controller.Mu.Unlock()
	if !slices.Contains(titles, "walk-gate") {
		t.Fatalf("the controller holds labs %q, want the one the Runner made", titles)
	}
	if _, done := h.run(t, controllerDispatch("cml-list")); done.Status == "failed" {
		t.Fatalf("virt.cml.lab.list from a Runner failed: %q", done.EventData.Message)
	}

	// A wrong pin is refused by the Runner's child as the CLI refuses it:
	// the fingerprint is what proves this is the controller.
	pinned := controllerDispatch("cml-list")
	pinned.DeviceProperties[devicetls.FingerprintProperty] = "sha256:" + strings.Repeat("0", 64)
	if _, done := h.run(t, pinned); done.Status != "failed" || !strings.Contains(done.EventData.Message, devicetls.Fingerprint(controller.Certificate())) {
		t.Errorf("a wrong pin: %s %q, want refused naming the certificate's own fingerprint", done.Status, done.EventData.Message)
	}

	controller.Mu.Lock()
	before := len(controller.Calls)
	controller.Mu.Unlock()
	jobID := uuid.New().String()
	node, err := cisco.NewRouter(record.Record{ID: "r1", Name: "r1", Type: "cisco_router",
		Properties: map[string]pkginventory.PropertyValue{
			// A host the Runner could dial, so only the refusal stops it.
			"host": h.sshHost, "port": h.sshPort,
			"transport": "console", "console_server": "cml", "console_line": "00000000-0000-4000-8000-000000000001",
		}})
	if err != nil {
		t.Fatal(err)
	}
	console := wire.DispatchPayload{
		JobID: jobID, RunbookID: "console",
		DeviceID: "console-gate-" + jobID[:8], DeviceName: "r1", DeviceHost: h.sshHost, SSHPort: h.sshPort,
		Capabilities: node.Capabilities(),
		Secrets:      credential.Flatten(credential.Credential{Username: "netops", Password: "node-password"}),
	}
	console.DeviceType, console.DeviceProperties = record.Dispatched(node)
	_, done := h.run(t, console)
	if done.Status != "failed" || !strings.Contains(done.EventData.Message, `reached through a console server, which only the pleiades CLI resolves`) {
		t.Errorf("a console-served device on a Runner: %s %q, want it refused naming the console server", done.Status, done.EventData.Message)
	}
	controller.Mu.Lock()
	after := len(controller.Calls)
	controller.Mu.Unlock()
	if after != before {
		t.Errorf("the controller heard %d calls during the console task, want none", after-before)
	}
}
