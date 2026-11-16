// Package dispatch: a template's SNMP credential (pleiades_snmp) delivered
// to the devices of a native dispatch, and a device's own SNMP family kept
// off every dispatch.
package dispatch

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ApplySNMP gives the device a payload is for the template's SNMP
// credential, beside whatever login the device already has: its own stored
// credential's, or the template's machine credential. It runs after those
// are settled (worker_devices.go), because the device's own credential is
// read only when the template gave it no secrets, and an SNMP credential
// arriving first would read as one.
//
// The device's own SNMP family never reaches a dispatch (deviceSecrets), so
// the template's stands where a stored one would, as a template's machine
// credential stands in for a stored login. Any SNMP key already on the
// payload is removed first all the same, so a v3 passphrase never rides
// beside a template's community whatever supplied the login. The login is
// kept, which is the family rule the Crawl tier's store keeps.
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

// deviceSecrets is what a dispatch carries of a device's own stored
// credential, the fallback when the template gave the device no secrets:
// its login, flattened, and never its SNMP family, or nil when the login is
// empty, so an SNMP-only device reads on the Runner as one with no
// credential.
//
// The SNMP family stays home because nothing on a Runner reads it yet, and a
// dispatch is not a sealed hop: the payload sits on the JetStream stream for
// its retention window and reaches the Runner's per-task child and every
// external Collection a task calls. A native, check, rollback or playbook
// job would each have carried the device's community or v3 passphrases there
// for no task. Phase 97, which builds the SNMP client, adds the device's
// SNMP family back for the methods that declare they need it, and only for
// them. A template's bound SNMP credential is the operator's explicit
// choice, and ApplySNMP still delivers it to a native job.
func deviceSecrets(cred credential.Credential) map[string]string {
	secrets := credential.Flatten(cred.WithSNMP(wire.SNMPCredential{}))
	if len(secrets) == 0 {
		return nil
	}
	return secrets
}
