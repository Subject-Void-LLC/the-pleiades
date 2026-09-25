// Package onboard runs onboarding: it probes a generic device over its
// protocol, records what the device proved as a revision, and moves it
// from discovered to active. It is the only writer of
// inventory.DiscoveredProperty.
//
// One Prober per generic device type, each registered from its own file,
// so a protocol added later (RESTCONF, gNMI, SNMP) adds a prober and a
// type without touching the others.
package onboard

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
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

// SnapshotForTest captures the prober registry and returns a function that
// restores it, for a test that registers a stand-in prober.
func SnapshotForTest() func() {
	return probers.SnapshotForTest()
}
