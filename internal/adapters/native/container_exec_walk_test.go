// The Walk tier's half of Phase 119a's release gate: a container host
// dispatched to a Runner is rebuilt from its payload in the Runner's real
// per-task child, and container.docker.exec there reaches the engine the
// device advertised, over a real SSH login to a real Docker engine. It is
// the path finding S1 ran on: before, the child dialed the socket path the
// record typed, on the Runner's own machine.
package native

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// walkGateImage is the image the gate's container runs, pinned exactly.
const walkGateImage = "alpine:3.20.10"

// containerHostPayload is what a Controller dispatches for an onboarded
// container host at d: its address, and the discovery onboarding recorded,
// bound to that address.
func containerHostPayload(d *testsupport.EngineHost, engineID string) wire.DispatchPayload {
	props := map[string]any{"host": d.Host, "port": d.Port}
	found := inventory.Discovery{
		Protocol:     "docker",
		Capabilities: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineSocket},
		Facts: map[string]any{
			engines.FactKey(capability.EngineDocker, engines.FieldSocket):   testsupport.DockerHostSocket,
			engines.FactKey(capability.EngineDocker, engines.FieldID):       engineID,
			engines.FactKey(capability.EngineDocker, engines.FieldCarriers): []any{"socket"},
			// What onboarding's `id -un` answers for the login the payload
			// carries; a session compares it with the run's own answer.
			engines.FactLoginUser: testsupport.DockerHostUser,
		},
	}
	found.Binding = record.DiscoveryBinding(container.TypeHost, inventory.NewProperties(props))
	props[inventory.DiscoveredProperty] = found.Property()
	return wire.DispatchPayload{
		JobID: "j1", DeviceID: "d1", DeviceName: "box", DeviceHost: d.Host, SSHPort: d.Port,
		Capabilities:     []capability.Name{capability.NameSSHTransport, capability.NameNetworkAddressable, capability.NameContainerEngine, capability.NameDocker, capability.NameEngineSocket},
		DeviceType:       container.TypeHost,
		DeviceProperties: props,
		Secrets:          map[string]string{wire.SecretUsername: testsupport.DockerHostUser, wire.SecretPassword: testsupport.DockerHostPassword},
	}
}

// TestContainerExec_RunnerChildReachesTheDevicesEngine runs
// container.docker.exec in the real per-task child against a dispatched
// container host, and proves it ran inside the container on the device's
// engine; then it proves an older Controller's payload, still carrying the
// typed socket, is refused rather than run.
func TestContainerExec_RunnerChildReachesTheDevicesEngine(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real Docker engine behind a real sshd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	d := testsupport.StartDockerHost(t, testsupport.EngineHostOptions{})
	name := d.StartContainer(t, walkGateImage, "")
	knownHosts, err := d.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The child inherits this process's environment, as a Runner's does.
	t.Setenv("PLEIADES_KNOWN_HOSTS", knownHosts)

	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatal(err)
	}
	desc, ok := collection.Lookup("container.docker.exec")
	if !ok {
		t.Fatal("container.docker.exec is not registered in this binary")
	}
	params := map[string]any{"name": name, "cmd": "echo walk reached $(cat /etc/alpine-release)"}

	payload := containerHostPayload(d, d.EngineID(t))
	_, facts, err := exec.invoke(ctx, desc, newWireDevice(payload), params, collection.ModeExecute)
	if err != nil {
		t.Fatalf("invoke in the per-task child: %v", err)
	}
	if out, _ := facts["stdout"].(string); !strings.Contains(out, "walk reached 3.20") {
		t.Errorf("stdout = %q, want the container's answer", out)
	}

	// An engine other than the one onboarding pinned is refused.
	other := containerHostPayload(d, "an-engine-onboarding-never-saw")
	if _, _, err := exec.invoke(ctx, desc, newWireDevice(other), params, collection.ModeExecute); err == nil || !strings.Contains(err.Error(), "onboarding proved") {
		t.Errorf("a payload pinning another engine: err = %v, want the identity refusal", err)
	}

	// A login other than the account onboarding found the engine as is
	// refused, asked of the device over the same login, before any request
	// reaches the engine: the facts are that account's. The run's own
	// account is the credential's username, which the child masks like every
	// other credential value on its way out, so the refusal is held to the
	// recorded account and the remedy, and to never printing the username.
	elsewhere := containerHostPayload(d, d.EngineID(t))
	discovered := elsewhere.DeviceProperties[inventory.DiscoveredProperty].(map[string]any)
	discovered["facts"].(map[string]any)[engines.FactLoginUser] = "an-account-onboarding-used"
	_, _, err = exec.invoke(ctx, desc, newWireDevice(elsewhere), params, collection.ModeExecute)
	if err == nil || !strings.Contains(err.Error(), "an-account-onboarding-used") || !strings.Contains(err.Error(), "pleiades onboard box") {
		t.Errorf("a login onboarding did not use: err = %v, want the account refusal naming the recorded account and re-onboarding", err)
	} else if strings.Contains(err.Error(), testsupport.DockerHostUser) {
		t.Errorf("the refusal printed the credential's username: %v", err)
	}

	// An older Controller dispatches the typed socket a record held. The
	// child rebuilds the device as its real type, which refuses that key, so
	// the task is refused instead of run against a socket on this machine.
	old := containerHostPayload(d, d.EngineID(t))
	old.DeviceProperties = map[string]any{"host": d.Host, "port": strconv.Itoa(d.Port), container.RefusedEndpointKey: "/var/run/docker.sock"}
	if _, _, err := exec.invoke(ctx, desc, newWireDevice(old), params, collection.ModeExecute); err == nil || !strings.Contains(err.Error(), container.RefusedEndpointKey) {
		t.Errorf("an older Controller's payload: err = %v, want a refusal naming %s", err, container.RefusedEndpointKey)
	}
}
