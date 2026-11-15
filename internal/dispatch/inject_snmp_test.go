// Tests that a template's SNMP credential reaches a native dispatch beside
// the device's login, through the Worker's real fan-out and the Crawl-tier
// file store the device's own credential is read from.
package dispatch_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestWorkerGivesATemplatesSNMPBesideTheDevicesLogin: with only a
// pleiades_snmp credential bound, the device's own stored login still
// reaches the dispatch and the template's community replaces the device's
// own SNMPv3 user whole, no v3 key left beside it; bound beside a machine
// credential, the template supplies both.
func TestWorkerGivesATemplatesSNMPBesideTheDevicesLogin(t *testing.T) {
	ct, ok := managed.Type("pleiades_snmp")
	if !ok {
		t.Fatal("pleiades_snmp is not shipped")
	}
	snmp := (credtype.Credential{ID: 21, Name: "switch snmp", Type: ct, Inputs: map[string]string{"community": "template-community"}}).WithDefaults()
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, "core-switch-1", credential.Credential{
		Username: "per-device", Password: "per-device-password",
		SNMPUser: "own-user", SNMPAuthProtocol: wire.SNMPAuthSHA, SNMPAuthPassphrase: "own-auth-passphrase",
	}); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name         string
		bound        []credtype.Credential
		wantUsername string
	}{
		{"SNMP alone keeps the device's own login", []credtype.Credential{snmp}, "per-device"},
		{"SNMP beside a machine credential", []credtype.Credential{machineCredential(3, "template machine", "from-template", "template-password"), snmp}, "from-template"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			store := newTestJobStore(t)
			bus := newCapturingBus()
			device := sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)
			repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}
			worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, credential.NewLazyFileStore(dir),
				dispatch.WithCredentials(&fakeResolver{creds: tc.bound}, testInjector(t)))
			ids := make([]int, 0, len(tc.bound))
			for _, c := range tc.bound {
				ids = append(ids, c.ID)
			}
			if err := worker.HandleJobRequested(requestBoundJob(t, ctx, store, ids, nil)); err != nil {
				t.Fatalf("HandleJobRequested: %v", err)
			}
			secrets := dispatchedPayload(t, bus).Secrets
			if secrets[wire.SecretUsername] != tc.wantUsername || secrets[wire.SecretSNMPCommunity] != "template-community" {
				t.Errorf("username %q, community set %v; want %q and the template's community", secrets[wire.SecretUsername], secrets[wire.SecretSNMPCommunity] != "", tc.wantUsername)
			}
			for _, k := range []string{wire.SecretSNMPUser, wire.SecretSNMPAuthProtocol, wire.SecretSNMPAuthPassphrase} {
				if _, stale := secrets[k]; stale {
					t.Errorf("the device's own %s rode beside the template's community", k)
				}
			}
		})
	}
}
