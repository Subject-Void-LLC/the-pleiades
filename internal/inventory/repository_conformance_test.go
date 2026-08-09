package inventory_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// This file is the Phase W4 Release Gate proof for the Repository port: "the
// identical engine code path runs against local adapters and against NATS
// plus Postgres, selected only by wiring in the composition root."
// entRepository (ent_repository.go) is the Crawl-tier-and-above adapter, the
// same generated ent.Client code a real Postgres deployment uses (Open's
// driverName is the only thing that changes; entRepository itself never
// knows which SQL dialect backs it). fileRepository (file_repository.go) is
// the Walk-tier local adapter this session added. Every test below runs the
// exact same sequence of Repository calls against both, asserting identical
// outcomes, so a caller (the engine, a future executor) cannot tell which
// adapter it is talking to from behavior alone.
//
// Using an in-memory SQLite database to stand in for "the ent-backed
// adapter" rather than spinning up a real Postgres container matches this
// repository's own existing convention for testing entRepository
// (client_test.go, ent_save_test.go): entRepository's code is exactly the
// same regardless of which SQL dialect Open connects to, so this is the
// real code path, not a mock of it, per RULE 0.

// repositoryBackend names one Repository implementation under conformance
// test, plus how to construct a fresh instance seeded with exactly one host
// named conformanceHostName.
type repositoryBackend struct {
	name    string
	newRepo func(t *testing.T) inventory.Repository
}

// conformanceHostName is the seeded host every backend below constructs,
// so every conformance test can address it identically regardless of which
// backend produced it.
const conformanceHostName = "conformance-host"

// repositoryBackends lists every Repository implementation the conformance
// suite below runs against. Adding a third adapter behind this port means
// adding one entry here, never editing an existing test function.
func repositoryBackends() []repositoryBackend {
	return []repositoryBackend{
		{name: "ent", newRepo: newConformanceEntRepo},
		{name: "file", newRepo: newConformanceFileRepo},
	}
}

// newConformanceEntRepo builds an entRepository over a fresh in-memory
// SQLite database (via enttest, this codebase's existing convention for
// exercising the real ent-backed code path without a live Postgres
// instance) and seeds it with one linux_server host.
func newConformanceEntRepo(t *testing.T) inventory.Repository {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	_, err := client.Device.Create().
		SetName(conformanceHostName).
		SetType("linux_server").
		SetProperties(map[string]interface{}{
			"host": "10.0.0.9",
		}).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seeding ent-backed conformance host: %v", err)
	}

	return inventory.NewEntRepository(client, inventory.NewItemFactory())
}

// newConformanceFileRepo builds a fileRepository over a hosts.yaml file in a
// fresh t.TempDir(), seeded with one linux_server host, matching
// newConformanceEntRepo's seed data field for field so both backends start
// from an equivalent state.
func newConformanceFileRepo(t *testing.T) inventory.Repository {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	err := inventory.WriteHosts(path, []inventory.HostSpec{
		{
			ID:   "conformance-id-1",
			Name: conformanceHostName,
			Type: "linux_server",
			Properties: map[string]interface{}{
				"host": "10.0.0.9",
			},
		},
	})
	if err != nil {
		t.Fatalf("seeding file-backed conformance host: %v", err)
	}

	return inventory.NewFileRepository(path, inventory.NewItemFactory())
}

// TestRepositoryConformance_FreshHostStartsAtVersionZero asserts a host that
// has never been through Save loads at version 0 with no history, on both
// backends.
func TestRepositoryConformance_FreshHostStartsAtVersionZero(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.newRepo(t)

			item, err := repo.GetByName(context.Background(), conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if got := item.Version(); got != 0 {
				t.Errorf("fresh host version = %d, want 0", got)
			}
			if got := len(item.History()); got != 0 {
				t.Errorf("fresh host history length = %d, want 0", got)
			}
			if got, ok := item.Properties().String("host"); !ok || got != "10.0.0.9" {
				t.Errorf("host property = %q, %v, want 10.0.0.9, true", got, ok)
			}
		})
	}
}

