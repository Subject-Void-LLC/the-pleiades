// Package main_test: the Walk-tier release gate for a capability an
// external program defines (Phase 127a): the grant the Controller ships
// with a dispatch is held by the Runner to the definition it loaded.
package main_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestExternalCapabilityReleaseGate_TheRunnerHoldsAShippedGrant: a real
// Runner, over real NATS, rebuilds the dispatched linux_server with the
// grant the Controller shipped, and the program's method finds its
// notebook there and writes the note on a real sshd; a grant whose
// properties do not fit the definition grants nothing, and the task is
// refused naming the capability.
func TestExternalCapabilityReleaseGate_TheRunnerHoldsAShippedGrant(t *testing.T) {
	if testing.Short() {
		t.Skip("starts NATS and sshd")
	}
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(capability.SnapshotForTest())
	collections := buildApprovedExample(t)
	if _, err := loader.Load(context.Background(), collections, loader.Options{}); err != nil {
		t.Fatalf("loading the approved example: %v", err)
	}
	h := newReleaseGateHarnessFor(t, knownHostsInEnvironment, map[string]string{
		"notebook.yaml": "id: notebook\ntasks:\n  - name: a note\n    example.notebook.write:\n      name: walk\n      content: through the runner\n",
	})
	addr := net.JoinHostPort(h.sshHost, strconv.Itoa(h.sshPort))
	notes := readOverSSH(t, addr, `mkdir -p "$HOME/notes" && cd "$HOME/notes" && pwd`)

	dispatch := func(props map[string]any) wire.JobEvent {
		t.Helper()
		granted := inventory.GrantsProperty([]inventory.Grant{{
			Capability: "example.notebook", Source: inventory.GrantAsserted, Properties: props,
			By: "ops", Reason: "the release gate made the directory", At: time.Now(),
		}})
		return h.dispatch(t, wire.DispatchPayload{
			JobID: uuid.New().String(), RunbookID: "notebook",
			DeviceID: "release-gate-device", DeviceName: "release-gate-device", DeviceHost: h.sshHost, SSHPort: h.sshPort,
			DeviceType: "linux_server",
			DeviceProperties: map[string]any{
				"host": h.sshHost, "port": h.sshPort, inventory.GrantedProperty: granted,
			},
			Capabilities: []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem, "example.notebook"},
			Secrets:      credential.Flatten(credential.Credential{Username: releaseGateSSHUser, Password: releaseGateSSHPassword}),
		})
	}

	evt := dispatch(map[string]any{"dir": notes})
	if evt.Status != "changed" {
		t.Fatalf("the job did not write the note: %+v", evt)
	}
	if got := readOverSSH(t, addr, "cat "+notes+"/walk"); got != "through the runner" {
		t.Fatalf("the note holds %q", got)
	}

	evt = dispatch(map[string]any{})
	if evt.Status != "failed" || !strings.Contains(evt.EventData.Message, "example.notebook") {
		t.Errorf("a grant missing its required property: %+v", evt)
	}
}
