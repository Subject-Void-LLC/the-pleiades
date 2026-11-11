// Package journal_test: the consumer stores each row under the method the
// Controller dispatched for its node (nodes.go), never under the method its
// batch names, through a real store over a real SQLite database.
package journal_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// stubNodes answers a fixed NodeMethod per node id and records the ids it
// was asked for.
type stubNodes struct {
	answers map[string]journal.NodeMethod
	asked   []string
	err     error
}

// resolve implements journal.NodeResolver over s.
func (s *stubNodes) resolve(_ context.Context, _, _, nodeID string) (journal.NodeMethod, error) {
	s.asked = append(s.asked, nodeID)
	if s.err != nil {
		return journal.NodeMethod{}, s.err
	}
	// The Controller's resolver answers for the task an item or try of it
	// names, as this stub does.
	return s.answers[engine.BaseNodeID(nodeID)], nil
}

// recordedUndo gives e the undo, identity and program a Runner's projection
// writes for a reversible built-in.
func recordedUndo(e engine.JournalEntry) engine.JournalEntry {
	text := "/etc/motd"
	e.InverseFQCN = "file.permissions"
	e.InverseParamKeys = []string{"path"}
	e.InverseParams = []engine.InverseParam{{Key: "path", Text: &text}}
	e.InverseComplete = true
	e.InverseSpecDigest = digestOf("b")
	e.Collection = wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: digestOf("a")}
	return e
}

// TestSubscriber_StoresEachRowUnderItsNodesMethod: a row claiming a catalog
// method for a node that ran an external one is stored under the node's
// method with the identity and undo it brought cleared, and said at warning
// naming both methods; a row whose claim is the node's is stored as sent; a
// generic node's row claiming the concrete method it dispatched to is stored
// as the generic, since the engine names the task's own method; an undo step
// claiming one of the undos its node declares keeps it; a row whose node the
// Controller cannot resolve is stored with its method unresolved and nothing
// it carried; and a row the Runner could not resolve keeps that answer.
func TestSubscriber_StoresEachRowUnderItsNodesMethod(t *testing.T) {
	store, _ := newEntStore(t)
	var logged bytes.Buffer
	nodes := &stubNodes{answers: map[string]journal.NodeMethod{
		"tasks[0]":         {Resolved: true, Method: "acme.inventory.write"},
		"tasks[1]":         {Resolved: true, Method: "file.permissions"},
		"tasks[2]":         {Resolved: true, Method: "pkg.install"},
		"tasks[3].undo[0]": {Resolved: true, Alternatives: []string{"file.copy", "file.permissions"}},
		"tasks[5]":         {Reason: "the job has no pinned runbook version"},
		"tasks[6]":         {Resolved: true, Method: "file.permissions"},
	}}
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), journal.WithNodeResolver(nodes.resolve))

	forged := recordedUndo(walkEntry("job-1", "device-1", 1, 1, "tasks[0]"))
	forged.FQCN = "file.permissions"
	honest := recordedUndo(walkEntry("job-1", "device-1", 1, 2, "tasks[1].item[2]"))
	honest.FQCN = "file.permissions"
	generic := walkEntry("job-1", "device-1", 1, 3, "tasks[2]")
	generic.FQCN = "pkg.apt.install"
	undo := walkEntry("job-1", "device-1", 1, 4, "tasks[3].undo[0]")
	undo.FQCN = "file.permissions"
	unresolvable := recordedUndo(walkEntry("job-1", "device-1", 1, 5, "tasks[5]"))
	unresolvable.FQCN = "file.permissions"
	runnerUnresolved := walkEntry("job-1", "device-1", 1, 6, "tasks[6]")
	runnerUnresolved.FQCN, runnerUnresolved.FQCNUnresolved = engine.FQCNUnregistered, true
	batch := journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1,
		Entries: []engine.JournalEntry{forged, honest, generic, undo, unresolvable, runnerUnresolved}}

	if err := sub.Handle(batchEvent(t, batch)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	stored, _, err := store.ForJob(context.Background(), "job-1", 0)
	if err != nil || len(stored) != len(batch.Entries) {
		t.Fatalf("stored %d entries, %v; want all %d", len(stored), err, len(batch.Entries))
	}
	byNode := map[string]engine.JournalEntry{}
	for _, e := range stored {
		byNode[e.NodeID] = e
	}
	cleared := func(e engine.JournalEntry) bool {
		return e.Collection.IsZero() && e.InverseFQCN == "" && len(e.InverseParams) == 0 && !e.InverseComplete && e.InverseSpecDigest == ""
	}
	if e := byNode["tasks[0]"]; e.FQCN != "acme.inventory.write" || e.FQCNUnresolved || !cleared(e) {
		t.Errorf("the forged row was stored as %q (unresolved %v), identity %+v, undo %q: want the node's method and nothing it brought", e.FQCN, e.FQCNUnresolved, e.Collection, e.InverseFQCN)
	}
	if e := byNode["tasks[1].item[2]"]; e.FQCN != "file.permissions" || cleared(e) || e.InverseFQCN != "file.permissions" {
		t.Errorf("the honest row lost what it carried: %+v", e)
	}
	if e := byNode["tasks[2]"]; e.FQCN != "pkg.install" {
		t.Errorf("a generic node's row naming its concrete method was stored as %q, want pkg.install", e.FQCN)
	}
	if e := byNode["tasks[3].undo[0]"]; e.FQCN != "file.permissions" {
		t.Errorf("an undo step naming a declared undo was stored as %q", e.FQCN)
	}
	if e := byNode["tasks[5]"]; e.FQCN != engine.FQCNUnregistered || !e.FQCNUnresolved || !cleared(e) {
		t.Errorf("an unresolvable row was stored as %q (unresolved %v) with %+v: want it unresolved with nothing it carried", e.FQCN, e.FQCNUnresolved, e)
	}
	if e := byNode["tasks[6]"]; e.FQCN != engine.FQCNUnregistered || !e.FQCNUnresolved {
		t.Errorf("a row the Runner could not resolve was stored as %q", e.FQCN)
	}
	// The resolver is asked with the row's own node id, item and all.
	if !strings.Contains(strings.Join(nodes.asked, ","), "tasks[1].item[2]") {
		t.Errorf("the resolver was asked %q", nodes.asked)
	}
	log := logged.String()
	for _, want := range []string{"claimed=file.permissions", "dispatched=acme.inventory.write", "dispatched=pkg.install", "entries=1", "no pinned runbook version"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log does not say %s:\n%s", want, log)
		}
	}
	if strings.Contains(log, "/etc/motd") {
		t.Errorf("a recorded value reached the log:\n%s", log)
	}
}

