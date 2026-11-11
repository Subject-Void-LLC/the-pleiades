// Package journal_test: the Controller holds a Runner's journal identity to
// wire's frozen Deliverable rule, never to Valid, whose Unicode tables are
// its own toolchain's, and what it says about a cleared identity or undo
// digest repeats none of the bytes the batch delivered.
package journal_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSubscriberKeepsAnIdentityOnlyThisToolchainsTablesRefuse: a program's
// group holding a code point this toolchain has not assigned, which Valid
// refuses here and which is what a letter a newer toolchain assigned looks
// like to this build, on an entry that
// program ran, is stored as sent, with one info line naming the node and
// repeating none of the group.
func TestSubscriberKeepsAnIdentityOnlyThisToolchainsTablesRefuse(t *testing.T) {
	store, _ := newEntStore(t)
	var logged bytes.Buffer
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)))
	const marker = "zq9probe"
	unassigned := unassignedHere(t)
	group := marker + string(unassigned)
	id := wire.CollectionIdentity{Group: group, Program: true, Manifests: digestOf("c")}
	if id.Valid() == nil {
		t.Fatal("the fixture needs an identity this build's Valid refuses")
	}
	e := walkEntry("job-1", "device-1", 1, 1, "tasks[0]")
	e.Collection, e.ProviderProgram = id, group
	if err := sub.Handle(batchEvent(t, journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1, Entries: []engine.JournalEntry{e}})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	stored, _, err := store.ForJob(context.Background(), "job-1", 0)
	if err != nil || len(stored) != 1 || stored[0].Collection != id {
		t.Fatalf("stored %+v, %v; want the entry with its identity as sent", stored, err)
	}
	log := logged.String()
	if !strings.Contains(log, "level=INFO") || !strings.Contains(log, "node=tasks[0]") || strings.Contains(log, "level=WARN") {
		t.Errorf("want one info line naming the node and no warning:\n%s", log)
	}
	if strings.Contains(log, marker) || strings.ContainsRune(log, unassigned) {
		t.Errorf("a log line repeats the group:\n%s", log)
	}
}

// TestSubscriberWarningsRepeatNothingDelivered: an undo digest of a megabyte,
// a first-party identity on an entry a program named only by the batch ran,
// and a program's identity on an entry no program ran are each cleared, and
// each warning is fixed text naming none of the digest, the program or the
// group.
func TestSubscriberWarningsRepeatNothingDelivered(t *testing.T) {
	store, _ := newEntStore(t)
	var logged bytes.Buffer
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)))
	const marker = "zq9marker"
	hugeUndo := strings.Repeat(marker, 1<<20/len(marker))
	undo := walkEntry("job-1", "device-1", 1, 1, "tasks[0]")
	undo.InverseSpecDigest = hugeUndo
	firstParty := walkEntry("job-1", "device-1", 1, 2, "tasks[1]")
	firstParty.Collection = wire.CollectionIdentity{Group: "exec", Version: "0.4.0", Manifests: digestOf("a")}
	firstParty.ProviderProgram = marker + "-program"
	program := walkEntry("job-1", "device-1", 1, 3, "tasks[2]")
	program.Collection = wire.CollectionIdentity{Group: marker + "-group", Program: true, Manifests: digestOf("c")}
	if err := sub.Handle(batchEvent(t, journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1, Entries: []engine.JournalEntry{undo, firstParty, program}})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	stored, _, err := store.ForJob(context.Background(), "job-1", 0)
	if err != nil || len(stored) != 3 {
		t.Fatalf("stored %d entries, %v; want all three kept", len(stored), err)
	}
	for _, e := range stored {
		if e.InverseSpecDigest != "" || !e.Collection.IsZero() {
			t.Errorf("%s: stored %+v and an undo digest of %d bytes, want both cleared", e.NodeID, e.Collection, len(e.InverseSpecDigest))
		}
	}
	log := logged.String()
	for _, node := range []string{"tasks[0]", "tasks[1]", "tasks[2]"} {
		if !strings.Contains(log, "node="+node) {
			t.Errorf("no warning names %s", node)
		}
	}
	// The one program name the store keeps is the entry's own column, which
	// the warning does not print either.
	if strings.Contains(log, marker) || len(log) > 4096 {
		t.Errorf("a warning repeats what the batch delivered (%d bytes logged): %.300s", len(log), log)
	}
}

// TestSubscriberNamesNoNodeOrMethodUnbounded: an entry's node and method are
// the batch's own text, so a Runner can send a megabyte of each holding
// direction overrides. The warning about an identity cleared, and the info
// line about one this toolchain would not make, name both bounded and
// escaped; the store's own warning does too.
func TestSubscriberNamesNoNodeOrMethodUnbounded(t *testing.T) {
	override := string(rune(0x202e))
	huge := strings.Repeat("zq9"+override, 1<<20/6)
	store, _ := newEntStore(t)
	var logged bytes.Buffer
	sub := journal.NewSubscriber(store, slog.New(slog.NewTextHandler(&logged, nil)))
	unassigned := string(unassignedHere(t))
	cleared := walkEntry("job-1", "device-1", 1, 1, "tasks[0]"+huge)
	cleared.FQCN = "exec." + huge
	cleared.Collection = wire.CollectionIdentity{Group: "acme", Program: true, Manifests: digestOf("c")}
	notMade := walkEntry("job-1", "device-1", 1, 2, "tasks[1]"+huge)
	notMade.FQCN = "exec." + huge
	notMade.Collection = wire.CollectionIdentity{Group: "acme" + unassigned, Program: true, Manifests: digestOf("c")}
	notMade.ProviderProgram = "acme" + unassigned
	if err := sub.Handle(batchEvent(t, journal.Batch{JobID: "job-1", DeviceID: "device-1", Attempt: 1, Entries: []engine.JournalEntry{cleared, notMade}})); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("logged %d lines, want a warning and an info line", len(lines))
	}
	for _, line := range lines {
		if len(line) > 2048 || strings.Contains(line, override) {
			t.Errorf("a line of %d bytes repeats the node or method: %.300s", len(line), line)
		}
	}
}
