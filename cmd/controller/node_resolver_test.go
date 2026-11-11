// Tests of the node resolver the facts and journal consumers share, over the
// Controller's real job store and a real runbook directory, and of both
// consumers built as main builds them with it.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// resolverRunbook is the runbook the jobs below dispatch: an external
// Collection's method, a built-in fact gatherer, a generic dispatcher, a
// change with an authored rollback: list, one whose undo its method records,
// and a first-party method this Controller's catalog lacks.
const resolverRunbook = `id: web
hosts: h1
tasks:
  - name: read the inventory
    acme.inventory.read:
      scope: all
  - name: gather
    facts.gather: {}
  - name: install
    pkg.install:
      name: nginx
  - name: chmod motd
    file.permissions:
      path: /etc/motd
      mode: "0644"
    rollback:
      - name: put it back
        file.permissions:
          path: /etc/motd
          mode: "0600"
  - name: chmod issue
    file.permissions:
      path: /etc/issue
      mode: "0644"
  - name: gather the way a newer release does
    facts.gather_next: {}
`

// resolverFixture is a real database, job store and runbook directory.
type resolverFixture struct {
	client   *ent.Client
	jobs     dispatch.JobStore
	dir      string
	runbooks runbook.Source
	device   string
}

// newResolverFixture opens the fixture with resolverRunbook written as web.
func newResolverFixture(t *testing.T) *resolverFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=10000&_fk=1", filepath.Join(t.TempDir(), "controller.db"))
	client := enttest.Open(t, "sqlite3", dsn)
	t.Cleanup(func() { _ = client.Close() })
	dev := client.Device.Create().SetName("web1").SetType("linux_server").SetProperties(map[string]any{"host": "10.0.0.1"}).SaveX(context.Background())
	dir := t.TempDir()
	writeRunbook(t, dir, resolverRunbook)
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &resolverFixture{client: client, jobs: dispatch.NewEntJobStore(client), dir: dir, runbooks: src, device: dev.DeviceID}
}

