// The test harness: this machine's real Docker engine, reached through its
// own socket as a device onboarded with the local carrier, and a runbook
// context that keeps what a method records, holds registry accounts by
// host, and keeps the values a method registers for masking.
package registry_test

import (
	"context"
	"os"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/registry"
)

// dockerSocket is the engine these tests reach.
const dockerSocket = "/var/run/docker.sock"

// engineDevice is a device whose Docker engine this machine reaches
// through its own socket, as onboarding would have recorded it.
type engineDevice struct {
	*inventorytest.Stub
	id   capability.EngineID
	user string
}

func (d *engineDevice) ContainerEngines() []capability.EngineKind {
	return []capability.EngineKind{capability.EngineDocker}
}
func (d *engineDevice) DockerEndpoint() capability.SocketAddress { return dockerSocket }
func (d *engineDevice) EngineSocket(capability.EngineKind) capability.SocketAddress {
	return dockerSocket
}
func (d *engineDevice) EngineID(capability.EngineKind) capability.EngineID { return d.id }
func (d *engineDevice) EngineCarriers(capability.EngineKind) []capability.Carrier {
	return []capability.Carrier{capability.CarrierLocal}
}
func (d *engineDevice) EngineLoginUser() string { return d.user }

// recorder keeps what a method records, answers registry accounts from
// accounts, and keeps what the method registers for masking.
type recorder struct {
	stats    map[string]any
	accounts map[string][2]string
	masked   []string
}

func (r *recorder) RegistryCredential(host string) (string, string, bool) {
	a, ok := r.accounts[host]
	return a[0], a[1], ok
}
func (r *recorder) RegisterSecret(values ...string) { r.masked = append(r.masked, values...) }

func (r *recorder) InjectSecrets() map[string]string     { return map[string]string{} }
func (r *recorder) SetStat(key string, value any) error  { r.stats[key] = value; return nil }
func (r *recorder) EmitFact(key string, value any) error { return r.SetStat(key, value) }

func fresh() *recorder { return &recorder{stats: map[string]any{}, accounts: map[string][2]string{}} }

// harness is the real engine as a device, and a client for the test's own
// reads and cleanup.
type harness struct {
	device inventory.InventoryItem
	api    *client.Client
}

// newHarness reaches this machine's Docker, skipping where there is none.
func newHarness(t *testing.T) *harness {
	t.Helper()
	if testing.Short() {
		t.Skip("reaches a real Docker engine")
	}
	_, err := os.Stat(dockerSocket)
	testsupport.Require(t, "docker", err == nil, "no Docker socket at "+dockerSocket)
	ctx := context.Background()
	id, err := containerengine.ReadIdentity(ctx, containerengine.Local(dockerSocket), capability.EngineDocker)
	if err != nil {
		t.Fatalf("reading the engine's identity: %v", err)
	}
	who, err := containerengine.LocalUser()
	if err != nil {
		t.Fatal(err)
	}
	containerengine.AllowLocalCarrier()
	api, err := client.New(client.WithHost("unix://" + dockerSocket))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	return &harness{
		device: &engineDevice{
			Stub: &inventorytest.Stub{StubName: "engine-host", StubState: inventory.StateActive,
				Caps: []capability.Name{capability.NameContainerEngine, capability.NameDocker, capability.NameEngineLocal}},
			id: id.ID, user: who,
		},
		api: api,
	}
}

// call runs fqcn's Invoke, or its Check, with params.
func call(t *testing.T, h *harness, rc *recorder, fqcn string, check bool, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	if check {
		return d.Check(context.Background(), rc, h.device, params)
	}
	return d.Invoke(context.Background(), rc, h.device, params)
}

// inverseOf is the undo a run recorded.
func inverseOf(rc *recorder) (string, map[string]any) {
	inv, _ := rc.stats["inverse"].(map[string]any)
	fqcn, _ := inv["fqcn"].(string)
	params, _ := inv["params"].(map[string]any)
	return fqcn, params
}