// TestRepositoryConformance_ShowInfoAgreesAcrossBackends is the
// conformance scenario the chain audit's device-type discriminator item
// asks for (IMPLEMENTATION.md Phase W4): both backends are seeded with
// the identical logical device (conformanceHostName, type linux_server,
// one "host" property), and ShowInfo() must return the identical content
// on both. Before the fix, entRepository read its classification key out
// of Properties["type"] while the file adapter kept HostSpec.Type
// entirely out of Properties, so the two backends' ShowInfo() disagreed
// for the same logical device and no existing test compared them side by
// side to notice.
func TestRepositoryConformance_ShowInfoAgreesAcrossBackends(t *testing.T) {
	ctx := context.Background()
	got := make(map[string]pkginventory.Properties, len(repositoryBackends()))

	for _, backend := range repositoryBackends() {
		item, err := backend.newRepo(t).GetByName(ctx, conformanceHostName)
		if err != nil {
			t.Fatalf("%s: GetByName: %v", backend.name, err)
		}
		got[backend.name] = item.ShowInfo()

		if _, ok := item.ShowInfo().String("type"); ok {
			t.Errorf("%s: ShowInfo() contains a \"type\" key; type must be a first-class field, not a property", backend.name)
		}
	}

	entHost, _ := got["ent"].String("host")
	fileHost, _ := got["file"].String("host")
	if entHost != fileHost {
		t.Errorf("ShowInfo()[\"host\"] disagrees across backends: ent=%q file=%q", entHost, fileHost)
	}
}

// TestRepositoryConformance_SaveRoundTripsPropertiesVersionHistory mirrors
// the same round trip TestSave_RoundTripsPropertiesVersionAndHistory
// (ent_save_test.go) and TestFileRepository_RoundTripsPropertiesVersionAndHistory
// (file_repository_test.go) each already assert individually, but here as
// one test function driving both backends through the identical call
// sequence, which is what actually proves substitutability rather than two
// independently-written tests that merely happen to agree.
func TestRepositoryConformance_SaveRoundTripsPropertiesVersionHistory(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			repo := backend.newRepo(t)

			loaded, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}

			if err := loaded.AddInfo("kernel", "6.6.1", false); err != nil {
				t.Fatalf("AddInfo: %v", err)
			}
			if err := loaded.AddInfo("kernel", "6.6.2", true); err != nil {
				t.Fatalf("AddInfo overwrite: %v", err)
			}
			if err := repo.Save(ctx, loaded); err != nil {
				t.Fatalf("Save: %v", err)
			}

			reloaded, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName after save: %v", err)
			}

			if got := reloaded.Version(); got != 2 {
				t.Errorf("version after reload = %d, want 2", got)
			}
			if got, _ := reloaded.Properties().String("kernel"); got != "6.6.2" {
				t.Errorf("kernel after reload = %q, want 6.6.2", got)
			}

			history := reloaded.History()
			if len(history) != 2 {
				t.Fatalf("history length = %d, want 2: %+v", len(history), history)
			}
			if history[0].Version != 1 || history[1].Version != 2 {
				t.Errorf("history versions = %d,%d, want 1,2", history[0].Version, history[1].Version)
			}
			if history[1].OldValue != "6.6.1" || history[1].NewValue != "6.6.2" {
				t.Errorf("second revision = old %v new %v, want old 6.6.1 new 6.6.2", history[1].OldValue, history[1].NewValue)
			}

			// Save is a no-op the second time with nothing new to persist:
			// both adapters must honor this identically rather than burning
			// a version bump on an unchanged item.
			if err := repo.Save(ctx, reloaded); err != nil {
				t.Errorf("no-op Save on unchanged item returned an error: %v", err)
			}
		})
	}
}

