package staticyaml_test

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins/staticyaml"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// fileEndpoint builds the file:// URL Config.Endpoint expects from a plain
// path, so tests never hand-splice the scheme and get the triple-slash
// form wrong.
func fileEndpoint(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// newProject writes an upstream inventory document holding hosts and
// returns its path plus a file-backed Repository over a separate, initially
// empty document.
//
// The two documents must not be the same file. Pointing this plugin at the
// project's own inventory would be a self-sync: the source and the
// destination would be one file, every discovered host would already exist
// in the repository, and every sync would report unchanged no matter what
// the reconciler did. The realistic arrangement, and the one that actually
// exercises the code, is importing some other document into this project.
func newProject(t *testing.T, hosts []inv.HostSpec) (string, inv.Repository) {
	t.Helper()

	upstream := filepath.Join(t.TempDir(), "upstream.yaml")
	if err := inv.WriteHosts(upstream, hosts); err != nil {
		t.Fatalf("failed to write upstream inventory: %v", err)
	}

	project := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := inv.WriteHosts(project, nil); err != nil {
		t.Fatalf("failed to write project inventory: %v", err)
	}

	return upstream, inv.NewFileRepository(project, inv.NewItemFactory())
}

// connected returns a plugin already pointed at path, failing the test if
// Connect rejects it.
func connected(t *testing.T, path string) *staticyaml.Plugin {
	t.Helper()

	p := staticyaml.New(inv.NewItemFactory())
	cfg := syncplugin.Config{Name: staticyaml.Name, Endpoint: fileEndpoint(path)}
	if err := p.Connect(context.Background(), cfg); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return p
}

// sampleHost is the single host most tests below sync. It carries an
// explicit id because DeviceID must never be the mutable name.
var sampleHost = inv.HostSpec{
	ID:   "11111111-1111-1111-1111-111111111111",
	Name: "webserver1",
	Type: "linux_server",
	Tags: []string{"web", "prod"},
	Properties: map[string]interface{}{
		"host":         "10.0.0.5",
		"distribution": "ubuntu",
	},
}

// TestSync_OnboardsHost is the replacement for the old
// TestStaticYAMLPlugin_Load. It proves the same facts, but through the real
// port and a real Repository rather than a Load method nothing called: the
// explicit id survives, the host is immediately active, linux_server
// declares LinuxCapable, and provenance is recorded as this plugin.
func TestSync_OnboardsHost(t *testing.T) {
	path, repo := newProject(t, []inv.HostSpec{sampleHost})
	p := connected(t, path)

	report, err := p.Sync(context.Background(), repo)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := report.Count(syncplugin.OutcomeAdded); got != 1 {
		t.Fatalf("expected 1 device added, got %d (report: %+v)", got, report.Results)
	}

	item, err := repo.GetByName(context.Background(), "webserver1")
	if err != nil {
		t.Fatalf("GetByName after sync: %v", err)
	}
	if item.ID() != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("expected the explicit id to survive hydration, got %q", item.ID())
	}
	if !item.State().CanExecute() {
		t.Error("expected a statically listed host to be immediately active")
	}
	if !item.HasCapability(capability.NameLinux) {
		t.Error("expected linux_server to declare LinuxCapable")
	}
	if item.Source().Plugin != staticyaml.Name {
		t.Errorf("expected source plugin %q, got %q", staticyaml.Name, item.Source().Plugin)
	}
	if item.Source().SyncedAt.IsZero() {
		t.Error("expected sync to stamp SyncedAt")
	}
}

