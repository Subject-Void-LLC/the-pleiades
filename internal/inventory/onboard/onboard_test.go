// Tests for the onboarding orchestrator, over the real file inventory and
// a real HTTPS server: the lifecycle it drives, what it records, and what
// it refuses.
package onboard

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// apiServer is a real HTTPS API accepting one basic credential, whose
// answer to a refused one is 401.
func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, pass, ok := r.BasicAuth(); !ok || user != "api" || pass != "api-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Server", "stand-in/1.0")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newRepo returns a file inventory in a temporary project holding one
// device of deviceType with props, created as add-host creates one.
func newRepo(t *testing.T, name, deviceType string, props map[string]inventory.PropertyValue) (inv.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, inv.DefaultInventoryFilename)
	factory := inv.NewItemFactory()
	repo := inv.NewFileRepository(path, factory)
	item, err := factory.Build(record.Record{ID: inventory.DeviceID(name + "-id"), Name: name, Type: deviceType, Properties: props, State: record.InitialState(deviceType)})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return repo, dir
}

// staticSecrets resolves every device to creds.
func staticSecrets(creds map[string]string) SecretsFunc {
	return func(context.Context, inventory.InventoryItem) (map[string]string, error) { return creds, nil }
}

// caPEM is srv's certificate as the PEM a device's tls_ca_pem pins, which
// is how an operator trusts a device's private authority.
func caPEM(srv *httptest.Server) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}

var fixedNow = func() time.Time { return time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC) }

// TestOnboard_DiscoveredToActive walks a new generic_http device through
// discovered, onboarding and active, and checks each thing onboarding
// records: the capability, now held; the discovery kept in the state file
// and not in hosts.yaml; and a revision for each change. A second run
// proves nothing new and writes nothing.
func TestOnboard_DiscoveredToActive(t *testing.T) {
	srv := apiServer(t)
	repo, dir := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: caPEM(srv), generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic,
	})
	ctx := context.Background()
	before, _ := repo.GetByName(ctx, "api1")
	if before.State() != inventory.StateDiscovered || before.HasCapability(capability.NameHTTPAPI) {
		t.Fatalf("a new device is %s holding HTTPAPICapable=%v, want discovered without it", before.State(), before.HasCapability(capability.NameHTTPAPI))
	}

	res, err := onboardWith(ctx, repo, "api1", staticSecrets(map[string]string{"username": "api", "password": "api-secret"}), fixedNow, Lookup)
	if err != nil {
		t.Fatal(err)
	}
	if res.PreviousState != "discovered" || res.State != "active" || !res.Changed || res.Facts["status"] != "200" || res.Facts["server"] != "stand-in/1.0" {
		t.Errorf("result %+v", res)
	}
	after, err := repo.GetByName(ctx, "api1")
	if err != nil {
		t.Fatal(err)
	}
	if after.State() != inventory.StateActive || !after.HasCapability(capability.NameHTTPAPI) {
		t.Errorf("after onboarding: %s, HTTPAPICapable=%v", after.State(), after.HasCapability(capability.NameHTTPAPI))
	}
	fields := map[string]int{}
	for _, rev := range after.History() {
		fields[rev.Field]++
	}
	if fields[inventory.DiscoveredProperty] != 1 || fields["state"] != 2 {
		t.Errorf("revisions by field %v, want one discovery and two state changes", fields)
	}
	hosts, _ := os.ReadFile(filepath.Join(dir, inv.DefaultInventoryFilename))
	if strings.Contains(string(hosts), inventory.DiscoveredProperty) {
		t.Errorf("the discovery was written to hosts.yaml:\n%s", hosts)
	}

	again, err := onboardWith(ctx, repo, "api1", staticSecrets(map[string]string{"username": "api", "password": "api-secret"}), fixedNow, Lookup)
	if err != nil {
		t.Fatal(err)
	}
	reread, _ := repo.GetByName(ctx, "api1")
	if again.Changed || reread.Version() != after.Version() {
		t.Errorf("a re-probe that learned nothing wrote: changed=%v, version %d -> %d", again.Changed, after.Version(), reread.Version())
	}
}

// TestOnboard_FailedProbeLeavesOnboarding: a refused credential proves
// nothing, so the device stays onboarding, holds no discovered
// capability, and the result says why.
func TestOnboard_FailedProbeLeavesOnboarding(t *testing.T) {
	srv := apiServer(t)
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: caPEM(srv), generic.BaseURLProperty: srv.URL, generic.HTTPAuthProperty: httpapi.AuthBasic,
	})
	res, err := onboardWith(context.Background(), repo, "api1", staticSecrets(map[string]string{"username": "api", "password": "wrong"}), fixedNow, Lookup)
	if err == nil || !strings.Contains(res.Error, "refused the credential") {
		t.Fatalf("err %v, result %+v", err, res)
	}
	item, _ := repo.GetByName(context.Background(), "api1")
	if item.State() != inventory.StateOnboarding || item.HasCapability(capability.NameHTTPAPI) {
		t.Errorf("after a failed probe: %s, HTTPAPICapable=%v", item.State(), item.HasCapability(capability.NameHTTPAPI))
	}
	if _, ok := item.Properties().Raw()[inventory.DiscoveredProperty]; ok {
		t.Error("a failed probe recorded a discovery")
	}
}

