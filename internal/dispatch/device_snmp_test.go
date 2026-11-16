// Tests that a device's own SNMP family never rides a dispatch, through the
// Worker's real fan-out and the Crawl-tier file store a device's own
// credential is read from.
package dispatch_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// storeDeviceCredential saves cred for device in a fresh Crawl-tier file
// store and returns the store's directory.
func storeDeviceCredential(t *testing.T, device string, cred credential.Credential) string {
	t.Helper()
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveFileStore(dir, key, device, cred); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestWorkerKeepsTheDevicesOwnSNMPOffEveryDispatch: a device whose stored
// credential holds a login and an SNMP community, with no machine
// credential bound, gets its login on the dispatch and none of its SNMP
// keys, on a native job and a playbook job alike; a device whose credential
// is SNMP alone gets no secrets at all. Nothing on a Runner reads them until
// Phase 97, and a payload sits on the JetStream stream and reaches the
// Runner's child and every external Collection.
func TestWorkerKeepsTheDevicesOwnSNMPOffEveryDispatch(t *testing.T) {
	login := credential.Credential{Username: "per-device", Password: "per-device-password"}
	withSNMP := login
	withSNMP.SNMPCommunity = "device-own-community"
	snmpOnly := credential.Credential{SNMPUser: "monitor", SNMPAuthProtocol: wire.SNMPAuthSHA256, SNMPAuthPassphrase: "own-auth-passphrase"}

	// noSNMP fails the test for any SNMP key on secrets.
	noSNMP := func(t *testing.T, secrets map[string]string) {
		t.Helper()
		for _, key := range wire.SNMPKeys() {
			if _, carried := secrets[key]; carried {
				t.Errorf("the dispatch carries the device's own %s", key)
			}
		}
	}

	t.Run("a native job carries the login alone", func(t *testing.T) {
		ctx := t.Context()
		store := newTestJobStore(t)
		bus := newCapturingBus()
		repo := &fakeRepository{Devices: []pkginventory.InventoryItem{sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)}}
		worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus,
			credential.NewLazyFileStore(storeDeviceCredential(t, "core-switch-1", withSNMP)))
		if err := worker.HandleJobRequested(requestJob(t, ctx, store, "pb-1", "routers")); err != nil {
			t.Fatalf("HandleJobRequested: %v", err)
		}
		secrets := dispatchedPayload(t, bus).Secrets
		if secrets[wire.SecretUsername] != "per-device" || secrets[wire.SecretPassword] != "per-device-password" {
			t.Errorf("the device's own login did not reach the dispatch: username %q", secrets[wire.SecretUsername])
		}
		noSNMP(t, secrets)
	})

	t.Run("a playbook job carries the login alone", func(t *testing.T) {
		store := newTestJobStore(t)
		bus := newCapturingBus()
		repo := &capturingRepository{fakeRepository: &fakeRepository{Devices: oneDevice()}}
		worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus,
			credential.NewLazyFileStore(storeDeviceCredential(t, "edge-01", withSNMP)),
			dispatch.WithSetStore(fakeSetStore{set: inventory.Set{ID: 4, DeviceIDs: []int{21}}}),
			dispatch.WithDefinitionSource("playbook", dispatch.NewPlaybookDefinitionSource(staticPlaybooks{"site"})))
		launchPlaybookJob(t, store, worker, "site")
		payload := dispatchedPayload(t, bus)
		if payload.Kind != "playbook" || payload.Secrets[wire.SecretUsername] != "per-device" {
			t.Errorf("kind %q, username %q: want a playbook dispatch carrying the device's login", payload.Kind, payload.Secrets[wire.SecretUsername])
		}
		noSNMP(t, payload.Secrets)
	})

	t.Run("an SNMP-only device carries nothing", func(t *testing.T) {
		ctx := t.Context()
		store := newTestJobStore(t)
		bus := newCapturingBus()
		repo := &fakeRepository{Devices: []pkginventory.InventoryItem{sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)}}
		worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus,
			credential.NewLazyFileStore(storeDeviceCredential(t, "core-switch-1", snmpOnly)))
		if err := worker.HandleJobRequested(requestJob(t, ctx, store, "pb-1", "routers")); err != nil {
			t.Fatalf("HandleJobRequested: %v", err)
		}
		if secrets := dispatchedPayload(t, bus).Secrets; len(secrets) != 0 {
			t.Errorf("an SNMP-only device's dispatch carries %d secrets, want none", len(secrets))
		}
	})
}