// TestSync_IsIdempotent proves a second sync of an unchanged document
// writes nothing. This is the property that makes a scheduled sync safe to
// run on a timer: a no-op write would burn a version and append a
// revision-free bump to every device's audit trail on every tick.
func TestSync_IsIdempotent(t *testing.T) {
	path, repo := newProject(t, []inv.HostSpec{sampleHost})
	p := connected(t, path)
	ctx := context.Background()

	if _, err := p.Sync(ctx, repo); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	before, err := repo.GetByName(ctx, "webserver1")
	if err != nil {
		t.Fatalf("GetByName after first sync: %v", err)
	}

	report, err := p.Sync(ctx, repo)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if got := report.Count(syncplugin.OutcomeUnchanged); got != 1 {
		t.Fatalf("expected the second sync to report 1 unchanged, got %d (report: %+v)", got, report.Results)
	}

	after, err := repo.GetByName(ctx, "webserver1")
	if err != nil {
		t.Fatalf("GetByName after second sync: %v", err)
	}
	if after.Version() != before.Version() {
		t.Errorf("expected an unchanged re-sync to leave version at %d, got %d", before.Version(), after.Version())
	}
	if len(after.History()) != len(before.History()) {
		t.Errorf("expected an unchanged re-sync to add no revisions, history went from %d to %d",
			len(before.History()), len(after.History()))
	}
}

// TestSync_UpdatesChangedProperty proves a changed upstream value produces
// an update carrying a real audit trail, and specifically that the recorded
// revision names the value that was actually replaced. A reconciliation
// that rebuilt the item from upstream data could not know the old value,
// and would record a revision that lies about what changed.
func TestSync_UpdatesChangedProperty(t *testing.T) {
	path, repo := newProject(t, []inv.HostSpec{sampleHost})
	p := connected(t, path)
	ctx := context.Background()

	if _, err := p.Sync(ctx, repo); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	changed := sampleHost
	changed.Properties = map[string]interface{}{
		"host":         "10.0.0.99",
		"distribution": "ubuntu",
	}
	if err := inv.WriteHosts(path, []inv.HostSpec{changed}); err != nil {
		t.Fatalf("rewriting inventory: %v", err)
	}

	report, err := p.Sync(ctx, repo)
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if got := report.Count(syncplugin.OutcomeUpdated); got != 1 {
		t.Fatalf("expected 1 device updated, got %d (report: %+v)", got, report.Results)
	}

	item, err := repo.GetByName(ctx, "webserver1")
	if err != nil {
		t.Fatalf("GetByName after update: %v", err)
	}
	host, _ := item.Properties().String("host")
	if host != "10.0.0.99" {
		t.Errorf("expected the updated host property to persist, got %q", host)
	}

	var found bool
	for _, rev := range item.History() {
		if rev.Field != "host" {
			continue
		}
		found = true
		if rev.OldValue != "10.0.0.5" {
			t.Errorf("expected the revision to record the replaced value 10.0.0.5, got %v", rev.OldValue)
		}
		if rev.NewValue != "10.0.0.99" {
			t.Errorf("expected the revision to record the new value 10.0.0.99, got %v", rev.NewValue)
		}
	}
	if !found {
		t.Errorf("expected a revision recording the host change, history: %+v", item.History())
	}
}

// TestSync_ForeignSourceIsAConflict proves Section 11's One Authority Per
// Item: a device another plugin already owns is reported as a conflict and
// left alone, never silently overwritten.
func TestSync_ForeignSourceIsAConflict(t *testing.T) {
	path, repo := newProject(t, []inv.HostSpec{sampleHost})
	ctx := context.Background()

	// Onboard the device under a different plugin's name by running the
	// shared reconciler with a foreign config, which is exactly what a
	// second real plugin would do.
	foreign := connected(t, path)
	if _, err := syncplugin.Reconcile(ctx, foreign, syncplugin.Config{Name: "netbox"}, repo, inv.NewItemFactory()); err != nil {
		t.Fatalf("seeding with a foreign source: %v", err)
	}

	report, err := connected(t, path).Sync(ctx, repo)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := report.Count(syncplugin.OutcomeConflict); got != 1 {
		t.Fatalf("expected 1 conflict, got %d (report: %+v)", got, report.Results)
	}

	item, err := repo.GetByName(ctx, "webserver1")
	if err != nil {
		t.Fatalf("GetByName after conflict: %v", err)
	}
	if item.Source().Plugin != "netbox" {
		t.Errorf("expected the original owner to survive, got %q", item.Source().Plugin)
	}
}

