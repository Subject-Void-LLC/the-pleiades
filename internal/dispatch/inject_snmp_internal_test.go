// Tests of where a template's SNMP credential does not go (a playbook job, a
// device that cannot be logged into, a dispatch binding none) and of when a
// device's own SNMP family travels.
package dispatch

import (
	"encoding/json"
	"maps"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// snmpArtifact renders a pleiades_snmp v3 user through the real injector.
func snmpArtifact(t *testing.T) credtype.Artifact {
	t.Helper()
	in, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := managed.Type("pleiades_snmp")
	art, err := in.Inject([]credtype.Credential{(credtype.Credential{ID: 4, Name: "v3", Type: ct, Inputs: map[string]string{
		"username": "monitor", "auth_protocol": "sha256", "auth_passphrase": "auth-pass-phrase",
	}}).WithDefaults()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return art
}

// TestApplySNMP_GoesOnlyToANativeLogin: a native dispatch to a device that
// can be logged into gets the user with its login kept and the caller's map
// untouched; a playbook job, a cluster, and an artifact with no SNMP
// credential leave the secrets as they were.
func TestApplySNMP_GoesOnlyToANativeLogin(t *testing.T) {
	art := snmpArtifact(t)
	own := map[string]string{wire.SecretUsername: "ops", wire.SecretPassword: "own-password", wire.SecretSNMPCommunity: "own-community-1"}
	before := maps.Clone(own)

	native := wire.DispatchPayload{Kind: runbook.Kind, DeviceType: "linux_server", Secrets: own}
	ApplySNMP(&native, art, loginDevice())
	if native.Secrets[wire.SecretPassword] != "own-password" || native.Secrets[wire.SecretSNMPUser] != "monitor" ||
		native.Secrets[wire.SecretSNMPAuthPassphrase] != "auth-pass-phrase" || native.Secrets[wire.SecretSNMPCommunity] != "" {
		t.Errorf("a native login's secrets = %v keys, want the login kept and the user in place of the community", len(native.Secrets))
	}
	if !maps.Equal(own, before) {
		t.Error("ApplySNMP changed the caller's map")
	}

	for name, payload := range map[string]wire.DispatchPayload{
		"a playbook job": {Kind: playbook.Kind, DeviceType: "linux_server", Secrets: maps.Clone(before)},
		"a cluster":      {Kind: runbook.Kind, DeviceType: kubernetes.TypeCluster, Secrets: maps.Clone(before)},
	} {
		device := loginDevice()
		if payload.DeviceType == kubernetes.TypeCluster {
			device = clusterDevice()
		}
		ApplySNMP(&payload, art, device)
		if !maps.Equal(payload.Secrets, before) {
			t.Errorf("%s was given the template's SNMP credential", name)
		}
	}
	untouched := wire.DispatchPayload{Kind: runbook.Kind, DeviceType: "linux_server", Secrets: maps.Clone(before)}
	ApplySNMP(&untouched, credtype.Artifact{}, loginDevice())
	if !maps.Equal(untouched.Secrets, before) {
		t.Error("an artifact with no SNMP credential changed the secrets")
	}
}

// TestDeviceSNMP_TheFamilyTravelsOnlyForAJobThatReadsIt: deviceSecrets keeps
// the SNMP family exactly when asked; withDeviceSNMP adds the device's family
// beside a template's login, replacing any SNMP key there whole; and the rule
// a job is held to is the manifest's flag, its rollback steps' for a
// rollback.
func TestDeviceSNMP_TheFamilyTravelsOnlyForAJobThatReadsIt(t *testing.T) {
	cred := credential.Credential{Username: "ops", Password: "own-password", SNMPCommunity: "own-community-1"}
	if got := deviceSecrets(cred, false); got[wire.SecretSNMPCommunity] != "" || got[wire.SecretPassword] != "own-password" {
		t.Errorf("deviceSecrets without SNMP = %d keys, want the login alone", len(got))
	}
	if got := deviceSecrets(cred, true); got[wire.SecretSNMPCommunity] != "own-community-1" || got[wire.SecretPassword] != "own-password" {
		t.Errorf("deviceSecrets with SNMP = %d keys, want the login and the community", len(got))
	}
	if got := deviceSecrets(credential.Credential{SNMPCommunity: "own-community-1"}, false); got != nil {
		t.Errorf("an SNMP-only credential for a job reading none = %d keys, want nil", len(got))
	}

	template := map[string]string{wire.SecretUsername: "template-login", wire.SecretPassword: "template-password", wire.SecretSNMPUser: "stale"}
	before := maps.Clone(template)
	got := withDeviceSNMP(template, cred)
	if got[wire.SecretUsername] != "template-login" || got[wire.SecretSNMPCommunity] != "own-community-1" || got[wire.SecretSNMPUser] != "" {
		t.Errorf("withDeviceSNMP = %d keys, want the template's login and the device's community alone", len(got))
	}
	if !maps.Equal(template, before) {
		t.Error("withDeviceSNMP changed the caller's map")
	}
	if got := withDeviceSNMP(template, credential.Credential{Username: "ops"}); !maps.Equal(got, template) {
		t.Error("a device with no SNMP family changed the secrets")
	}

	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	catalog, err := json.Marshal(map[string]collection.Manifest{
		"net.snmp.get": {Status: collection.StatusImplemented, UsesSNMPCredential: true},
		"net.snmp.set": {Status: collection.StatusImplemented, UsesSNMPCredential: true},
		"net.ios.ping": {Status: collection.StatusImplemented},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	if !readsSNMP([]string{"ios_backup", "net.snmp.get"}) || readsSNMP([]string{"net.ios.ping", "acme.unknown.method"}) {
		t.Error("readsSNMP does not follow the manifest's UsesSNMPCredential")
	}
	if !rollbackReadsSNMP([]wire.RollbackStep{{Method: "net.snmp.set"}}) || rollbackReadsSNMP([]wire.RollbackStep{{Method: "ssh_exec"}}) {
		t.Error("rollbackReadsSNMP does not follow its steps' methods")
	}
}

// TestApplySNMP_ReachesADeviceReachedBySNMPAlone: an onboarded generic_snmp,
// which takes no login, is given the template's SNMP credential; one not yet
// onboarded, holding no SNMPCapable, is not.
func TestApplySNMP_ReachesADeviceReachedBySNMPAlone(t *testing.T) {
	art := snmpArtifact(t)
	props := map[string]pkginventory.PropertyValue{generic.HostProperty: "10.0.0.41"}
	fresh, err := generic.NewSNMP(record.Record{Name: "pdu", Type: generic.TypeSNMP, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	payload := wire.DispatchPayload{Kind: runbook.Kind, DeviceType: generic.TypeSNMP}
	ApplySNMP(&payload, art, fresh)
	if len(payload.Secrets) != 0 {
		t.Error("a device holding no SNMPCapable was given the template's SNMP credential")
	}
	bound := map[string]pkginventory.PropertyValue{generic.HostProperty: "10.0.0.41"}
	d := pkginventory.Discovery{Protocol: "snmp", Capabilities: []capability.Name{capability.NameSNMP}, Binding: generic.Binding(generic.TypeSNMP, pkginventory.NewProperties(bound))}
	bound[pkginventory.DiscoveredProperty] = d.Property()
	onboarded, err := generic.NewSNMP(record.Record{Name: "pdu", Type: generic.TypeSNMP, Properties: bound})
	if err != nil {
		t.Fatal(err)
	}
	ApplySNMP(&payload, art, onboarded)
	if payload.Secrets[wire.SecretSNMPUser] != "monitor" {
		t.Errorf("an onboarded SNMP device was given %d secrets, want the template's user", len(payload.Secrets))
	}
}
