//go:build integration

// This file is the reproducible real-target proof PLAN.md Section 6a's sync
// plugins and IMPLEMENTATION.md Phase 38's Release Gate both require: the
// catalyst_center plugin talking to an actual Cisco Catalyst Center, not a
// fixture replaying one.
//
// It is gated twice on purpose. The integration build tag keeps it out of
// the ordinary `go test ./...`, and PLEIADES_E2E_DNAC keeps it out even of
// an integration run unless someone opted in. CI must never depend on a
// third-party sandbox being reachable, and a test that fails because Cisco
// took their sandbox down teaches everyone to ignore failures.
//
// Run it with:
//
//	PLEIADES_E2E_DNAC=1 go test -tags integration -race ./tests/e2e/ -run Catalyst
//
// Credentials come from PLEIADES_E2E_DNAC_USER and PLEIADES_E2E_DNAC_PASS,
// defaulting to the public DevNet sandbox account documented in
// .SPECIFICATION/.CATALYST_CENTER_PLUGINS.md. Those are published
// credentials for a public read-only sandbox, not a secret.
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/catalystcenter"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginv "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"go.uber.org/goleak"
)

const (
	dnacEnvGate  = "PLEIADES_E2E_DNAC"
	dnacEndpoint = "https://sandboxdnac.cisco.com"
)

// staticStore is a credential.Store returning one fixed credential. The
// real store is an encrypted file store keyed by a passphrase, which this
// test has no business standing up: what is under test is the plugin's use
// of the Store port, not the store's own encryption.
type staticStore struct {
	cred credential.Credential
}

func (s staticStore) Lookup(_ context.Context, _ string) (credential.Credential, error) {
	return s.cred, nil
}

// requireDNAC skips unless the opt-in gate is set, and returns the config
// and credential store to connect with.
func requireDNAC(t *testing.T) (syncplugin.Config, credential.Store) {
	t.Helper()

	if os.Getenv(dnacEnvGate) == "" {
		t.Skipf("set %s=1 to run the live Catalyst Center end-to-end test", dnacEnvGate)
	}

	user := os.Getenv("PLEIADES_E2E_DNAC_USER")
	if user == "" {
		user = "devnetuser"
	}
	pass := os.Getenv("PLEIADES_E2E_DNAC_PASS")
	if pass == "" {
		pass = "Cisco123!"
	}

	endpoint := os.Getenv("PLEIADES_E2E_DNAC_ENDPOINT")
	if endpoint == "" {
		endpoint = dnacEndpoint
	}

	cfg := syncplugin.Config{
		Name:     catalystcenter.Name,
		Endpoint: endpoint,
		ReadOnly: true,
		// A page size well below the fleet size on purpose: the sandbox has
		// four devices, so a size of two forces the multi-page path to run.
		// A single-page test would never exercise paging at all.
		PageSize: 2,
		// The DevNet sandbox terminates TLS on a Kong ingress whose
		// certificate is issued for dragonfly-kong-frontend and friends,
		// not for sandboxdnac.cisco.com, so verification genuinely cannot
		// succeed against it. This is the appliance-with-a-wrong-certificate
		// case Config.InsecureSkipVerify exists to model, and setting it
		// here rather than disabling verification inside the client is the
		// whole point: it stays visible in configuration a reviewer can
		// grep for.
		InsecureSkipVerify: true,
	}
	return cfg, staticStore{credential.Credential{Username: user, Password: pass}}
}

// newProjectRepo builds a file-backed Repository over an empty inventory
// document, which is the Crawl-tier arrangement a user running this for the
// first time actually has.
func newProjectRepo(t *testing.T) inv.Repository {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hosts.yaml")
	if err := inv.WriteHosts(path, nil); err != nil {
		t.Fatalf("creating empty project inventory: %v", err)
	}
	return inv.NewFileRepository(path, inv.NewItemFactory())
}

