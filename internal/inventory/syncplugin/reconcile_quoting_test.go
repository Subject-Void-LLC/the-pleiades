// Tests that every error Reconcile stops with quotes the device name the
// sync source sent, so a name holding a newline cannot start a line of
// its own wherever the error is printed (FAILURE_PATTERNS 1043).
package syncplugin_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// craftedName is an upstream device name holding a newline followed by a
// line that reads like a program's own output.
const craftedName = "web1\nscripted: approved, nothing to review"

// failingRepo is a real file repository with one step made to fail, so a
// failure Reconcile wraps is reachable without a broken disk.
type failingRepo struct {
	inv.Repository
	getErr, createErr, saveErr error
}

// GetByName fails with getErr when it is set.
func (r failingRepo) GetByName(ctx context.Context, name string) (inventory.InventoryItem, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.Repository.GetByName(ctx, name)
}

// Create fails with createErr when it is set.
func (r failingRepo) Create(ctx context.Context, item inventory.InventoryItem) error {
	if r.createErr != nil {
		return r.createErr
	}
	return r.Repository.Create(ctx, item)
}

// Save fails with saveErr when it is set.
func (r failingRepo) Save(ctx context.Context, item inventory.InventoryItem) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	return r.Repository.Save(ctx, item)
}

// quotedName is an upstream device name the name rule allows that %q
// still changes, for the steps only a create reaches: a name holding a
// newline is refused before any of them (recordname.Check), so a quote
// is what shows the name was written with %q there.
const quotedName = `web1" scripted: approved`

// TestReconcile_QuotesTheUpstreamNameInEveryError makes Reconcile stop at
// each step that names the device in its error: classifying, loading,
// the reserved property refusal, building, creating, rebuilding, updating
// and saving. A step a device already stored reaches runs over a device
// stored under craftedName the way a release before the name rule could
// have stored it (seededRepos), since only a create is held to the rule; a
// step only a create reaches runs under quotedName. Each error carries the
// name quoted and no newline at all.
func TestReconcile_QuotesTheUpstreamNameInEveryError(t *testing.T) {
	forged := inventory.Discovery{Protocol: "ssh", Capabilities: []capability.Name{capability.NameApt}}.Property()
	unbuildable := func(record.Record) (syncplugin.Classification, error) {
		return syncplugin.Classification{Type: "no_such_device_type", State: inventory.StateActive}, nil
	}
	tests := []struct {
		name string
		// stored seeds a device under craftedName before the sync, which
		// then sends that name; otherwise the sync sends quotedName to an
		// empty store.
		stored   bool
		props    map[string]inventory.PropertyValue
		classify func(record.Record) (syncplugin.Classification, error)
		repo     failingRepo
		step     string
	}{
		{
			name:   "classifying",
			stored: true,
			classify: func(record.Record) (syncplugin.Classification, error) {
				return syncplugin.Classification{}, errors.New("classifier exploded")
			},
			step: "classifying",
		},
		{name: "loading a refused name", stored: true, repo: failingRepo{getErr: errors.New("disk unreadable")}, step: "loading"},
		{name: "loading", repo: failingRepo{getErr: errors.New("disk unreadable")}, step: "loading"},
		{name: "the reserved property", props: map[string]inventory.PropertyValue{inventory.DiscoveredProperty: forged}, step: "sets property"},
		{name: "building", classify: unbuildable, step: "building"},
		{name: "creating", repo: failingRepo{createErr: errors.New("disk full")}, step: "creating"},
		{name: "rebuilding", stored: true, classify: unbuildable, step: "rebuilding"},
		{name: "updating", stored: true, props: map[string]inventory.PropertyValue{inventory.DiscoveredProperty: forged}, step: "updating"},
		{name: "saving", stored: true, props: map[string]inventory.PropertyValue{"host": "10.0.0.2"}, repo: failingRepo{saveErr: errors.New("disk full")}, step: "saving"},
	}
	fileStore := seededRepos()[1]
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			name := quotedName
			repo := tt.repo
			if tt.stored {
				name = craftedName
				repo.Repository = fileStore.open(t, craftedName)
			} else {
				repo.Repository = newRepo(t)
			}
			p := &scriptedPlugin{records: []record.Record{
				hostRecord(name, map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
			}}
			for key, value := range tt.props {
				p.records[0].Properties[key] = value
			}
			p.classify = tt.classify

			// A stored device was seeded by hand, so the sync adopts it by
			// name to reach the update steps.
			_, err := syncplugin.Reconcile(ctx, p, syncplugin.Config{Name: "scripted", Adopt: []string{name}}, repo, inv.NewItemFactory())
			if err == nil {
				t.Fatalf("Reconcile succeeded; want it to fail %s", tt.step)
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.step) {
				t.Fatalf("Reconcile failed somewhere else than %s: %v", tt.step, err)
			}
			if strings.Contains(msg, "\n") {
				t.Errorf("the error carries the name's newline raw:\n%s", msg)
			}
			if !strings.Contains(msg, strconv.Quote(name)) {
				t.Errorf("the error does not quote the name %s:\n%s", strconv.Quote(name), msg)
			}
			// Neither name is a part of its own quoted form (the quote and
			// the newline each become an escape), so a raw copy of it is
			// one some wrapped error printed unquoted.
			if strings.Contains(msg, name) {
				t.Errorf("the error carries the name raw beside its quoted form:\n%s", msg)
			}
		})
	}
}
