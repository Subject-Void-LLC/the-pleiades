// Tests of where a template's SNMP credential does not go: a playbook job, a
// device that cannot be logged into, and a dispatch binding none.
package dispatch

import (
	"maps"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
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