// TestCatalystCenter_LiveSync is the whole pipeline against the real
// controller: authenticate, page the device inventory, classify each device
// through the Section 6d rule tree, and reconcile into a real Repository.
func TestCatalystCenter_LiveSync(t *testing.T) {
	defer goleak.VerifyNone(t)

	cfg, store := requireDNAC(t)
	ctx := context.Background()
	repo := newProjectRepo(t)

	plugin := catalystcenter.New(catalystcenter.WithCredentialStore(store))
	defer func() { _ = plugin.Close() }()
	if err := plugin.Connect(ctx, cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	report, err := plugin.Sync(ctx, repo)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if report.Total() == 0 {
		t.Fatal("sync discovered nothing; a working controller always reports at least itself")
	}
	if got := report.Count(syncplugin.OutcomeAdded); got == 0 {
		t.Fatalf("expected a first sync into an empty project to add devices, got none (report: %+v)", report.Results)
	}
	if got := report.Count(syncplugin.OutcomeConflict); got != 0 {
		t.Errorf("expected no conflicts syncing into an empty project, got %d", got)
	}
	for _, res := range report.Results {
		if res.Outcome == syncplugin.OutcomeQuarantined {
			t.Errorf("device %s was quarantined: %s", res.Name, res.Reason)
		}
	}

	assertControllerOnboarded(t, ctx, repo, cfg.Endpoint)
	assertManagedSwitches(t, ctx, repo)
}

// assertControllerOnboarded proves the Catalyst Center itself landed in
// inventory as a targetable device, which is what makes the net.catalyst.*
// methods runnable without a separate manual add-host step.
func assertControllerOnboarded(t *testing.T, ctx context.Context, repo inv.Repository, endpoint string) {
	t.Helper()

	item, err := repo.GetByName(ctx, "sandboxdnac.cisco.com")
	if err != nil {
		t.Fatalf("the controller itself was not onboarded: %v", err)
	}
	if !item.HasCapability(capability.NameCatalystAPI) {
		t.Errorf("controller does not declare %s, capabilities: %v", capability.NameCatalystAPI, item.Capabilities())
	}
	if got, _ := item.Properties().String("catalyst_base_url"); got != endpoint {
		t.Errorf("controller base URL property = %q, want %q", got, endpoint)
	}
	if item.Source().Plugin != catalystcenter.Name {
		t.Errorf("controller source = %q, want %q", item.Source().Plugin, catalystcenter.Name)
	}
}

// assertManagedSwitches proves the managed devices classified as switches,
// carry the properties the rest of the codebase reads, and landed
// simulate-locked rather than active.
func assertManagedSwitches(t *testing.T, ctx context.Context, repo inv.Repository) {
	t.Helper()

	it, err := repo.GetGroup(ctx, pkginv.Selector{})
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	defer func() { _ = it.Close() }()

	var switches int
	for it.Next(ctx) {
		item := it.Item()
		role, _ := item.Properties().String("catalyst_role")
		if role != "managed_device" {
			continue
		}
		switches++

		if !item.HasCapability(capability.NameCiscoIOS) {
			t.Errorf("%s does not declare %s", item.Name(), capability.NameCiscoIOS)
		}
		// The dispatcher reads the ip property and the SSH transport reads
		// host. A device missing either is in inventory but unreachable,
		// which is worse than not being there.
		if ip, _ := item.Properties().String("ip"); ip == "" {
			t.Errorf("%s has no ip property", item.Name())
		}
		if host, _ := item.Properties().String("host"); host == "" {
			t.Errorf("%s has no host property", item.Name())
		}
		// Read-only source means simulate-locked, per Section 9: Pleiades
		// has only read about this device and never authenticated to it
		// directly, so it must not accept real work until promoted.
		if item.State() != pkginv.StateSimulateLocked {
			t.Errorf("%s state = %v, want %v", item.Name(), item.State(), pkginv.StateSimulateLocked)
		}
		if item.State().CanExecute() {
			t.Errorf("%s reports it can execute, but it was imported read-only", item.Name())
		}
	}
	if err := it.Error(); err != nil {
		t.Fatalf("iterating synced inventory: %v", err)
	}
	if switches == 0 {
		t.Error("no managed devices reached inventory")
	}
}

// TestCatalystCenter_LiveResyncIsIdempotent proves a second sync of an
// unchanged controller writes nothing. This is the property that makes a
// scheduled sync safe to run on a timer: a no-op write would burn a version
// and append a revision-free bump to every device's audit trail on every
// tick.
func TestCatalystCenter_LiveResyncIsIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t)

	cfg, store := requireDNAC(t)
	ctx := context.Background()
	repo := newProjectRepo(t)

	plugin := catalystcenter.New(catalystcenter.WithCredentialStore(store))
	defer func() { _ = plugin.Close() }()
	if err := plugin.Connect(ctx, cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	first, err := plugin.Sync(ctx, repo)
	if err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	second, err := plugin.Sync(ctx, repo)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	if second.Total() != first.Total() {
		t.Errorf("second sync saw %d devices, first saw %d", second.Total(), first.Total())
	}
	if got := second.Count(syncplugin.OutcomeAdded); got != 0 {
		t.Errorf("second sync added %d devices, want 0", got)
	}
	if got := second.Count(syncplugin.OutcomeUnchanged); got != second.Total() {
		t.Errorf("second sync reported %d of %d unchanged, want all of them (report: %+v)",
			got, second.Total(), second.Results)
	}
}

// TestCatalystCenter_LiveRejectsBadCredential proves a wrong password fails
// at Connect with a clear error rather than surfacing later as an empty
// inventory. A sync that silently discovered zero devices would be
// indistinguishable from a controller that manages none.
func TestCatalystCenter_LiveRejectsBadCredential(t *testing.T) {
	defer goleak.VerifyNone(t)

	cfg, _ := requireDNAC(t)
	bad := staticStore{credential.Credential{Username: "devnetuser", Password: "definitely-not-the-password"}}

	plugin := catalystcenter.New(catalystcenter.WithCredentialStore(bad))
	defer func() { _ = plugin.Close() }()

	if err := plugin.Connect(context.Background(), cfg); err == nil {
		t.Fatal("expected Connect to fail with a wrong password")
	}
}