// TestSubscriber_HoldsAResolvedRowsProgramToItsMethod: a row whose claim its
// node ran keeps its program only where the method can have one: a
// first-party method named with a program loses the program and identity,
// and an external method named with a first-party identity loses the
// identity.
func TestSubscriber_HoldsAResolvedRowsProgramToItsMethod(t *testing.T) {
	store, _ := newEntStore(t)
	nodes := &stubNodes{answers: map[string]journal.NodeMethod{
		"tasks[0]": {Resolved: true, Method: "file.permissions"},
		"tasks[1]": {Resolved: true, Method: "acme.inventory.write"},
		"tasks[2]": {Resolved: true, Method: "acme.inventory.write"},
	}}
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), journal.WithNodeResolver(nodes.resolve))
	builtin := wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: digestOf("a")}
	program := wire.CollectionIdentity{Group: "acme", Program: true, Manifests: digestOf("c")}
	a := walkEntry("job-1", "device-1", 1, 1, "tasks[0]")
	a.FQCN, a.ProviderProgram, a.ProviderDigest, a.Collection = "file.permissions", "acme", digestOf("d"), program
	b := walkEntry("job-1", "device-1", 1, 2, "tasks[1]")
	b.FQCN, b.Collection = "acme.inventory.write", builtin
	c := walkEntry("job-1", "device-1", 1, 3, "tasks[2]")
	c.FQCN, c.ProviderProgram, c.ProviderDigest, c.Collection = "acme.inventory.write", "acme", digestOf("d"), program
	if err := sub.Handle(batchEvent(t, journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1, Entries: []engine.JournalEntry{a, b, c}})); err != nil {
		t.Fatal(err)
	}
	stored, _, err := store.ForJob(context.Background(), "job-1", 0)
	if err != nil || len(stored) != 3 {
		t.Fatalf("stored %d, %v", len(stored), err)
	}
	byNode := map[string]engine.JournalEntry{}
	for _, e := range stored {
		byNode[e.NodeID] = e
	}
	if e := byNode["tasks[0]"]; e.ProviderProgram != "" || !e.Collection.IsZero() {
		t.Errorf("a first-party method kept a program: %+v", e)
	}
	if e := byNode["tasks[1]"]; !e.Collection.IsZero() {
		t.Errorf("an external method kept a first-party identity: %+v", e.Collection)
	}
	if e := byNode["tasks[2]"]; e.ProviderProgram != "acme" || e.Collection != program {
		t.Errorf("an external method's own program and identity were not kept: %+v", e)
	}
}

