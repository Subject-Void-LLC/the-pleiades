// Package engines holds what every device type that can run a container
// engine shares: the facts onboarding records about each engine it found,
// the capabilities onboarding may grant for them, and the accessors that
// read the facts back. container_host, generic_ssh and linux_server embed
// Accessors, so the three cannot read an engine's facts three ways.
//
// Facts are per engine, because a host can run Docker and Podman at once
// with a socket, an identity and working carriers each: "docker_socket",
// "docker_id", "podman_carriers" and so on (FactKey). Onboarding
// (internal/inventory/onboard) is their only writer.
package engines

import (
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// The per-engine facts, each stored under FactKey(kind, field).
const (
	// FieldSocket is the engine's socket path on the device.
	FieldSocket = "socket"
	// FieldID is the identity onboarding pinned (capability.EngineID).
	FieldID = "id"
	// FieldVersion is the engine's release.
	FieldVersion = "version"
	// FieldCarriers lists the carriers onboarding proved, in
	// capability.Carriers' order.
	FieldCarriers = "carriers"
	// FieldRootless is "true" for an engine running as the login user.
	FieldRootless = "rootless"
)

// FactKey is the fact name for one field of one engine, such as
// "docker_socket".
func FactKey(kind capability.EngineKind, field string) string {
	return string(kind) + "_" + field
}

// FactLoginUser is the user onboarding logged in as; a rootless engine
// belongs to that user, and a run as another user would read another store.
const FactLoginUser = "login_user"

// Grants is every capability onboarding may grant a device for its engines.
var Grants = []capability.Name{
	capability.NameContainerEngine,
	capability.NameDocker,
	capability.NamePodman,
	capability.NameEngineLocal,
	capability.NameEngineSocket,
	capability.NameEngineStdio,
	capability.NameEngineTLS,
}

// SSHGrants is what onboarding may grant a device reached only over SSH
// (linux_server, generic_ssh): its engines and the two SSH carriers, never
// TLS, which needs an address only container_host states.
var SSHGrants = []capability.Name{
	capability.NameContainerEngine,
	capability.NameDocker,
	capability.NamePodman,
	capability.NameEngineSocket,
	capability.NameEngineStdio,
}

// IsGrant reports whether c is one only onboarding may grant, so a
// classification rule naming it is dropped rather than trusted.
func IsGrant(c capability.Name) bool { return slices.Contains(Grants, c) }

// Accessors reads a device's engine facts from its record. A device type
// embeds it with Of set to its own record.Base.
type Accessors struct {
	Of *record.Base
}

// ContainerEngines implements capability.ContainerEngineCapable: the engines
// onboarding found, in capability.Engines' order.
func (a Accessors) ContainerEngines() []capability.EngineKind {
	var out []capability.EngineKind
	for _, kind := range capability.Engines() {
		if a.EngineID(kind) != "" {
			out = append(out, kind)
		}
	}
	return out
}

// DockerEndpoint implements capability.DockerCapable.
func (a Accessors) DockerEndpoint() capability.SocketAddress {
	return a.EngineSocket(capability.EngineDocker)
}

// PodmanEndpoint implements capability.PodmanCapable.
func (a Accessors) PodmanEndpoint() capability.SocketAddress {
	return a.EngineSocket(capability.EnginePodman)
}

// EngineSocket implements capability.EngineReach.
func (a Accessors) EngineSocket(kind capability.EngineKind) capability.SocketAddress {
	return capability.SocketAddress(record.DiscoveredFact(a.Of.Properties(), FactKey(kind, FieldSocket)))
}

// EngineID implements capability.EngineReach.
func (a Accessors) EngineID(kind capability.EngineKind) capability.EngineID {
	return capability.EngineID(record.DiscoveredFact(a.Of.Properties(), FactKey(kind, FieldID)))
}

// EngineCarriers implements capability.EngineReach. A carrier the platform
// does not know is dropped rather than tried.
func (a Accessors) EngineCarriers(kind capability.EngineKind) []capability.Carrier {
	var out []capability.Carrier
	for _, c := range record.DiscoveredList(a.Of.Properties(), FactKey(kind, FieldCarriers)) {
		if slices.Contains(capability.Carriers(), capability.Carrier(c)) {
			out = append(out, capability.Carrier(c))
		}
	}
	return out
}

// EngineLoginUser returns the account onboarding's probe ran as.
func (a Accessors) EngineLoginUser() string {
	return record.DiscoveredFact(a.Of.Properties(), FactLoginUser)
}

// WithoutGrants returns caps with every capability only onboarding may
// grant removed, for a type that unions classification into its baseline.
func WithoutGrants(caps []capability.Name) []capability.Name {
	return slices.DeleteFunc(slices.Clone(caps), IsGrant)
}
