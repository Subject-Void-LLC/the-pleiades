package syncplugin_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// These tests drive Reconcile directly with a controllable plugin, so every
// outcome and every failure path is reachable. The cross-plugin conformance
// suite in internal/inventory/plugins proves two real plugins agree; this
// proves the shared driver they both delegate to behaves correctly in cases
// a real upstream would be awkward to force.

// scriptedPlugin is a Plugin whose behavior each test dictates. It is not a
// mock of an interface under test: Reconcile is what is under test, and
// this is the input it consumes.
type scriptedPlugin struct {
	records      []record.Record
	classify     func(record.Record) (syncplugin.Classification, error)
	discoverErr  error
	iterErr      error
	closed       bool
	discoverCall int
}

func (p *scriptedPlugin) Connect(context.Context, syncplugin.Config) error { return nil }

func (p *scriptedPlugin) Discover(context.Context) (syncplugin.RecordIterator, error) {
	p.discoverCall++
	if p.discoverErr != nil {
		return nil, p.discoverErr
	}
	return &scriptedIterator{records: p.records, pos: -1, err: p.iterErr, plugin: p}, nil
}

func (p *scriptedPlugin) Classify(_ context.Context, rec record.Record) (syncplugin.Classification, error) {
	if p.classify != nil {
		return p.classify(rec)
	}
	return syncplugin.Classification{
		Type:  "linux_server",
		State: inventory.StateActive,
	}, nil
}

func (p *scriptedPlugin) Sync(ctx context.Context, repo inv.Repository) (syncplugin.Reconciliation, error) {
	return syncplugin.Reconcile(ctx, p, syncplugin.Config{Name: "scripted"}, repo, inv.NewItemFactory())
}

func (p *scriptedPlugin) Close() error { return nil }

// scriptedIterator streams the scripted records, optionally failing at the
// end so the stream-error path is reachable.
type scriptedIterator struct {
	records []record.Record
	pos     int
	err     error
	plugin  *scriptedPlugin
}

func (it *scriptedIterator) Next(context.Context) bool {
	if it.err != nil {
		return false
	}
	if it.pos+1 >= len(it.records) {
		return false
	}
	it.pos++
	return true
}

func (it *scriptedIterator) Record() record.Record {
	if it.pos < 0 || it.pos >= len(it.records) {
		return record.Record{}
	}
	return it.records[it.pos]
}

func (it *scriptedIterator) Error() error { return it.err }

func (it *scriptedIterator) Close() error {
	it.plugin.closed = true
	return nil
}

// newRepo builds an empty file-backed repository, the Crawl-tier arrangement
// a first sync actually writes into.
func newRepo(t *testing.T) inv.Repository {
	t.Helper()

	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := inv.WriteHosts(path, nil); err != nil {
		t.Fatalf("creating inventory: %v", err)
	}
	return inv.NewFileRepository(path, inv.NewItemFactory())
}

// hostRecord builds one discovered record.
func hostRecord(name string, props map[string]inventory.PropertyValue) record.Record {
	return record.Record{
		ID:         inventory.DeviceID("id-" + name),
		Name:       name,
		Properties: props,
	}
}

// reconcile runs the shared driver over p.
func reconcile(t *testing.T, p *scriptedPlugin, repo inv.Repository) syncplugin.Reconciliation {
	t.Helper()

	report, err := syncplugin.Reconcile(context.Background(), p, syncplugin.Config{Name: "scripted"}, repo, inv.NewItemFactory())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return report
}

