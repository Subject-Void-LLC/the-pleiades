// Package syncplugin_test: a sync skips an upstream record whose name no URL
// can carry, reports it with the reason, and goes on with the rest.
package syncplugin_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestReconcile_RefusesANameNoURLCarriesAndGoesOn syncs a source holding
// records named "", "." and ".." between two usable ones. The sync succeeds,
// the usable ones are added, and each of the three is reported refused with
// the reason and stored nowhere.
func TestReconcile_RefusesANameNoURLCarriesAndGoesOn(t *testing.T) {
	repo := newRepo(t)
	classified := 0
	p := &scriptedPlugin{
		records: []record.Record{
			hostRecord("web1", map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
			hostRecord("", map[string]inventory.PropertyValue{"host": "10.0.0.2"}),
			hostRecord(".", map[string]inventory.PropertyValue{"host": "10.0.0.3"}),
			hostRecord("..", map[string]inventory.PropertyValue{"host": "10.0.0.4"}),
			hostRecord("web2", map[string]inventory.PropertyValue{"host": "10.0.0.5"}),
		},
		classify: func(record.Record) (syncplugin.Classification, error) {
			classified++
			return syncplugin.Classification{Type: "linux_server", State: inventory.StateActive}, nil
		},
	}

	report := reconcile(t, p, repo)
	if got := report.Count(syncplugin.OutcomeAdded); got != 2 {
		t.Errorf("added %d, want the 2 usable names", got)
	}
	if got := report.Count(syncplugin.OutcomeRefused); got != 3 {
		t.Fatalf("refused %d, want 3: %+v", got, report.Results)
	}
	if classified != 2 {
		t.Errorf("Classify ran %d times, want 2: a refused name is not classified", classified)
	}
	for _, res := range report.Results {
		if res.Outcome != syncplugin.OutcomeRefused {
			continue
		}
		if res.Reason == "" || !strings.Contains(res.Reason, "URL") && !strings.Contains(res.Reason, "required") {
			t.Errorf("refused %q with reason %q, want one saying why", res.Name, res.Reason)
		}
		if _, err := repo.GetByName(context.Background(), res.Name); !errors.Is(err, inv.ErrItemNotFound) {
			t.Errorf("the refused record %q was stored (GetByName = %v)", res.Name, err)
		}
	}
	for _, name := range []string{"web1", "web2"} {
		if _, err := repo.GetByName(context.Background(), name); err != nil {
			t.Errorf("the usable record %q was not stored: %v", name, err)
		}
	}
}