// writeRunbook writes body as the runbook web in dir, with a modification
// time a minute past the file it replaces, so the source compiles it again.
func writeRunbook(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, "web.yaml")
	later := time.Now()
	if info, err := os.Stat(path); err == nil {
		later = info.ModTime().Add(time.Minute)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// dispatchJob creates a job, claims its fan-out, pins (when pin is set) the
// graph the runbook source compiles now, exactly what the worker's Prepare
// pins, and records the fixture's device as dispatched, as the worker does.
func (f *resolverFixture) dispatchJob(t *testing.T, job *dispatch.Job, pin bool) string {
	t.Helper()
	ctx := context.Background()
	job.Actor = "tester"
	if job.RunbookID == "" {
		job.RunbookID = "web"
	}
	if err := f.jobs.Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	_, fence, err := f.jobs.BeginFanOut(ctx, job.JobID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if pin {
		rb, err := f.runbooks.Get(ctx, job.RunbookID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.jobs.PinGraph(ctx, job.JobID, fence, &dispatch.NodeGraph{Version: rb.Version, Scheme: runbook.GraphScheme, Digest: rb.GraphDigest, Nodes: rb.Nodes}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.jobs.RecordTask(ctx, job.JobID, fence, dispatch.JobTask{DeviceID: f.device, DeviceName: "web1", Outcome: dispatch.OutcomeDispatched}); err != nil {
		t.Fatal(err)
	}
	return job.JobID
}

// resolvedNodes are the answers the web runbook's pinned graph gives.
var resolvedNodes = []struct {
	node string
	want journal.NodeMethod
}{
	{"tasks[0]", journal.NodeMethod{Resolved: true, Method: "acme.inventory.read"}},
	{"tasks[1].item[3].attempt[1]", journal.NodeMethod{Resolved: true, Method: "facts.gather"}},
	{"tasks[2]", journal.NodeMethod{Resolved: true, Method: "pkg.install"}},
	{"tasks[3].undo[0]", journal.NodeMethod{Resolved: true, Method: "file.permissions"}},
	{"tasks[3].undo[1]", journal.NodeMethod{Reason: "the node's rollback: list has no such step"}},
	{"tasks[4].undo[0]", journal.NodeMethod{Resolved: true, Method: "file.permissions", AnyFirstParty: true}},
	{"tasks[5]", journal.NodeMethod{Resolved: true, Method: "facts.gather_next"}},
	{"tasks[5].undo[0]", journal.NodeMethod{Resolved: true, AnyFirstParty: true}},
	{"tasks[0].undo[0]", journal.NodeMethod{Reason: "the node's method records no undo the engine replays"}},
	{"tasks[9]", journal.NodeMethod{Reason: "the job's runbook graph has no such node"}},
}

// TestNodeResolver_ResolvesEachNodeFromThePinnedGraph: an ordinary job's
// nodes resolve from the graph its fan-out pinned: an item and a try of a
// task to the task, an undo step to the task's rollback: list, to the undo
// its method declares, or for a first-party method this catalog lacks to any
// first-party method, and a node the graph lacks to nothing. Editing the
// runbook, or removing it, changes no answer, for a resolver that read the
// job before and for one that never did.
func TestNodeResolver_ResolvesEachNodeFromThePinnedGraph(t *testing.T) {
	f := newResolverFixture(t)
	jobID := f.dispatchJob(t, &dispatch.Job{}, true)
	cached := newNodeResolver(f.jobs)
	check := func(label string, nodes *nodeResolver) {
		t.Helper()
		for _, tc := range resolvedNodes {
			got, err := nodes.Resolve(context.Background(), jobID, f.device, tc.node)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s: %s resolved to %+v, want %+v", label, tc.node, got, tc.want)
			}
		}
	}
	check("before any edit", cached)
	writeRunbook(t, f.dir, strings.Replace(resolverRunbook, "acme.inventory.read", "facts.gather", 1))
	check("a resolver that read the job, after an edit", cached)
	check("a fresh resolver, after an edit", newNodeResolver(f.jobs))
	if err := os.Remove(filepath.Join(f.dir, "web.yaml")); err != nil {
		t.Fatal(err)
	}
	check("a fresh resolver, with the runbook gone", newNodeResolver(f.jobs))
}

// TestNodeResolver_ResolvesNothingItCannotVouchFor: a job that pinned no
// graph (one fanned out before graphs were pinned, and a playbook job)
// resolves no node; a rollback job's nodes resolve from its plan, never the
// undone runbook, and a node or device the plan lacks to nothing.
func TestNodeResolver_ResolvesNothingItCannotVouchFor(t *testing.T) {
	f := newResolverFixture(t)
	ctx := context.Background()
	pinned := f.dispatchJob(t, &dispatch.Job{}, true)
	unpinned := f.dispatchJob(t, &dispatch.Job{}, false)
	playbook := f.dispatchJob(t, &dispatch.Job{Kind: "playbook"}, false)
	rb, err := f.runbooks.Get(ctx, "web")
	if err != nil {
		t.Fatal(err)
	}
	plan := &dispatch.RollbackPlan{DAGVersion: rb.Version, Devices: []dispatch.RollbackDevice{{DeviceID: f.device, DeviceName: "web1",
		Steps: []wire.RollbackStep{{Node: "tasks[4]", Method: "file.permissions"}, {Node: "tasks[2]", Method: "pkg.apt.remove"}}}}}
	undo := f.dispatchJob(t, &dispatch.Job{RollbackOf: pinned, Rollback: plan}, true)

	nodes := newNodeResolver(f.jobs)
	resolve := func(job, device, node string) journal.NodeMethod {
		t.Helper()
		got, err := nodes.Resolve(ctx, job, device, node)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for name, job := range map[string]string{"an unpinned job": unpinned, "a playbook job": playbook} {
		if got := resolve(job, f.device, "tasks[0]"); got.Resolved || !strings.Contains(got.Reason, "pinned no runbook graph") {
			t.Errorf("%s resolved %+v", name, got)
		}
	}
	if got := resolve(undo, f.device, "tasks[0]"); !reflect.DeepEqual(got, journal.NodeMethod{Resolved: true, Method: "file.permissions"}) {
		t.Errorf("the rollback's first step: %+v", got)
	}
	if got := resolve(undo, f.device, "tasks[1]"); got.Method != "pkg.apt.remove" {
		t.Errorf("the rollback's second step: %+v, want its plan's method, not the undone runbook's", got)
	}
	for _, miss := range [][2]string{{f.device, "tasks[2]"}, {"another-device", "tasks[0]"}} {
		if got := resolve(undo, miss[0], miss[1]); got.Resolved {
			t.Errorf("the rollback resolved %v as %+v", miss, got)
		}
	}
}

// failingJobs is the real job store whose Lookup fails, as a database that
// cannot be read does.
type failingJobs struct{ dispatch.JobStore }

// Lookup implements dispatch.JobStore by failing.
func (failingJobs) Lookup(context.Context, string) (*dispatch.Job, error) {
	return nil, errors.New("database is locked")
}

// TestNodeConsumers_StoreEachBatchUnderItsNodesMethod: the facts and journal
// consumers built as main builds them, over the real resolver, job store and
// runbook: a hostile batch naming a catalog method for the node that ran an
// external Collection's method stores no built-in fact and no undo, each
// under the node's method; an honest batch is stored as sent; and a record
// that cannot be read fails both batches, so the bus redelivers them.
func TestNodeConsumers_StoreEachBatchUnderItsNodesMethod(t *testing.T) {
	f := newResolverFixture(t)
	jobID := f.dispatchJob(t, &dispatch.Job{}, true)
	nodes := newNodeResolver(f.jobs)
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	factStore := entstore.New(f.client)
	factsSub, err := newFactsSubscriber(factStore, logger, journalAdmission(f.jobs), entstore.WithNodeResolver(nodes.Resolve))
	if err != nil {
		t.Fatal(err)
	}
	journalStore := journal.NewEntStore(f.client)
	journalSub := journal.NewSubscriber(journalStore, logger, journal.WithAdmission(journalAdmission(f.jobs)), journal.WithNodeResolver(nodes.Resolve))

	entries, _, _ := facts.Prepare(map[string]any{"host.name": "zq9value-web1", "acme.widgets": 3}, facts.Guard{})
	for _, by := range []facts.Provenance{{Node: "tasks[0]", Method: "facts.gather"}, {Node: "tasks[1]", Method: "facts.gather"}} {
		if err := factsSub.Handle(factsEvent(t, facts.Batch{JobID: jobID, DeviceID: f.device, Attempt: 1, ObservedAt: time.Now(), Entries: entries, By: by})); err != nil {
			t.Fatal(err)
		}
	}
	current, err := factStore.Current(context.Background(), f.device)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range current {
		got[e.Key] = e.By.Method + "@" + e.By.Node
	}
	// host.name is stored once, from the honest gather; the forged batch
	// kept only acme.widgets, which the honest batch saw again.
	if got["host.name"] != "facts.gather@tasks[1]" || got["acme.widgets"] != "acme.inventory.read@tasks[0]" {
		t.Errorf("stored %v", got)
	}
	if history, _ := factStore.History(context.Background(), f.device, "host.name"); len(history) != 1 || history[0].Value != "zq9value-web1" || history[0].By.Node != "tasks[1]" {
		t.Errorf("host.name history %+v, want only the honest gather's", history)
	}

	text := "/etc/motd"
	row := engine.JournalEntry{JobID: jobID, DeviceID: f.device, Attempt: 1, RunID: "run-1", Sequence: 1, NodeID: "tasks[0]",
		FQCN: "file.permissions", Outcome: engine.OutcomeChanged, ActionChanged: true,
		InverseFQCN: "file.permissions", InverseParamKeys: []string{"path"}, InverseParams: []engine.InverseParam{{Key: "path", Text: &text}}, InverseComplete: true,
		StartedAt: time.Now(), FinishedAt: time.Now()}
	honest := row
	honest.NodeID, honest.Sequence = "tasks[4]", 2
	// Undo steps of the node whose first-party method this catalog lacks:
	// one naming a first-party method is kept as sent, with its own undo,
	// and one naming an external method is not.
	newer := row
	newer.NodeID, newer.Sequence, newer.FQCN = "tasks[5].undo[0]", 3, "file.copy"
	foreign := newer
	foreign.Attempt, foreign.FQCN = 2, "acme.inventory.restore"
	if err := journalSub.Handle(journalEvent(t, journal.Batch{JobID: jobID, DeviceID: f.device, Attempt: 1, Entries: []engine.JournalEntry{row, honest, newer}})); err != nil {
		t.Fatal(err)
	}
	if err := journalSub.Handle(journalEvent(t, journal.Batch{JobID: jobID, DeviceID: f.device, Attempt: 2, Entries: []engine.JournalEntry{foreign}})); err != nil {
		t.Fatal(err)
	}
	stored, _, err := journalStore.ForJob(context.Background(), jobID, 0)
	if err != nil || len(stored) != 4 {
		t.Fatalf("stored %d rows, %v", len(stored), err)
	}
	for _, e := range stored {
		switch e.NodeID {
		case "tasks[0]":
			if e.FQCN != "acme.inventory.read" || e.InverseFQCN != "" || e.InverseComplete {
				t.Errorf("the forged row was stored as %q with undo %q", e.FQCN, e.InverseFQCN)
			}
		case "tasks[4]":
			if e.FQCN != "file.permissions" || e.InverseFQCN != "file.permissions" || !e.InverseComplete {
				t.Errorf("the honest row was stored as %+v", e)
			}
		case "tasks[5].undo[0]":
			switch {
			case e.Attempt == 1 && (e.FQCN != "file.copy" || e.InverseFQCN != "file.permissions" || !e.InverseComplete):
				t.Errorf("a newer method's first-party undo step was stored as %+v", e)
			case e.Attempt == 2 && (e.FQCN != engine.FQCNUnregistered || !e.FQCNUnresolved || e.InverseFQCN != ""):
				t.Errorf("a newer method's external undo step was stored as %q with undo %q", e.FQCN, e.InverseFQCN)
			}
		}
	}
	if log := logged.String(); !strings.Contains(log, "dispatched=acme.inventory.read") || !strings.Contains(log, "level=INFO msg=\""+journal.KeptNote) || strings.Contains(log, "zq9value") {
		t.Errorf("the log does not name the claim, or names a value:\n%s", log)
	}

	// A record that cannot be read: both batches are failed for redelivery.
	broken := newNodeResolver(failingJobs{f.jobs})
	factsBroken, err := newFactsSubscriber(factStore, logger, journalAdmission(f.jobs), entstore.WithNodeResolver(broken.Resolve))
	if err != nil {
		t.Fatal(err)
	}
	if err := factsBroken.Handle(factsEvent(t, facts.Batch{JobID: jobID, DeviceID: f.device, Attempt: 1, ObservedAt: time.Now(), Entries: entries,
		By: facts.Provenance{Node: "tasks[1]", Method: "facts.gather"}})); err == nil {
		t.Error("the facts batch was acknowledged though its node could not be resolved")
	}
	journalBroken := journal.NewSubscriber(journalStore, logger, journal.WithAdmission(journalAdmission(f.jobs)), journal.WithNodeResolver(broken.Resolve))
	again := honest
	again.Attempt = 2
	if err := journalBroken.Handle(journalEvent(t, journal.Batch{JobID: jobID, DeviceID: f.device, Attempt: 2, Entries: []engine.JournalEntry{again}})); err == nil {
		t.Error("the journal batch was acknowledged though its node could not be resolved")
	}
}

// factsEvent wraps batch as the facts subject carries it.
func factsEvent(t *testing.T, batch facts.Batch) event.Event {
	t.Helper()
	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	return event.Event{ID: "facts-1", Type: facts.EventType, Data: data}
}

// journalEvent wraps batch as the journal subject carries it.
func journalEvent(t *testing.T, batch journal.Batch) event.Event {
	t.Helper()
	data, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	return event.Event{ID: "journal-1", Type: journal.EventType, Data: data}
}

// TestNodeConsumers_JudgeIdentityAndSkewAlike: the facts and journal
// consumers, built as main builds them over the real resolver, judge alike.
// A program's Collection identity on a first-party method, and a first-party
// identity on an external method, are cleared with a warning by both, by the
// one rule (journal.IdentityProblem). An undo step naming a first-party undo
// this catalog does not declare is kept as sent, with the same note, whether
// the node's method is in the catalog (its declared undos grew in a newer
// release) or missing from it. A newer first-party method's facts are kept,
// built-in ones included, under its own name.
func TestNodeConsumers_JudgeIdentityAndSkewAlike(t *testing.T) {
	f := newResolverFixture(t)
	jobID := f.dispatchJob(t, &dispatch.Job{}, true)
	nodes := newNodeResolver(f.jobs)
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	factStore := entstore.New(f.client)
	factsSub, err := newFactsSubscriber(factStore, logger, journalAdmission(f.jobs), entstore.WithNodeResolver(nodes.Resolve))
	if err != nil {
		t.Fatal(err)
	}
	journalStore := journal.NewEntStore(f.client)
	journalSub := journal.NewSubscriber(journalStore, logger, journal.WithAdmission(journalAdmission(f.jobs)), journal.WithNodeResolver(nodes.Resolve))
	digest := func(c string) string { return wire.ManifestDigestPrefix + strings.Repeat(c, 64) }
	builtin := wire.CollectionIdentity{Group: "facts", Version: "0.4.0", Manifests: digest("a")}
	program := wire.CollectionIdentity{Group: "acme", Program: true, Manifests: digest("c")}

	// The same identity outcome from both consumers, node by node.
	for _, tc := range []struct {
		node, method, key string
		id                wire.CollectionIdentity
	}{
		{node: "tasks[1]", method: "facts.gather", key: "host.name", id: program},
		{node: "tasks[0]", method: "acme.inventory.read", key: "acme.widgets", id: builtin},
	} {
		entries, _, _ := facts.Prepare(map[string]any{tc.key: "zq9value"}, facts.Guard{})
		if err := factsSub.Handle(factsEvent(t, facts.Batch{JobID: jobID, DeviceID: f.device, Attempt: 1, ObservedAt: time.Now(), Entries: entries,
			By: facts.Provenance{Node: tc.node, Method: tc.method, Collection: tc.id}})); err != nil {
			t.Fatal(err)
		}
	}
	current, err := factStore.Current(context.Background(), f.device)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range current {
		if !e.By.Collection.IsZero() {
			t.Errorf("the facts consumer kept %s's identity %+v", e.Key, e.By.Collection)
		}
	}
	row := func(node, fqcn string, seq int, id wire.CollectionIdentity) engine.JournalEntry {
		return engine.JournalEntry{JobID: jobID, DeviceID: f.device, Attempt: 1, RunID: "run-1", Sequence: seq, NodeID: node, FQCN: fqcn,
			Outcome: engine.OutcomeRan, Collection: id, StartedAt: time.Now(), FinishedAt: time.Now()}
	}
	rows := []engine.JournalEntry{
		row("tasks[1]", "facts.gather", 1, program),
		row("tasks[0]", "acme.inventory.read", 2, builtin),
		// Skew of both kinds: the declared undos of a method in the catalog
		// grew, and a method missing from it.
		row("tasks[4].undo[0]", "file.copy", 3, wire.CollectionIdentity{}),
		row("tasks[5].undo[0]", "file.copy", 4, wire.CollectionIdentity{}),
	}
	if err := journalSub.Handle(journalEvent(t, journal.Batch{JobID: jobID, DeviceID: f.device, Attempt: 1, Entries: rows})); err != nil {
		t.Fatal(err)
	}
	stored, _, err := journalStore.ForJob(context.Background(), jobID, 0)
	if err != nil || len(stored) != len(rows) {
		t.Fatalf("stored %d rows, %v", len(stored), err)
	}
	for _, e := range stored {
		if !e.Collection.IsZero() {
			t.Errorf("the journal consumer kept %s's identity %+v", e.NodeID, e.Collection)
		}
		if strings.Contains(e.NodeID, ".undo[") && (e.FQCN != "file.copy" || e.FQCNUnresolved) {
			t.Errorf("an undo step outside this catalog's declarations, %s, was stored as %q", e.NodeID, e.FQCN)
		}
	}
	log := logged.String()
	if n := strings.Count(log, "without the Collection identity"); n != 4 {
		t.Errorf("%d identity warnings, want one per batch and row (4):\n%s", n, log)
	}
	if n := strings.Count(log, "an undo this controller's catalog does not declare"); n != 2 {
		t.Errorf("%d skew notes, want one for each kind (2):\n%s", n, log)
	}

	// A newer release's first-party method keeps its facts, built-in ones
	// included, under its own name, and says so at info.
	entries, _, _ := facts.Prepare(map[string]any{"host.next_release_key": 5}, facts.Guard{})
	if err := factsSub.Handle(factsEvent(t, facts.Batch{JobID: jobID, DeviceID: f.device, Attempt: 1, ObservedAt: time.Now(), Entries: entries,
		By: facts.Provenance{Node: "tasks[5]", Method: "facts.gather_next"}})); err != nil {
		t.Fatal(err)
	}
	current, err = factStore.Current(context.Background(), f.device)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range current {
		if e.Key == "host.next_release_key" {
			found = e.By.Method == "facts.gather_next" && e.By.Node == "tasks[5]"
		}
	}
	if !found || !strings.Contains(logged.String(), "reserves for built-ins") {
		t.Errorf("a newer first-party method's facts: %+v, want them stored under it and noted:\n%s", current, logged.String())
	}
	if strings.Contains(logged.String(), "zq9value") {
		t.Errorf("a value reached the log")
	}
}
