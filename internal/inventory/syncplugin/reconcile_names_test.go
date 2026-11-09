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

// TestReconcile_RefusesANameThatWouldNotShowAsItselfAndGoesOn syncs a
// source holding a record whose hostname would print a line of its own
// ("sw2\npleiades: approved") and one holding a direction override, between
// two usable ones. The sync succeeds, the usable ones are added, and each
// of the two is reported refused under its own name with a reason naming
// the character, unclassified and stored nowhere. The override is built from
// its code point, so this file holds none (FAILURE_PATTERNS 256, 834).
func TestReconcile_RefusesANameThatWouldNotShowAsItselfAndGoesOn(t *testing.T) {
	repo := newRepo(t)
	newline := "sw2\npleiades: approved"
	override := "abc" + string(rune(0x202E)) + "def"
	classified := 0
	p := &scriptedPlugin{
		records: []record.Record{
			hostRecord("sw1", map[string]inventory.PropertyValue{"host": "10.0.0.1"}),
			hostRecord(newline, map[string]inventory.PropertyValue{"host": "10.0.0.2"}),
			hostRecord(override, map[string]inventory.PropertyValue{"host": "10.0.0.3"}),
			hostRecord("sw3", map[string]inventory.PropertyValue{"host": "10.0.0.4"}),
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
	if classified != 2 {
		t.Errorf("Classify ran %d times, want 2: a refused name is not classified", classified)
	}
	reasons := map[string]string{}
	for _, res := range report.Results {
		if res.Outcome == syncplugin.OutcomeRefused {
			reasons[res.Name] = res.Reason
		}
	}
	for name, says := range map[string]string{newline: "U+000A at byte 3", override: "U+202E at byte 3"} {
		reason, ok := reasons[name]
		if !ok {
			t.Errorf("%q was not reported refused: %+v", name, report.Results)
			continue
		}
		if !strings.Contains(reason, "the name cannot be used") || !strings.Contains(reason, says) {
			t.Errorf("%q refused with reason %q, want one naming %s", name, reason, says)
		}
		if _, err := repo.GetByName(context.Background(), name); !errors.Is(err, inv.ErrItemNotFound) {
			t.Errorf("the refused record %q was stored (GetByName = %v)", name, err)
		}
	}
	for _, name := range []string{"sw1", "sw3"} {
		if _, err := repo.GetByName(context.Background(), name); err != nil {
			t.Errorf("the usable record %q was not stored: %v", name, err)
		}
	}
}
