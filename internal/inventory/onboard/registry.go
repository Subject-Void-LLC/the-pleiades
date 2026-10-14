// Package onboard runs onboarding: it probes a device over its protocol,
// records what the device proved as a revision, and moves it from
// discovered to active. It is the only writer of
// inventory.DiscoveredProperty. The generic types learn every capability
// beyond a small baseline this way; container_host learns its engine and
// how a task reaches it.
//
// One Prober per onboarded device type, each registered from its own file,
// so a protocol added later (RESTCONF, gNMI, SNMP) adds a prober and a
// type without touching the others.
package onboard

import (
	"context"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Probed is what one probe proved: the capabilities the device's own
// answers support, and facts worth keeping. Every fact is text or a list
// of text, bounded by factText and factList: those are the values every
// inventory store round-trips unchanged, so a re-probe that learned
// nothing new compares equal and writes nothing.
type Probed struct {
	Capabilities []capability.Name
	Facts        map[string]any
	// Warnings says what each weakening the device's record allows means
	// (a deprecated TLS version, legacy ciphers, a credential over plain
	// HTTP). They are reported with the result, never stored as facts.
	Warnings []string
}

// Peers lets a prober reach one other device in the same inventory, with
// that device's own credential, for a discovery that spans two devices: a
// cluster whose nodes are containers on a container host's engine. It also
// says how a device is reached when its record names a route of SSH hops.
type Peers interface {
	// Device returns the device named name and its credential.
	Device(ctx context.Context, name string) (inventory.InventoryItem, map[string]string, error)
	// Route returns a dialer through device's route, or nil when it has
	// none or onboarding was given no way to resolve one.
	Route(ctx context.Context, device inventory.InventoryItem) (sdk.RouteDial, error)
}

// PeerProber is a Prober whose discovery may consult a peer device.
// Onboarding calls ProbeWithPeers instead of Probe for one.
type PeerProber interface {
	Prober
	ProbeWithPeers(ctx context.Context, device inventory.InventoryItem, secrets map[string]string, peers Peers) (Probed, error)
}

// Prober proves what a device of one generic type can do.
type Prober interface {
	// Protocol names the protocol the probe speaks, recorded with the
	// discovery.
	Protocol() string

	// Probe connects to device, authenticating with secrets (the device's
	// own credential, flattened under the wire.Secret* keys; nil when none
	// is stored), and reports what the device's answers prove. An error
	// means nothing was proved: the connection, the handshake or the
	// credential failed.
	Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error)
}

var probers = registry.New[Prober]()

// Register adds deviceType's prober. It panics on a duplicate.
func Register(deviceType string, p Prober) {
	probers.MustRegister(deviceType, p)
}

// Lookup returns deviceType's prober.
func Lookup(deviceType string) (Prober, bool) {
	return probers.Get(deviceType)
}

// Types returns every device type with a prober, sorted: the types
// onboarding can prove something about.
func Types() []string {
	all := probers.All()
	types := make([]string, 0, len(all))
	for t := range all {
		types = append(types, t)
	}
	sort.Strings(types)
	return types
}

// SnapshotForTest captures the prober registry and returns a function that
// restores it, for a test that registers a stand-in prober.
func SnapshotForTest() func() {
	return probers.SnapshotForTest()
}