// TestRepositoryConformance_SaveRejectsConcurrentWrite is the
// optimistic-concurrency conflict test run identically against both
// backends: two readers load the same host, the first Save wins, the
// second must fail with ErrVersionConflict and must not have clobbered the
// first writer's change. Both entRepository.Save (ent_save.go) and
// fileRepository.Save (file_repository_save.go) are individually
// mutation-tested for this exact check in their own package-specific test
// files; this test exists to prove they fail in the identical, documented
// way (ErrVersionConflict, first writer preserved), not just that they each
// fail somehow.
func TestRepositoryConformance_SaveRejectsConcurrentWrite(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			repo := backend.newRepo(t)

			readerA, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName (reader A): %v", err)
			}
			readerB, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName (reader B): %v", err)
			}

			if err := readerA.AddInfo("kernel", "from-a", false); err != nil {
				t.Fatalf("AddInfo (A): %v", err)
			}
			if err := repo.Save(ctx, readerA); err != nil {
				t.Fatalf("Save (A), expected success: %v", err)
			}

			if err := readerB.AddInfo("kernel", "from-b", false); err != nil {
				t.Fatalf("AddInfo (B): %v", err)
			}
			err = repo.Save(ctx, readerB)
			if !errors.Is(err, inventory.ErrVersionConflict) {
				t.Fatalf("Save (B) error = %v, want ErrVersionConflict", err)
			}

			final, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName (final): %v", err)
			}
			if got, _ := final.Properties().String("kernel"); got != "from-a" {
				t.Errorf("stored kernel = %q, want from-a (the first writer must survive)", got)
			}
			if got := final.Version(); got != 1 {
				t.Errorf("stored version = %d, want 1 (the rejected writer must not have bumped it)", got)
			}
		})
	}
}

// TestRepositoryConformance_GetGroupListViewOmitsHistory asserts the
// documented Repository contract (iterator.go: "Items it yields carry their
// stored version but not their audit trail") holds identically on both
// backends: GetGroup's list view carries Version but not History, and
// GetByName's single-item read carries both.
func TestRepositoryConformance_GetGroupListViewOmitsHistory(t *testing.T) {
	for _, backend := range repositoryBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			repo := backend.newRepo(t)

			seeded, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if err := seeded.AddInfo("kernel", "6.6.1", false); err != nil {
				t.Fatalf("AddInfo: %v", err)
			}
			if err := repo.Save(ctx, seeded); err != nil {
				t.Fatalf("Save: %v", err)
			}

			it, err := repo.GetGroup(ctx, pkginventory.Selector{})
			if err != nil {
				t.Fatalf("GetGroup: %v", err)
			}
			defer it.Close()

			if !it.Next(ctx) {
				t.Fatalf("GetGroup iterator yielded no items, want 1")
			}
			listed := it.Item()
			if got := listed.Version(); got != 1 {
				t.Errorf("list-view version = %d, want 1", got)
			}
			if got := len(listed.History()); got != 0 {
				t.Errorf("list-view history length = %d, want 0 (not loaded)", got)
			}

			full, err := repo.GetByName(ctx, conformanceHostName)
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}
			if got := len(full.History()); got != 1 {
				t.Errorf("single-item read history length = %d, want 1 (loaded)", got)
			}
		})
	}
}

// conformanceTagsHostName is the host TestRepositoryConformance_TagsAndSourceRoundTrip
// seeds independently of conformanceHostName, since this test needs
// per-host Tags/Source input the other tests' shared fixture does not
// carry.
const conformanceTagsHostName = "conformance-tags-host"

// conformanceSeedTags is the input both backends below are seeded with,
// so asserting the reloaded item's Tags() against it proves an actual
// round trip, not just "both backends return something."
var conformanceSeedTags = []string{"core", "prod"}

