// Package dispatch: a template's SNMP credential (pleiades_snmp) delivered
// to the devices of a native dispatch.
package dispatch

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ApplySNMP gives the device a payload is for the template's SNMP
// credential, in place of the SNMP family of whatever secrets the device
// already has: its own stored credential, or the template's machine
// credential. It runs after those are settled (worker_devices.go), because
// the device's own credential is read only when the template gave it no
// secrets, and an SNMP credential arriving first would read as one.
//
// The SNMP family replaces the device's own as a template's machine
// credential replaces a stored login: an operator binding one and watching a
// run use another would have nothing in the record to say why. The login
// beside it is kept, which is the family rule the Crawl tier's store keeps.
//
// It reaches a device a template's machine credential would reach
// (record.TakesMachineCredential): a device that can be logged into, the kind
// an SNMP agent runs on. A cluster, an HTTP API or a cloud account is never
// given it. The Ansible path is given nothing from it, since no Ansible
// module reads these keys and an SNMP secret in a playbook's environment
// would be one more place to leak it. No SNMP client reads it yet either
// (Phase 97), so on the native path it is carried and masked.
//
// Exported for the reason ApplyInjection is: a release gate building a
// payload goes through the conversion the fan-out uses.
func ApplySNMP(payload *wire.DispatchPayload, art credtype.Artifact, device pkginventory.InventoryItem) {
	snmp, ok := art.SNMP()
	if !ok {
		return
	}
	if descriptor, known := launch.Lookup(payload.Kind); known && descriptor.Adapter == routing.AdapterLegacy {
		return
	}
	if !record.TakesMachineCredential(payload.DeviceType, device) {
		return
	}
	secrets := make(map[string]string, len(payload.Secrets)+len(wire.SNMPKeys()))
	for key, value := range payload.Secrets {
		secrets[key] = value
	}
	// The device's own SNMP family goes whole, so a v3 passphrase of its own
	// never rides beside a template's community.
	for _, key := range wire.SNMPKeys() {
		delete(secrets, key)
	}
	for key, value := range snmp.Secrets() {
		secrets[key] = value
	}
	payload.Secrets = secrets
}
