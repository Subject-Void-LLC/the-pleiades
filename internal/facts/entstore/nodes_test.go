// Tests of the facts consumer judging each batch under the method its node
// ran, as the Controller dispatched it (nodes.go), through a real store over
// a real SQLite database.
package entstore_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
)

// fixedNode is a journal.NodeResolver answering node for every node id, and
// recording the ids it was asked for.
type fixedNode struct {
	node  journal.NodeMethod
	err   error
	asked []string
}

// resolve implements journal.NodeResolver.
func (f *fixedNode) resolve(_ context.Context, _, _, nodeID string) (journal.NodeMethod, error) {
	f.asked = append(f.asked, nodeID)
	return f.node, f.err
}

// nodeSubscriber is the consumer as cmd/controller builds it, with resolve
// as its node resolver.
func nodeSubscriber(t *testing.T, store facts.Store, logged *bytes.Buffer, resolve journal.NodeResolver) *entstore.Subscriber {
	t.Helper()
	index, err := entstore.CatalogIndex(wellknown.ModuleCatalog)
	if err != nil {
		t.Fatal(err)
	}
	builtins, err := entstore.BuiltinMethods(wellknown.ModuleCatalog)
	if err != nil {
		t.Fatal(err)
	}
	admitted := func(context.Context, string, string) (journal.Admission, error) { return journal.AdmitDispatched, nil }
	logger := slog.New(slog.NewTextHandler(logged, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return entstore.NewSubscriber(store, logger, admitted, entstore.WithDeclarations(index), entstore.WithBuiltinProvenance(builtins),
		entstore.WithNodeResolver(resolve))
}

// storedMethods is the method each of device's current facts was stored
// under, by key.
func storedMethods(t *testing.T, store facts.Store, device string) map[string]string {
	t.Helper()
	current, err := store.Current(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range current {
		out[e.Key] = e.By.Method
	}
	return out
}

// TestSubscriber_JudgesABatchUnderItsNodesMethod: each batch is stored under
// the method the Controller dispatched for its node. A hostile batch naming
// a catalog method for a node that ran an external one keeps only the facts
// no built-in holds, under the node's method; an external method's own
// batch is held the same; an honest newer Runner's first-party built-in,
// outside this catalog, keeps every fact; a generic node's batch naming the
// concrete method it dispatched to is stored as the generic; and a node the
// Controller cannot resolve keeps only the facts no built-in holds, with no
// method. Every change is a warning naming both methods and no value.
func TestSubscriber_JudgesABatchUnderItsNodesMethod(t *testing.T) {
	observed := map[string]any{"host.name": "zq9value-web1", "Package.openssh": "zq9value-9.6", "acme.widgets": 3}
	external := journal.NodeMethod{Resolved: true, Method: "acme.inventory.read"}
	for _, tc := range []struct {
		name    string
		node    journal.NodeMethod
		claim   string
		stored  map[string]string
		warns   []string
		noWarns bool
	}{
		{name: "a catalog method claimed for an external node", node: external, claim: "facts.gather",
			stored: map[string]string{"acme.widgets": "acme.inventory.read"},
			warns:  []string{"claimed=facts.gather", "dispatched=acme.inventory.read", "dropped=2", "kept=1"}},
		{name: "an external node's own method", node: external, claim: "acme.inventory.read",
			stored: map[string]string{"acme.widgets": "acme.inventory.read"},
			warns:  []string{"method=acme.inventory.read", "dropped=2"}},
		{name: "a newer Runner's built-in", node: journal.NodeMethod{Resolved: true, Method: "facts.gather_next"}, claim: "facts.gather_next",
			stored:  map[string]string{"acme.widgets": "facts.gather_next", "host.name": "facts.gather_next", "Package.openssh": "facts.gather_next"},
			noWarns: true},
		{name: "a generic node's concrete method", node: journal.NodeMethod{Resolved: true, Method: "pkg.install"}, claim: "pkg.apt.install",
			stored: map[string]string{"acme.widgets": "pkg.install", "host.name": "pkg.install", "Package.openssh": "pkg.install"},
			warns:  []string{"claimed=pkg.apt.install", "dispatched=pkg.install"}},
		{name: "a kept undo claim", node: journal.NodeMethod{Resolved: true, AnyFirstParty: true}, claim: "file.copy",
			stored: map[string]string{"acme.widgets": "file.copy", "host.name": "file.copy", "Package.openssh": "file.copy"},
			warns:  []string{journal.KeptNote, "level=INFO"}},
		{name: "the Runner's unresolved sentinel", node: journal.NodeMethod{Resolved: true, Method: "facts.gather"}, claim: engine.FQCNUnregistered,
			stored: map[string]string{"acme.widgets": ""},
			warns:  []string{"dropped=2", "kept=1", "its Runner could not dispatch"}},
		{name: "an unresolvable node", node: journal.NodeMethod{Reason: "the job pinned no runbook graph (a playbook job, or one fanned out before graphs were pinned)"}, claim: "facts.gather",
			stored: map[string]string{"acme.widgets": ""},
			warns:  []string{"claimed=facts.gather", "dropped=2", "kept=1", "pinned no runbook graph"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client)
			var logged bytes.Buffer
			nodes := &fixedNode{node: tc.node}
			sub := nodeSubscriber(t, store, &logged, nodes.resolve)
			batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
				Entries: prepared(t, observed), By: facts.Provenance{Node: "tasks[3]", Method: tc.claim}}
			if err := handle(t, sub, batch); err != nil {
				t.Fatal(err)
			}
			if got := storedMethods(t, store, device); !reflect.DeepEqual(got, tc.stored) {
				t.Errorf("stored %v, want %v", got, tc.stored)
			}
			if !reflect.DeepEqual(nodes.asked, []string{"tasks[3]"}) {
				t.Errorf("the resolver was asked %q, want the batch's own node", nodes.asked)
			}
			log := logged.String()
			for _, want := range tc.warns {
				if !strings.Contains(log, want) {
					t.Errorf("the log does not say %s:\n%s", want, log)
				}
			}
			if tc.noWarns && strings.Contains(log, "level=WARN") {
				t.Errorf("an honest batch was warned about:\n%s", log)
			}
			if strings.Contains(log, "zq9value") {
				t.Errorf("a log line holds a fact's value:\n%s", log)
			}
		})
	}
}

// TestSubscriber_RedeliversWhenTheNodeCannotBeResolvedNow: a resolver that
// cannot read the record fails the batch, so the bus delivers it again, and
// nothing is stored.
func TestSubscriber_RedeliversWhenTheNodeCannotBeResolvedNow(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	var logged bytes.Buffer
	nodes := &fixedNode{err: errors.New("database is locked")}
	sub := nodeSubscriber(t, store, &logged, nodes.resolve)
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
		Entries: prepared(t, map[string]any{"acme.widgets": 3}), By: facts.Provenance{Node: "tasks[0]", Method: "acme.inventory.read"}}
	if err := handle(t, sub, batch); err == nil {
		t.Fatal("a resolver error was acknowledged")
	}
	if got := storedMethods(t, store, device); len(got) != 0 {
		t.Errorf("stored %v after a resolver error", got)
	}
}