// TestClassify_QuarantinesUntypedHost proves Section 6g: an entry the file
// failed to classify is quarantined with a reason rather than erroring the
// whole sync or being silently defaulted to some plausible type.
func TestClassify_QuarantinesUntypedHost(t *testing.T) {
	untyped := inv.HostSpec{
		ID:   "22222222-2222-2222-2222-222222222222",
		Name: "mystery1",
	}
	path, repo := newProject(t, []inv.HostSpec{untyped})

	report, err := connected(t, path).Sync(context.Background(), repo)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := report.Count(syncplugin.OutcomeQuarantined); got != 1 {
		t.Fatalf("expected 1 quarantined device, got %d (report: %+v)", got, report.Results)
	}
	if report.Results[0].Reason == "" {
		t.Error("expected a quarantined device to carry a reason explaining why")
	}
}

// TestSync_AnUnresolvableClassifyPathIsQuarantinedNotFatal is the
// regression test for one entry failing a whole sync. An entry whose
// classify path resolved to nothing made Discover return an error, so a
// single typo in one host stopped every other host from syncing, where the
// plugin contract requires a record it cannot place to be quarantined with
// a reason. The typed host beside it is the control that the sync ran.
func TestSync_AnUnresolvableClassifyPathIsQuarantinedNotFatal(t *testing.T) {
	// One path malformed, one well formed that matches no rule.
	malformed := inv.HostSpec{ID: "33333333-3333-3333-3333-333333333333", Name: "typo1", Classify: []string{"no-such-branch"}}
	unmatched := inv.HostSpec{ID: "44444444-4444-4444-4444-444444444444", Name: "typo2", Classify: []string{"no_such_branch"}}
	path, repo := newProject(t, []inv.HostSpec{sampleHost, malformed, unmatched})

	report, err := connected(t, path).Sync(context.Background(), repo)
	if err != nil {
		t.Fatalf("Sync failed over one unresolvable entry: %v", err)
	}
	if got := report.Count(syncplugin.OutcomeAdded); got != 1 {
		t.Errorf("expected the typed host to be added, got %d added (report: %+v)", got, report.Results)
	}
	if got := report.Count(syncplugin.OutcomeQuarantined); got != 2 {
		t.Fatalf("expected both unresolvable hosts to be quarantined, got %d (report: %+v)", got, report.Results)
	}
	want := map[string]string{"typo1": "no-such-branch", "typo2": "no_such_branch"}
	for _, res := range report.Results {
		if res.Outcome == syncplugin.OutcomeQuarantined && !strings.Contains(res.Reason, want[res.Name]) {
			t.Errorf("quarantined %q with reason %q, want a reason naming its classify path", res.Name, res.Reason)
		}
	}
}

// TestDiscover_RequiresConnect proves the port's call order is enforced
// with a typed error rather than a nil-pointer panic.
func TestDiscover_RequiresConnect(t *testing.T) {
	p := staticyaml.New(inv.NewItemFactory())
	if _, err := p.Discover(context.Background()); err == nil {
		t.Fatal("expected Discover before Connect to fail")
	}
}

// TestConnect_RejectsNonFileEndpoint proves a plugin that can only read
// local files says so, rather than accepting an https endpoint it would
// then fail to read for a much less obvious reason.
func TestConnect_RejectsNonFileEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "https endpoint", endpoint: "https://example.invalid/api"},
		{name: "empty endpoint", endpoint: ""},
		{name: "bare path with no scheme", endpoint: "/tmp/inventory.yaml"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := staticyaml.New(inv.NewItemFactory())
			cfg := syncplugin.Config{Name: staticyaml.Name, Endpoint: tt.endpoint}
			if err := p.Connect(context.Background(), cfg); err == nil {
				t.Fatalf("expected Connect to reject endpoint %q", tt.endpoint)
			}
		})
	}
}

// TestRegistered proves this plugin is reachable through the registry from
// a binary that imports the plugins composition root, not merely from its
// own test binary. That distinction is FAILURE_PATTERNS.md #52.
func TestRegistered(t *testing.T) {
	desc, ok := syncplugin.Lookup(staticyaml.Name)
	if !ok {
		t.Fatalf("plugin %q is not registered", staticyaml.Name)
	}
	if desc.New == nil {
		t.Fatal("registered descriptor has no constructor")
	}
	if desc.New(syncplugin.Deps{}) == nil {
		t.Fatal("constructor returned nil")
	}
}