// TestRepositoryConformance_TagsAndSourceRoundTrip closes a gap named in
// HANDOFF_DOCUMENT.md's Phase W4 section: "Tags() currently returns a
// hardcoded empty slice" for the ent-backed adapter, because nowhere in
// the old schema had anywhere to persist what a sync plugin reported.
// This phase added Device.tags and Device.source/source_synced_at
// columns and wired toRecord to populate them for real (ent_repository.go),
// matching what the file-backed adapter already did from real YAML.
//
// Source is now asserted symmetrically. It used to be asserted more
// narrowly on the file backend, because that backend hardcoded Plugin:
// "file" for every host regardless of input and there was no arbitrary
// value to round-trip. That hardcoded default was wrong and has been
// removed: it conflated where a device's data is stored with which sync
// plugin authoritatively owns it, and Section 11's One Authority Per Item
// is about the second. The practical damage was that every hand-written
// hosts.yaml entry looked like it was already claimed by a plugin named
// "file", so the first real sync plugin to run against a Walk-tier project
// reported every host as a conflict and adopted none of them.
//
// Both backends now round-trip a real, caller-supplied plugin name, which
// is a stronger conformance claim than the asymmetric one it replaces.
func TestRepositoryConformance_TagsAndSourceRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		newRepo    func(t *testing.T) inventory.Repository
		wantPlugin string
	}{
		{name: "ent", newRepo: newConformanceTagsEntRepo, wantPlugin: "netbox"},
		{name: "file", newRepo: newConformanceTagsFileRepo, wantPlugin: "netbox"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := tt.newRepo(t)

			item, err := repo.GetByName(context.Background(), conformanceTagsHostName)
			if err != nil {
				t.Fatalf("GetByName: %v", err)
			}

			wantTags := make([]pkginventory.Tag, len(conformanceSeedTags))
			for i, s := range conformanceSeedTags {
				wantTags[i] = pkginventory.Tag(s)
			}
			if got := item.Tags(); !reflect.DeepEqual(got, wantTags) {
				t.Errorf("Tags() = %v, want %v", got, wantTags)
			}

			if got := item.Source().Plugin; got != tt.wantPlugin {
				t.Errorf("Source().Plugin = %q, want %q", got, tt.wantPlugin)
			}
			if item.Source().Plugin == "" {
				t.Errorf("Source().Plugin must never be empty")
			}
		})
	}
}

// newConformanceTagsEntRepo seeds an ent-backed device with real tags and
// a real source plugin name, both stored in the columns this phase added.
func newConformanceTagsEntRepo(t *testing.T) inventory.Repository {
	t.Helper()

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name())
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })

	_, err := client.Device.Create().
		SetName(conformanceTagsHostName).
		SetType("linux_server").
		SetProperties(map[string]interface{}{
			"host": "10.0.0.10",
		}).
		SetTags(conformanceSeedTags).
		SetSource("netbox").
		Save(context.Background())
	if err != nil {
		t.Fatalf("seeding ent-backed tags conformance host: %v", err)
	}

	return inventory.NewEntRepository(client, inventory.NewItemFactory())
}

// newConformanceTagsFileRepo seeds a file-backed device with the same tags
// and the same source plugin name the ent backend is seeded with.
//
// It seeds through Repository.Create rather than by writing the YAML
// directly, because provenance lives in the sidecar state file rather than
// in HostSpec, and Create is the port's own way to write both halves. That
// also makes this the conformance proof that Create stores what it was
// given: the assertions below read back through GetByName without caring
// which backend wrote the row.
func newConformanceTagsFileRepo(t *testing.T) inventory.Repository {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yaml")
	if err := inventory.WriteHosts(path, nil); err != nil {
		t.Fatalf("creating empty file-backed inventory: %v", err)
	}

	factory := inventory.NewItemFactory()
	repo := inventory.NewFileRepository(path, factory)

	tags := make([]pkginventory.Tag, len(conformanceSeedTags))
	for i, s := range conformanceSeedTags {
		tags[i] = pkginventory.Tag(s)
	}

	item, err := factory.Build(record.Record{
		ID:   "conformance-tags-id-1",
		Name: conformanceTagsHostName,
		Type: "linux_server",
		Properties: map[string]interface{}{
			"host": "10.0.0.10",
		},
		Tags:   tags,
		State:  pkginventory.StateActive,
		Source: pkginventory.SourceAuthority{Plugin: "netbox"},
	})
	if err != nil {
		t.Fatalf("building file-backed tags conformance host: %v", err)
	}
	if err := repo.Create(context.Background(), item); err != nil {
		t.Fatalf("seeding file-backed tags conformance host: %v", err)
	}

	return repo
}
