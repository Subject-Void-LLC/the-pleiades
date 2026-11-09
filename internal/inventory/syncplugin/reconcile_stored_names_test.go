// Package syncplugin_test: a sync updates a device stored under a name the
// name rule now refuses, since only a create is held to the rule (LESSONS
// 415), while it still refuses a new upstream device under such a name.
package syncplugin_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	_ "github.com/mattn/go-sqlite3"
)

// seededRepo is a store holding one linux_server at host 10.0.0.1 under
// name, written past Create the way a release before the rule could have:
// the ent store through the generated client, the file store through
// hosts.yaml written by hand. Neither records a source, so a sync may adopt
// the device, as it adopts any hand-written entry.
type seededRepo struct {
	name string
	open func(t *testing.T, name string) inv.Repository
}

// seededRepos are both stores.
func seededRepos() []seededRepo {
	return []seededRepo{
		{name: "ent", open: func(t *testing.T, name string) inv.Repository {
			t.Helper()
			client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", t.Name()))
			t.Cleanup(func() { _ = client.Close() })
			if _, err := client.Device.Create().SetName(name).SetType("linux_server").
				SetProperties(map[string]interface{}{"host": "10.0.0.1"}).Save(context.Background()); err != nil {
				t.Fatalf("seeding %q: %v", name, err)
			}
			return inv.NewEntRepository(client, inv.NewItemFactory())
		}},
		{name: "file", open: func(t *testing.T, name string) inv.Repository {
			t.Helper()
			path := filepath.Join(t.TempDir(), "inventory.yaml")
			// A YAML double-quoted scalar reads Go's escapes as the
			// characters they stand for.
			doc := "hosts:\n  - id: legacy-1\n    name: " + strconv.Quote(name) + "\n    type: linux_server\n    properties:\n      host: 10.0.0.1\n"
			if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
				t.Fatal(err)
			}
			return inv.NewFileRepository(path, inv.NewItemFactory())
		}},
	}
}

// TestReconcile_UpdatesADeviceStoredUnderANameTheRuleNowRefuses seeds a
// device whose name holds a newline and syncs a source that sends that
// device with a changed address, beside a new device whose name holds one
// too. The stored device is updated and its new address saved; the new one
// is refused, unclassified, and stored nowhere.
func TestReconcile_UpdatesADeviceStoredUnderANameTheRuleNowRefuses(t *testing.T) {
	stored := "sw2\npleiades: approved"
	fresh := "sw4\npleiades: approved"
	for _, backend := range seededRepos() {
		t.Run(backend.name, func(t *testing.T) {
			repo := backend.open(t, stored)
			classified := 0
			p := &scriptedPlugin{
				records: []record.Record{
					hostRecord(stored, map[string]inventory.PropertyValue{"host": "10.0.0.9"}),
					hostRecord(fresh, map[string]inventory.PropertyValue{"host": "10.0.0.4"}),
				},
				classify: func(record.Record) (syncplugin.Classification, error) {
					classified++
					return syncplugin.Classification{Type: "linux_server", State: inventory.StateActive}, nil
				},
			}

			report := reconcile(t, p, repo)
			outcomes := map[string]syncplugin.Outcome{}
			for _, res := range report.Results {
				outcomes[res.Name] = res.Outcome
			}
			if outcomes[stored] != syncplugin.OutcomeUpdated {
				t.Errorf("the stored device %q came out %q, want %q: %+v", stored, outcomes[stored], syncplugin.OutcomeUpdated, report.Results)
			}
			if outcomes[fresh] != syncplugin.OutcomeRefused {
				t.Errorf("the new device %q came out %q, want %q", fresh, outcomes[fresh], syncplugin.OutcomeRefused)
			}
			if classified != 1 {
				t.Errorf("Classify ran %d times, want 1: the refused new name is not classified", classified)
			}

			item, err := repo.GetByName(context.Background(), stored)
			if err != nil {
				t.Fatalf("GetByName(%q) after the sync = %v", stored, err)
			}
			if host, _ := item.Properties().String("host"); host != "10.0.0.9" {
				t.Errorf("the stored device's host is %q after the sync, want the upstream 10.0.0.9", host)
			}
			if _, err := repo.GetByName(context.Background(), fresh); !errors.Is(err, inv.ErrItemNotFound) {
				t.Errorf("the refused new device %q was stored (GetByName = %v)", fresh, err)
			}
		})
	}
}

// TestReconcile_StopsWhenItCannotTellWhetherARefusedNameIsStored: over a
// store that cannot be read, a sync cannot know whether a device is stored
// under a name the rule refuses, so it stops with the store's error, the
// name quoted, rather than refuse a device that may exist or classify one
// that may not.
func TestReconcile_StopsWhenItCannotTellWhetherARefusedNameIsStored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := os.WriteFile(path, []byte("hosts: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := inv.NewFileRepository(path, inv.NewItemFactory())
	p := &scriptedPlugin{records: []record.Record{
		hostRecord("sw2\npleiades: approved", map[string]inventory.PropertyValue{"host": "10.0.0.2"}),
	}}
	report, err := syncplugin.Reconcile(context.Background(), p, syncplugin.Config{Name: "scripted"}, repo, inv.NewItemFactory())
	if err == nil {
		t.Fatalf("Reconcile over an unreadable store = %+v, nil; want the store's error", report)
	}
	if want := `loading "sw2\npleiades: approved"`; !strings.Contains(err.Error(), want) {
		t.Errorf("Reconcile's error is %q, want it to say %s", err, want)
	}
	if got := report.Count(syncplugin.OutcomeRefused); got != 0 {
		t.Errorf("refused %d before stopping, want none", got)
	}
}
