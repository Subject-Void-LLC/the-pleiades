// Tests of the built-in fact difference note (FP 1020): a Controller stores
// the facts a built-in holds from a batch whose method its own catalog lacks
// (a Runner of a newer release, a built-in renamed, a batch naming no
// method), and says so at info with a count, the families and a bounded list
// of names, never a value. What keeps a program from writing such a fact is
// the Runner's loader, and on the Controller the node resolver, which judges
// a batch under the method its node ran rather than the one it names
// (nodes_test.go). These tests build the consumer without one, as a test of
// the store alone does, except the last.
package entstore_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// noteSubscriber is the Controller's facts consumer over a real store, held
// to the embedded catalog's declarations and built-in methods as
// cmd/controller builds it, logging every level into logged.
func noteSubscriber(t *testing.T, store facts.Store, logged *bytes.Buffer) *entstore.Subscriber {
	t.Helper()
	index, err := entstore.CatalogIndex(wellknown.ModuleCatalog)
	if err != nil {
		t.Fatal(err)
	}
	builtins, err := entstore.BuiltinMethods(wellknown.ModuleCatalog)
	if err != nil || !builtins["facts.gather"] {
		t.Fatalf("BuiltinMethods = %d methods, %v; want facts.gather among them", len(builtins), err)
	}
	if _, err := entstore.BuiltinMethods([]byte("not json")); err == nil {
		t.Error("a catalog that does not decode was read")
	}
	admitted := func(context.Context, string, string) (journal.Admission, error) { return journal.AdmitDispatched, nil }
	logger := slog.New(slog.NewTextHandler(logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return entstore.NewSubscriber(store, logger, admitted, entstore.WithDeclarations(index), entstore.WithBuiltinProvenance(builtins))
}

// storedKeys is device's current keys, sorted.
func storedKeys(t *testing.T, store facts.Store, device string) []string {
	t.Helper()
	current, err := store.Current(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range current {
		got = append(got, e.Key)
	}
	sort.Strings(got)
	return got
}

// noteLines returns the log lines of the built-in difference note.
func noteLines(log string) []string {
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "reserves for built-ins") {
			out = append(out, line)
		}
	}
	return out
}

// TestSubscriber_KeepsBuiltInFactsFromAMethodOutsideTheCatalog: a batch
// whose method is not one this controller's catalog names (a Runner's newer
// built-in, a built-in this release renamed, or no method at all) stores
// every fact, a declared key and a reserved family's, whatever its case,
// and one info line notes how many and under which families, naming the
// keys and none of the values. A built-in method's batch stores the same
// and notes nothing.
func TestSubscriber_KeepsBuiltInFactsFromAMethodOutsideTheCatalog(t *testing.T) {
	observed := map[string]any{"host.name": "zq9value-web1", "Package.openssh": "zq9value-9.6", "acme.widgets": 3, "edge.site": "zq9value-dc1"}
	all := []string{"Package.openssh", "acme.widgets", "edge.site", "host.name"}
	for _, tc := range []struct {
		name   string
		method string
		noted  bool
	}{
		{name: "a newer Runner's built-in", method: "newer.gather", noted: true},
		{name: "a built-in this release renamed", method: "facts.gather_old", noted: true},
		{name: "no method", method: "", noted: true},
		{name: "a program's method", method: "acme.facts.read", noted: true},
		{name: "a built-in method", method: "facts.gather"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client)
			var logged bytes.Buffer
			sub := noteSubscriber(t, store, &logged)
			batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
				Entries: prepared(t, observed), By: facts.Provenance{Method: tc.method}}
			if err := handle(t, sub, batch); err != nil {
				t.Fatal(err)
			}
			if got := storedKeys(t, store, device); !reflect.DeepEqual(got, all) {
				t.Errorf("stored %q, want every fact %q", got, all)
			}
			notes := noteLines(logged.String())
			switch {
			case !tc.noted && len(notes) != 0:
				t.Errorf("a built-in method's batch was noted: %q", notes)
			case tc.noted && (len(notes) != 1 || !strings.Contains(notes[0], "level=INFO")):
				t.Errorf("logged %q, want one info note", notes)
			case tc.noted:
				for _, want := range []string{"facts=3", "package.", "edge.", "host.", "Package.openssh", "edge.site", "host.name"} {
					if !strings.Contains(notes[0], want) {
						t.Errorf("the note does not say %q: %s", want, notes[0])
					}
				}
				if strings.Contains(notes[0], "acme.widgets") {
					t.Errorf("the note names a fact no built-in holds: %s", notes[0])
				}
			}
			if strings.Contains(logged.String(), "zq9value") {
				t.Errorf("a log line holds a fact's value:\n%s", logged.String())
			}
			if strings.Contains(logged.String(), "level=WARN") {
				t.Errorf("a warning was logged:\n%s", logged.String())
			}
		})
	}
}

// TestSubscriber_BoundsTheBuiltInFactNote: a batch of hundreds of built-in
// facts from a method outside the catalog stores every one, and its note
// gives their number and names a bounded few.
func TestSubscriber_BoundsTheBuiltInFactNote(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	var logged bytes.Buffer
	sub := noteSubscriber(t, store, &logged)
	observed := map[string]any{}
	for i := range 300 {
		observed[fmt.Sprintf("edge.p%03d", i)] = "1.0"
	}
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
		Entries: prepared(t, observed), By: facts.Provenance{Method: "newer.gather"}}
	if err := handle(t, sub, batch); err != nil {
		t.Fatal(err)
	}
	if got := storedKeys(t, store, device); len(got) != 300 {
		t.Fatalf("stored %d facts, want all 300", len(got))
	}
	notes := noteLines(logged.String())
	if len(notes) != 1 || !strings.Contains(notes[0], "facts=300") {
		t.Fatalf("logged %q, want one note counting 300 facts", notes)
	}
	if named := strings.Count(notes[0], "edge.p"); named == 0 || named > entstore.MaxNotedFacts {
		t.Errorf("the note names %d facts, want between 1 and %d: %s", named, entstore.MaxNotedFacts, notes[0])
	}
}

// TestCatalogMethodClaimNoLongerWritesBuiltinKeys: a Runner that names a
// catalog method for the facts of a node that ran an external Collection's
// method used to write built-in keys, since the batch's method was its own
// claim. With the node resolver the Controller wires in (nodes.go), the
// batch is judged under the node's method: no built-in key is stored, the
// claim is said at warning, and nothing is noted as a release difference.
func TestCatalogMethodClaimNoLongerWritesBuiltinKeys(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	var logged bytes.Buffer
	nodes := &fixedNode{node: journal.NodeMethod{Resolved: true, Method: "acme.facts.read"}}
	sub := nodeSubscriber(t, store, &logged, nodes.resolve)
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
		Entries: prepared(t, map[string]any{"host.name": "forged-name", "Package.openssh": "0.0-forged"}),
		By:      facts.Provenance{Node: "tasks[0]", Method: "facts.gather"}}
	if err := handle(t, sub, batch); err != nil {
		t.Fatal(err)
	}
	if got := storedKeys(t, store, device); len(got) != 0 {
		t.Errorf("stored %q: a forged claim still writes built-in keys", got)
	}
	if notes := noteLines(logged.String()); len(notes) != 0 {
		t.Errorf("noted %q as a release difference", notes)
	}
	if !strings.Contains(logged.String(), "claimed=facts.gather") || !strings.Contains(logged.String(), "dispatched=acme.facts.read") {
		t.Errorf("the forged claim was not said at warning:\n%s", logged.String())
	}
}