// TestSubscriber_RedeliversWhenANodeCannotBeResolvedNow: a resolver that
// cannot read the record fails the batch, so the bus delivers it again, and
// nothing is stored.
func TestSubscriber_RedeliversWhenANodeCannotBeResolvedNow(t *testing.T) {
	store, _ := newEntStore(t)
	nodes := &stubNodes{err: errors.New("database is locked")}
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), journal.WithNodeResolver(nodes.resolve))
	batch := journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1, Entries: []engine.JournalEntry{walkEntry("job-1", "device-1", 1, 1, "tasks[0]")}}
	if err := sub.Handle(batchEvent(t, batch)); err == nil {
		t.Fatal("a resolver error was acknowledged")
	}
	if stored, _, _ := store.ForJob(context.Background(), "job-1", 0); len(stored) != 0 {
		t.Errorf("stored %d entries after a resolver error", len(stored))
	}
}

// TestNodeMethod_Judge pins the answers the consumers act on: every verdict,
// the method each stores under, and a reason for every unresolved one.
func TestNodeMethod_Judge(t *testing.T) {
	declared := journal.NodeMethod{Resolved: true, Method: "file.permissions", AnyFirstParty: true}
	several := journal.NodeMethod{Resolved: true, Alternatives: []string{"file.copy", "file.remove"}, AnyFirstParty: true}
	newer := journal.NodeMethod{Resolved: true, AnyFirstParty: true}
	for _, tc := range []struct {
		name    string
		node    journal.NodeMethod
		claim   string
		method  string
		verdict journal.Verdict
	}{
		{"the node's own method", journal.NodeMethod{Resolved: true, Method: "a.b"}, "a.b", "a.b", journal.VerdictNamed},
		{"another method", journal.NodeMethod{Resolved: true, Method: "a.b"}, "c.d", "a.b", journal.VerdictMismatch},
		{"an empty claim on a method's node", journal.NodeMethod{Resolved: true, Method: "a.b"}, "", "a.b", journal.VerdictMismatch},
		{"the marker's empty claim", journal.NodeMethod{Resolved: true}, "", "", journal.VerdictNamed},
		{"a method claimed for the marker", journal.NodeMethod{Resolved: true}, "a.b", "", journal.VerdictUnresolved},
		{"an unresolved node", journal.NodeMethod{Reason: "gone"}, "a.b", "", journal.VerdictUnresolved},
		{"the declared undo", declared, "file.permissions", "file.permissions", journal.VerdictNamed},
		{"an undo a newer release declares", declared, "file.copy", "file.copy", journal.VerdictKept},
		// An external claim on a first-party method's undo step is unresolved
		// whatever the catalog declares: none (newer), one (declared) or
		// several.
		{"an external undo of a declared method", declared, "acme.restore", "", journal.VerdictUnresolved},
		{"an empty undo of a declared method", declared, "", "", journal.VerdictUnresolved},
		{"one of several declared undos", several, "file.remove", "file.remove", journal.VerdictNamed},
		{"another first-party undo of several", several, "file.permissions", "file.permissions", journal.VerdictKept},
		{"an external undo of several", several, "acme.restore", "", journal.VerdictUnresolved},
		{"an empty undo of several", several, "", "", journal.VerdictUnresolved},
		{"a newer method's first-party undo", newer, "file.copy", "file.copy", journal.VerdictKept},
		{"a newer method's external undo", newer, "acme.restore", "", journal.VerdictUnresolved},
		{"a newer method's engine action undo", newer, "ssh_exec", "", journal.VerdictUnresolved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.node.Judge(tc.claim)
			if got.Method != tc.method || got.Verdict != tc.verdict {
				t.Errorf("Judge(%q) = %+v; want %q, verdict %d", tc.claim, got, tc.method, tc.verdict)
			}
			if (got.Verdict == journal.VerdictUnresolved) != (got.Reason != "") {
				t.Errorf("Judge(%q) = %+v: a reason goes with an unresolved verdict, and only with one", tc.claim, got)
			}
		})
	}
	for _, node := range []journal.NodeMethod{newer, declared, several} {
		if got := node.Judge("acme.restore"); got.Verdict != journal.VerdictUnresolved || !strings.Contains(got.Reason, "not a first-party Collection method") {
			t.Errorf("an external undo of a first-party method with undos %q %q: %+v, want it unresolved saying why", node.Method, node.Alternatives, got)
		}
	}
	for name, want := range map[string]bool{"file.copy": true, "ssh_exec": true, "pleiades.builtin.fail": true, "net.new.method": true, "acme.x": false, "nodots": false, "": false} {
		if got := journal.FirstPartyMethod(name); got != want {
			t.Errorf("FirstPartyMethod(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestIdentityProblem pins the one identity rule both consumers apply.
func TestIdentityProblem(t *testing.T) {
	builtin := wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: digestOf("a")}
	program := wire.CollectionIdentity{Group: "acme", Program: true, Manifests: digestOf("c")}
	for _, tc := range []struct {
		method, program string
		id              wire.CollectionIdentity
		problem         bool
	}{
		{"file.copy", "", builtin, false},
		{"file.copy", "", wire.CollectionIdentity{}, false},
		{"file.copy", "", program, true},
		{"file.copy", "acme", wire.CollectionIdentity{}, true},
		{"db.mysql.query", "", program, true},
		{"acme.read", "acme", program, false},
		{"acme.read", "", wire.CollectionIdentity{}, false},
		{"acme.read", "", builtin, true},
	} {
		if got := journal.IdentityProblem(tc.method, tc.id, tc.program); (got != "") != tc.problem {
			t.Errorf("IdentityProblem(%q, %+v, %q) = %q, want a problem %v", tc.method, tc.id, tc.program, got, tc.problem)
		}
	}
}

// TestSubscriber_SaysWhatItChangedOnlyOnceStored: a batch whose write fails
// says nothing of what was changed of it (a claim held to its node, an
// identity cleared, a claim kept), and the redelivery that stores it says
// each once.
func TestSubscriber_SaysWhatItChangedOnlyOnceStored(t *testing.T) {
	store, path := newEntStore(t)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("CREATE TRIGGER refuse_for_the_test BEFORE INSERT ON journal_entries BEGIN SELECT RAISE(ABORT, 'refused for the test'); END"); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	nodes := &stubNodes{answers: map[string]journal.NodeMethod{
		"tasks[0]":         {Resolved: true, Method: "acme.inventory.write"},
		"tasks[1]":         {Resolved: true, Method: "file.permissions"},
		"tasks[2].undo[0]": {Resolved: true, Method: "file.permissions", AnyFirstParty: true},
	}}
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)), journal.WithNodeResolver(nodes.resolve))
	mismatch := walkEntry("job-1", "device-1", 1, 1, "tasks[0]")
	mismatch.FQCN = "file.permissions"
	identity := walkEntry("job-1", "device-1", 1, 2, "tasks[1]")
	identity.FQCN, identity.Collection = "file.permissions", wire.CollectionIdentity{Group: "acme", Program: true, Manifests: digestOf("c")}
	kept := walkEntry("job-1", "device-1", 1, 3, "tasks[2].undo[0]")
	kept.FQCN = "file.copy"
	evt := batchEvent(t, journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1, Entries: []engine.JournalEntry{mismatch, identity, kept}})
	notes := []string{"not the one its batch names", "without the Collection identity", journal.KeptNote}

	if err := sub.Handle(evt); err == nil {
		t.Fatal("a failed write was acknowledged")
	}
	for _, note := range notes {
		if strings.Contains(logged.String(), note) {
			t.Errorf("a batch that was not stored said %q:\n%s", note, logged.String())
		}
	}
	if _, err := db.Exec("DROP TRIGGER refuse_for_the_test"); err != nil {
		t.Fatal(err)
	}
	if err := sub.Handle(evt); err != nil {
		t.Fatal(err)
	}
	for _, note := range notes {
		if n := strings.Count(logged.String(), note); n != 1 {
			t.Errorf("the stored batch said %q %d times, want once:\n%s", note, n, logged.String())
		}
	}
}