// TestReconcile_Outcomes walks a device through its whole lifecycle across
// repeated syncs: first seen, unchanged, then changed upstream.
func TestReconcile_Outcomes(t *testing.T) {
	repo := newRepo(t)
	p := &scriptedPlugin{records: []record.Record{
		hostRecord("web1", map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
	}}

	if got := reconcile(t, p, repo).Count(syncplugin.OutcomeAdded); got != 1 {
		t.Fatalf("first sync added %d, want 1", got)
	}
	if got := reconcile(t, p, repo).Count(syncplugin.OutcomeUnchanged); got != 1 {
		t.Fatalf("second sync reported %d unchanged, want 1", got)
	}

	p.records[0].Properties["host"] = "10.0.0.2"
	if got := reconcile(t, p, repo).Count(syncplugin.OutcomeUpdated); got != 1 {
		t.Fatalf("third sync updated %d, want 1", got)
	}

	item, err := repo.GetByName(context.Background(), "web1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if host, _ := item.Properties().String("host"); host != "10.0.0.2" {
		t.Errorf("host = %q, want the updated value", host)
	}
}

// TestReconcile_DoesNotDeleteLocalProperties proves a property absent
// upstream survives. Section 11 lets a second source enrich a device it
// does not own, and deleting on every sync would erase that enrichment.
func TestReconcile_DoesNotDeleteLocalProperties(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()
	p := &scriptedPlugin{records: []record.Record{
		hostRecord("web1", map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
	}}
	reconcile(t, p, repo)

	item, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if err := item.AddInfo("owner", "platform-team", false); err != nil {
		t.Fatalf("AddInfo: %v", err)
	}
	if err := repo.Save(ctx, item); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reconcile(t, p, repo)

	reloaded, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatalf("GetByName after re-sync: %v", err)
	}
	if owner, _ := reloaded.Properties().String("owner"); owner != "platform-team" {
		t.Errorf("locally added property was lost on re-sync, owner = %q", owner)
	}
}

// TestReconcile_QuarantineIsReportedNotPersisted proves an unclassifiable
// device is reported with its reason and does not reach storage. It cannot
// be persisted because building an item requires a device type and no
// generic unclassified type is registered; reporting it is what keeps it
// visible per Section 6g.
func TestReconcile_QuarantineIsReportedNotPersisted(t *testing.T) {
	repo := newRepo(t)
	p := &scriptedPlugin{
		records: []record.Record{hostRecord("mystery1", nil)},
		classify: func(record.Record) (syncplugin.Classification, error) {
			return syncplugin.Quarantine("no software type reported"), nil
		},
	}

	report := reconcile(t, p, repo)
	if got := report.Count(syncplugin.OutcomeQuarantined); got != 1 {
		t.Fatalf("quarantined %d, want 1", got)
	}
	if report.Results[0].Reason == "" {
		t.Error("a quarantined device must carry its reason")
	}
	if _, err := repo.GetByName(context.Background(), "mystery1"); !errors.Is(err, inv.ErrItemNotFound) {
		t.Errorf("a quarantined device reached storage: %v", err)
	}
}

// TestReconcile_ForeignOwnerIsAConflict proves Section 11's One Authority
// Per Item: the second source reports the collision rather than
// overwriting, and the original data survives untouched.
func TestReconcile_ForeignOwnerIsAConflict(t *testing.T) {
	repo := newRepo(t)
	ctx := context.Background()

	p := &scriptedPlugin{records: []record.Record{
		hostRecord("web1", map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
	}}
	if _, err := syncplugin.Reconcile(ctx, p, syncplugin.Config{Name: "netbox"}, repo, inv.NewItemFactory()); err != nil {
		t.Fatalf("seeding under a foreign source: %v", err)
	}

	p.records[0].Properties["host"] = "10.0.0.99"
	report := reconcile(t, p, repo)

	if got := report.Count(syncplugin.OutcomeConflict); got != 1 {
		t.Fatalf("conflicts = %d, want 1", got)
	}
	if report.Results[0].Reason == "" {
		t.Error("a conflict must name the owning plugin")
	}

	item, err := repo.GetByName(ctx, "web1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if host, _ := item.Properties().String("host"); host != "10.0.0.1" {
		t.Errorf("the conflicting sync overwrote the owner's data: host = %q", host)
	}
}

// TestReconcile_ReadOnlyReportsWouldChange proves the read-only guard turns
// into a dry run rather than an abort. This is the simulate-first proof: a
// user asking what would change gets an answer, and nothing is written.
func TestReconcile_ReadOnlyReportsWouldChange(t *testing.T) {
	inner := newRepo(t)
	guarded := inv.NewReadOnlyRepository(inner)
	ctx := context.Background()

	p := &scriptedPlugin{records: []record.Record{
		hostRecord("web1", map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
		hostRecord("web2", map[string]inventory.PropertyValue{"host": "10.0.0.2"}),
	}}

	report := reconcile(t, p, guarded)
	if got := report.Count(syncplugin.OutcomeWouldAdd); got != 2 {
		t.Fatalf("would-add = %d, want 2 (report: %+v)", got, report.Results)
	}
	if got := report.Count(syncplugin.OutcomeAdded); got != 0 {
		t.Errorf("added = %d, want 0 under a read-only inventory", got)
	}
	for _, name := range []string{"web1", "web2"} {
		if _, err := inner.GetByName(ctx, name); !errors.Is(err, inv.ErrItemNotFound) {
			t.Errorf("%s reached storage despite read-only: %v", name, err)
		}
	}
}

// TestReconcile_ReadOnlyReportsWouldUpdate proves the same for an existing
// device whose upstream data changed.
func TestReconcile_ReadOnlyReportsWouldUpdate(t *testing.T) {
	repo := newRepo(t)
	p := &scriptedPlugin{records: []record.Record{
		hostRecord("web1", map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
	}}
	reconcile(t, p, repo)

	p.records[0].Properties["host"] = "10.0.0.2"
	report := reconcile(t, p, inv.NewReadOnlyRepository(repo))

	if got := report.Count(syncplugin.OutcomeWouldUpdate); got != 1 {
		t.Fatalf("would-update = %d, want 1 (report: %+v)", got, report.Results)
	}

	item, err := repo.GetByName(context.Background(), "web1")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if host, _ := item.Properties().String("host"); host != "10.0.0.1" {
		t.Errorf("read-only sync wrote anyway: host = %q", host)
	}
}

// TestReconcile_ClosesTheIterator proves the discovery stream is released
// even on the happy path, so a plugin holding a cursor does not leak one
// per sync.
func TestReconcile_ClosesTheIterator(t *testing.T) {
	p := &scriptedPlugin{records: []record.Record{hostRecord("web1", nil)}}
	reconcile(t, p, newRepo(t))

	if !p.closed {
		t.Error("Reconcile did not close the discovery iterator")
	}
}

// TestReconcile_PropagatesFailures covers the three ways a sync can fail
// outright, as opposed to producing a per-device outcome.
func TestReconcile_PropagatesFailures(t *testing.T) {
	discoverErr := errors.New("upstream refused the listing")
	streamErr := errors.New("connection dropped mid-page")
	classifyErr := errors.New("classifier exploded")

	tests := []struct {
		name   string
		plugin *scriptedPlugin
		want   error
	}{
		{
			name:   "discover fails",
			plugin: &scriptedPlugin{discoverErr: discoverErr},
			want:   discoverErr,
		},
		{
			name:   "stream fails partway",
			plugin: &scriptedPlugin{records: []record.Record{hostRecord("web1", nil)}, iterErr: streamErr},
			want:   streamErr,
		},
		{
			name: "classify errors instead of quarantining",
			plugin: &scriptedPlugin{
				records: []record.Record{hostRecord("web1", nil)},
				classify: func(record.Record) (syncplugin.Classification, error) {
					return syncplugin.Classification{}, classifyErr
				},
			},
			want: classifyErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := syncplugin.Reconcile(context.Background(), tt.plugin,
				syncplugin.Config{Name: "scripted"}, newRepo(t), inv.NewItemFactory())
			if !errors.Is(err, tt.want) {
				t.Fatalf("Reconcile error = %v, want it to wrap %v", err, tt.want)
			}
		})
	}
}

// TestReconcile_UnbuildableRecordFails proves a classification naming a
// device type nothing registers is a hard failure rather than a silent
// skip. Skipping would make a broken rule tree look like an empty fleet.
func TestReconcile_UnbuildableRecordFails(t *testing.T) {
	p := &scriptedPlugin{
		records: []record.Record{hostRecord("web1", nil)},
		classify: func(record.Record) (syncplugin.Classification, error) {
			return syncplugin.Classification{Type: "no_such_device_type", State: inventory.StateActive}, nil
		},
	}

	_, err := syncplugin.Reconcile(context.Background(), p,
		syncplugin.Config{Name: "scripted"}, newRepo(t), inv.NewItemFactory())
	if err == nil {
		t.Fatal("expected an unregistered device type to fail the sync")
	}
}
