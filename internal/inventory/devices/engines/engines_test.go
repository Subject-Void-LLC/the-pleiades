// Tests of the engine facts a container-running device's accessors read
// from its discovery.
package engines_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/engines"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// accessors reads facts as onboarding records them.
func accessors(facts map[string]any) engines.Accessors {
	d := inventory.Discovery{Facts: facts}
	rec := record.Record{ID: "h", Name: "h", Properties: map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}}
	return engines.Accessors{Of: record.NewBase(rec, nil)}
}

// TestFactKeyAndGrants: the key each engine fact is stored under, and the
// capabilities only onboarding may grant.
func TestFactKeyAndGrants(t *testing.T) {
	if got := engines.FactKey(capability.EnginePodman, engines.FieldSocket); got != "podman_socket" {
		t.Errorf("FactKey = %q", got)
	}
	for _, c := range engines.Grants {
		if !engines.IsGrant(c) {
			t.Errorf("%s is in Grants but not IsGrant", c)
		}
	}
	for _, c := range engines.SSHGrants {
		if !engines.IsGrant(c) {
			t.Errorf("an SSH grant %s is not a grant", c)
		}
	}
	if engines.IsGrant(capability.NameSSHTransport) {
		t.Error("SSHTransportCapable, a record's own claim, is an onboarding grant")
	}
	in := []capability.Name{capability.NameSSHTransport, capability.NameDocker, capability.NameEngineTLS}
	out := engines.WithoutGrants(in)
	if !slices.Equal(out, []capability.Name{capability.NameSSHTransport}) {
		t.Errorf("WithoutGrants = %v", out)
	}
	if len(in) != 3 || in[1] != capability.NameDocker {
		t.Error("WithoutGrants changed its argument")
	}
}

// TestAccessors: each engine onboarding found, where its socket is, its
// pinned identity, the carriers that reach it, and the account onboarding
// used; anything not a carrier is dropped.
func TestAccessors(t *testing.T) {
	a := accessors(map[string]any{
		engines.FactKey(capability.EngineDocker, engines.FieldSocket):   "/var/run/docker.sock",
		engines.FactKey(capability.EngineDocker, engines.FieldID):       "docker-1",
		engines.FactKey(capability.EngineDocker, engines.FieldCarriers): []any{"socket", "teleport", "stdio"},
		engines.FactKey(capability.EnginePodman, engines.FieldSocket):   "/run/podman/podman.sock",
		engines.FactKey(capability.EnginePodman, engines.FieldID):       "podman-1",
		engines.FactLoginUser: "ops",
	})
	if got := a.ContainerEngines(); !slices.Equal(got, []capability.EngineKind{capability.EngineDocker, capability.EnginePodman}) {
		t.Errorf("ContainerEngines = %v", got)
	}
	if a.DockerEndpoint() != "/var/run/docker.sock" || a.PodmanEndpoint() != "/run/podman/podman.sock" {
		t.Errorf("endpoints = %q, %q", a.DockerEndpoint(), a.PodmanEndpoint())
	}
	if a.EngineID(capability.EnginePodman) != "podman-1" {
		t.Errorf("EngineID(podman) = %q", a.EngineID(capability.EnginePodman))
	}
	if got := a.EngineCarriers(capability.EngineDocker); !slices.Equal(got, []capability.Carrier{capability.CarrierSocket, capability.CarrierStdio}) {
		t.Errorf("EngineCarriers(docker) = %v, want the two real carriers", got)
	}
	if a.EngineLoginUser() != "ops" {
		t.Errorf("EngineLoginUser = %q", a.EngineLoginUser())
	}

	none := accessors(map[string]any{engines.FactKey(capability.EngineDocker, engines.FieldSocket): "/var/run/docker.sock"})
	if got := none.ContainerEngines(); got != nil {
		t.Errorf("an engine with no pinned identity was listed: %v", got)
	}
}
