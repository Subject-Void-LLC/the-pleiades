// Tests that a device's own SNMP family travels with a dispatch exactly when
// a method the job runs declares it reads it (collection.Manifest.
// UsesSNMPCredential), through the Worker's real fan-out, net.snmp.get's
// manifest read from an installed module catalog as a Controller reads it
// (it registers no Collection), a real generic_snmp device and the
// Crawl-tier file store its credential is read from.
package dispatch_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// installSNMPCatalog installs a module catalog holding net.snmp.get as a
// Controller's composition root installs the generated one: reading its
// device's SNMP family, over the snmp transport.
func installSNMPCatalog(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if _, ok := collection.Lookup("net.snmp.get"); ok {
		t.Fatal("net.snmp.get is registered, so this would not test the catalog")
	}
	catalog, err := json.Marshal(map[string]collection.Manifest{"net.snmp.get": {
		Status: collection.StatusImplemented, SupportedTransports: []string{capability.TransportSNMP},
		RequiredCapabilities: []capability.Name{capability.NameSNMP}, UsesSNMPCredential: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := collection.InstallCatalog(catalog); err != nil {
		t.Fatal(err)
	}
}

// snmpRunbooks is a source holding a runbook that reads SNMP (snmp-read)
// and one that does not (ssh-only).
func snmpRunbooks(t *testing.T) runbook.Source {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"snmp-read.yaml": "id: snmp-read\ntasks:\n  - name: read the name\n    net.snmp.get:\n      oids:\n        - sysName.0\n",
		"ssh-only.yaml":  "id: ssh-only\ntasks:\n  - name: step\n    fqcn: ios_backup\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// onboardedAgent is a generic_snmp device as onboarding leaves it: active,
// its discovery granting SNMPCapable and bound to its address.
func onboardedAgent(t *testing.T) pkginventory.InventoryItem {
	t.Helper()
	props := map[string]pkginventory.PropertyValue{generic.HostProperty: "10.0.0.40"}
	d := pkginventory.Discovery{Protocol: "snmp", Capabilities: []capability.Name{capability.NameSNMP}, Binding: generic.Binding(generic.TypeSNMP, pkginventory.NewProperties(props))}
	props[pkginventory.DiscoveredProperty] = d.Property()
	item, err := generic.NewSNMP(record.Record{ID: "dev-id-snmp", Name: "pdu-1", Type: generic.TypeSNMP, Properties: props, State: pkginventory.StateActive})
	if err != nil {
		t.Fatal(err)
	}
	if !item.HasCapability(capability.NameSNMP) {
		t.Fatal("the onboarded agent does not hold SNMPCapable")
	}
	return item
}

// TestWorkerCarriesTheDevicesOwnSNMPForAJobThatReadsIt: a job whose runbook
// names net.snmp.get carries the device's own SNMP family, a device reached
// by SNMP alone included; and a login device's dispatch of a job naming no
// such method carries its login and none of its SNMP family.
func TestWorkerCarriesTheDevicesOwnSNMPForAJobThatReadsIt(t *testing.T) {
	snmpOnly := credential.Credential{SNMPUser: "monitor", SNMPAuthProtocol: wire.SNMPAuthSHA256, SNMPAuthPassphrase: "own-auth-passphrase"}
	withLogin := snmpOnly
	withLogin.Username, withLogin.Password = "per-device", "per-device-password"

	installSNMPCatalog(t)
	t.Run("an SNMP-only device, for a job that reads SNMP", func(t *testing.T) {
		ctx := t.Context()
		store := newTestJobStore(t)
		bus := newCapturingBus()
		repo := &fakeRepository{Devices: []pkginventory.InventoryItem{onboardedAgent(t)}}
		worker := dispatch.NewWorker(store, repo, snmpRunbooks(t), bus, credential.NewLazyFileStore(storeDeviceCredential(t, "pdu-1", snmpOnly)))
		if err := worker.HandleJobRequested(requestJob(t, ctx, store, "snmp-read", "pdus")); err != nil {
			t.Fatalf("HandleJobRequested: %v", err)
		}
		secrets := dispatchedPayload(t, bus).Secrets
		if secrets[wire.SecretSNMPUser] != "monitor" || secrets[wire.SecretSNMPAuthPassphrase] != "own-auth-passphrase" || secrets[wire.SecretSNMPAuthProtocol] != "sha256" {
			t.Errorf("the dispatch carries %d secrets, want the device's SNMP family", len(secrets))
		}
	})

	t.Run("a login device, for a job that reads no SNMP", func(t *testing.T) {
		ctx := t.Context()
		store := newTestJobStore(t)
		bus := newCapturingBus()
		repo := &fakeRepository{Devices: []pkginventory.InventoryItem{sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)}}
		worker := dispatch.NewWorker(store, repo, snmpRunbooks(t), bus, credential.NewLazyFileStore(storeDeviceCredential(t, "core-switch-1", withLogin)))
		if err := worker.HandleJobRequested(requestJob(t, ctx, store, "ssh-only", "routers")); err != nil {
			t.Fatalf("HandleJobRequested: %v", err)
		}
		secrets := dispatchedPayload(t, bus).Secrets
		if secrets[wire.SecretUsername] != "per-device" {
			t.Errorf("the login did not travel")
		}
		for _, key := range wire.SNMPKeys() {
			if _, carried := secrets[key]; carried {
				t.Errorf("a job reading no SNMP carries the device's %s", key)
			}
		}
	})
}