// failingOnce is a store whose first Record fails, as a database briefly
// unavailable does.
type failingOnce struct {
	facts.Store
	failed bool
}

// Record fails the first time and stores every later time.
func (f *failingOnce) Record(ctx context.Context, device string, obs facts.Observation) error {
	if !f.failed {
		f.failed = true
		return errors.New("database is locked")
	}
	return f.Store.Record(ctx, device, obs)
}

// TestSubscriber_SaysWhatItChangedOnlyOnceStored: a batch whose write fails
// says nothing of what was changed of it (its claim held to its node, facts
// a built-in holds dropped), and the redelivery that stores it says each
// once, as the built-in difference note was already said.
func TestSubscriber_SaysWhatItChangedOnlyOnceStored(t *testing.T) {
	client, device := open(t)
	store := &failingOnce{Store: entstore.New(client)}
	var logged bytes.Buffer
	nodes := &fixedNode{node: journal.NodeMethod{Resolved: true, Method: "acme.inventory.read"}}
	sub := nodeSubscriber(t, store, &logged, nodes.resolve)
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
		Entries: prepared(t, map[string]any{"host.name": "zq9value", "acme.widgets": 3}), By: facts.Provenance{Node: "tasks[0]", Method: "facts.gather"}}
	notes := []string{"not the one their batch names", "from a node that ran an external Collection's method"}
	if err := handle(t, sub, batch); err == nil {
		t.Fatal("a failed write was acknowledged")
	}
	for _, note := range notes {
		if strings.Contains(logged.String(), note) {
			t.Errorf("a batch that was not stored said %q:\n%s", note, logged.String())
		}
	}
	if err := handle(t, sub, batch); err != nil {
		t.Fatal(err)
	}
	for _, note := range notes {
		if n := strings.Count(logged.String(), note); n != 1 {
			t.Errorf("the stored batch said %q %d times, want once:\n%s", note, n, logged.String())
		}
	}
}