// TestOnboard_RefusesAVendorType: a linux_server's capabilities come from
// its Go type, so there is nothing to onboard.
func TestOnboard_RefusesAVendorType(t *testing.T) {
	repo, _ := newRepo(t, "web1", "linux_server", map[string]inventory.PropertyValue{"host": "127.0.0.1"})
	if _, err := Onboard(context.Background(), repo, "web1", staticSecrets(nil), fixedNow); !errors.Is(err, ErrNotOnboarded) {
		t.Fatalf("err %v, want ErrNotOnboarded", err)
	}
}

// countingProber records whether it was asked, and grants what it is
// given.
type countingProber struct {
	calls  *int
	grants []capability.Name
	facts  map[string]any
}

func (countingProber) Protocol() string { return "http" }

func (p countingProber) Probe(context.Context, inventory.InventoryItem, map[string]string) (Probed, error) {
	*p.calls++
	return Probed{Capabilities: p.grants, Facts: p.facts}, nil
}

func only(p Prober) func(string) (Prober, bool) {
	return func(string) (Prober, bool) { return p, true }
}

// TestOnboard_LeavesAdministratorStatesAlone: a quarantined device is not
// probed at all.
func TestOnboard_LeavesAdministratorStatesAlone(t *testing.T) {
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://api.invalid"})
	ctx := context.Background()
	item, _ := repo.GetByName(ctx, "api1")
	item.(interface {
		ChangeState(inventory.LifecycleState) bool
	}).ChangeState(inventory.StateQuarantined)
	if err := repo.Save(ctx, item); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if _, err := onboardWith(ctx, repo, "api1", staticSecrets(nil), fixedNow, only(countingProber{calls: &calls})); err == nil || calls != 0 {
		t.Fatalf("err %v after %d probes, want a refusal before any", err, calls)
	}
}

// TestOnboard_ProberCannotGrantAForeignCapability: a prober reporting a
// capability its type may not hold is refused, and nothing is recorded.
func TestOnboard_ProberCannotGrantAForeignCapability(t *testing.T) {
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://api.invalid"})
	calls := 0
	p := countingProber{calls: &calls, grants: []capability.Name{capability.NameHTTPAPI, capability.NameApt}}
	if _, err := onboardWith(context.Background(), repo, "api1", staticSecrets(nil), fixedNow, only(p)); err == nil || !strings.Contains(err.Error(), "AptCapable") {
		t.Fatalf("err %v, want a refusal naming AptCapable", err)
	}
	item, _ := repo.GetByName(context.Background(), "api1")
	if _, ok := item.Properties().Raw()[inventory.DiscoveredProperty]; ok || item.State() == inventory.StateActive {
		t.Errorf("a refused grant was recorded: state %s", item.State())
	}
}

// TestOnboard_ReprobeRecordsWhatChanged: a re-probe that learns a new fact
// writes one more discovery revision; the device stays active.
func TestOnboard_ReprobeRecordsWhatChanged(t *testing.T) {
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://api.invalid"})
	ctx := context.Background()
	calls := 0
	first := countingProber{calls: &calls, grants: []capability.Name{capability.NameHTTPAPI}, facts: map[string]any{"server": "one"}}
	if _, err := onboardWith(ctx, repo, "api1", staticSecrets(nil), fixedNow, only(first)); err != nil {
		t.Fatal(err)
	}
	second := first
	second.facts = map[string]any{"server": "two"}
	res, err := onboardWith(ctx, repo, "api1", staticSecrets(nil), fixedNow, only(second))
	if err != nil || !res.Changed || res.State != "active" {
		t.Fatalf("err %v, result %+v", err, res)
	}
	item, _ := repo.GetByName(ctx, "api1")
	n := 0
	for _, rev := range item.History() {
		if rev.Field == inventory.DiscoveredProperty {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d discovery revisions, want 2", n)
	}
}

// TestOnboard_DatabaseInventory onboards a device stored in the database
// inventory the Controller uses, where the discovery lives in the
// properties column: it survives the JSON round trip, grants the
// capability after reload, and a re-probe that learns nothing writes
// nothing.
func TestOnboard_DatabaseInventory(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:onboarddb?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	factory := inv.NewItemFactory()
	repo := inv.NewEntRepository(client, factory)
	ctx := context.Background()
	item, err := factory.Build(record.Record{ID: "grpc1-id", Name: "grpc1", Type: generic.TypeGRPC,
		Properties: map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "10.0.0.9:50051"}, State: record.InitialState(generic.TypeGRPC)})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p := countingProber{calls: &calls, grants: []capability.Name{capability.NameGRPC}, facts: map[string]any{"services": []any{"a.B"}, "health": "SERVING"}}
	if _, err := onboardWith(ctx, repo, "grpc1", staticSecrets(nil), fixedNow, only(p)); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetByName(ctx, "grpc1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.State() != inventory.StateActive || !stored.HasCapability(capability.NameGRPC) {
		t.Fatalf("stored: %s, GRPCCapable=%v", stored.State(), stored.HasCapability(capability.NameGRPC))
	}
	again, err := onboardWith(ctx, repo, "grpc1", staticSecrets(nil), fixedNow, only(p))
	if err != nil || again.Changed {
		t.Fatalf("re-probe: err %v, changed %v", err, again.Changed)
	}
}
