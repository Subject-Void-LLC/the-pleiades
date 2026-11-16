// Package dispatch: a template's SNMP credential (pleiades_snmp) delivered
// to the devices of a native dispatch, and a device's own SNMP family kept
// off every dispatch but one whose job runs a method that reads it.
package dispatch

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/adapters/routing"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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
// The template's SNMP credential stands where the device's own SNMP family
// would, as a template's machine credential stands in for a stored login, so
// any SNMP key already on the payload is removed first: a device's own v3
// passphrase never rides beside a template's community. The login is kept,
// which is the family rule the Crawl tier's store keeps.
//
// It reaches a device a template's machine credential would reach
// (record.TakesMachineCredential), the kind an SNMP agent runs on beside its
// login, and a device reached by SNMP itself (SNMPCapable, a generic_snmp
// once onboarded), which takes no login. A cluster, an HTTP API or a cloud
// account is never given it. The Ansible path is given nothing from it, since
// no Ansible module reads these keys and an SNMP secret in a playbook's
// environment would be one more place to leak it.
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
	if !record.TakesMachineCredential(payload.DeviceType, device) && (device == nil || !device.HasCapability(capability.NameSNMP)) {
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
// credential, the fallback when the template gave the device no secrets: its
// login, flattened, and its SNMP family only when withSNMP, or nil when that
// leaves nothing, so an SNMP-only device of a job that reads no SNMP reads on
// the Runner as one with no credential.
//
// withSNMP is the job's (PreparedDefinition.ReadsSNMP, or for a rollback its
// steps', rollbackReadsSNMP): some method it runs declares it reads its
// device's SNMP credential. Every other dispatch leaves the family home,
// because a dispatch is not a sealed hop: the payload sits on the JetStream
// stream for its retention window and reaches the Runner's per-task child
// and every external Collection a task calls, so a native, check, rollback or
// playbook job running no SNMP method would carry the device's community or
// v3 passphrases there for no task. A template's bound SNMP credential is the
// operator's explicit choice, and ApplySNMP still delivers it to a native
// job.
func deviceSecrets(cred credential.Credential, withSNMP bool) map[string]string {
	if !withSNMP {
		cred = cred.WithSNMP(wire.SNMPCredential{})
	}
	secrets := credential.Flatten(cred)
	if len(secrets) == 0 {
		return nil
	}
	return secrets
}

// withDeviceSNMP is secrets (a template's login) with the device's own SNMP
// family added, for a job that reads it: a machine credential is a login and
// stands in for none of the device's SNMP family. Any SNMP key already there
// is replaced whole by the device's family, never merged with it.
func withDeviceSNMP(secrets map[string]string, cred credential.Credential) map[string]string {
	family := cred.SNMP().Secrets()
	if len(family) == 0 {
		return secrets
	}
	out := make(map[string]string, len(secrets)+len(family))
	for key, value := range secrets {
		out[key] = value
	}
	for _, key := range wire.SNMPKeys() {
		delete(out, key)
	}
	for key, value := range family {
		out[key] = value
	}
	return out
}

// readsSNMP reports whether a method in methods declares it reads its
// device's SNMP credential, as this process's registry or, on a Controller,
// the module catalog its composition root installed (collection.ManifestOf)
// says. A method neither knows (an external program's) reads none.
func readsSNMP(methods []string) bool {
	for _, fqcn := range methods {
		if manifest, ok := collection.ManifestOf(fqcn); ok && manifest.UsesSNMPCredential {
			return true
		}
	}
	return false
}

// rollbackReadsSNMP is readsSNMP over the methods a rollback's steps run,
// which a rollback dispatch runs in place of the runbook's tasks.
func rollbackReadsSNMP(steps []wire.RollbackStep) bool {
	methods := make([]string, 0, len(steps))
	for _, s := range steps {
		methods = append(methods, s.Method)
	}
	return readsSNMP(methods)
}
