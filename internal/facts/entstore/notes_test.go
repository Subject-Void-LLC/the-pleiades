// Tests of what the Controller's facts consumer says about a stored batch:
// nothing a batch delivered reaches a log line unbounded or raw, and nothing
// is said for a batch the store did not take, so a redelivered batch is not
// noted twice.
package entstore_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSubscriber_NotesRepeatNoMethodUnbounded: a batch's method is its own
// text, so a Runner can send a megabyte of it full of direction overrides.
// Each line the consumer logs about a stored batch (an identity cleared, one
// this toolchain would not make, one routed otherwise, built-in facts from a
// method outside the catalog) names it bounded and escaped.
func TestSubscriber_NotesRepeatNoMethodUnbounded(t *testing.T) {
	override := string(rune(0x202E))
	method := "acme." + strings.Repeat("zq9"+override, 1<<20/6)
	tagged := wire.CollectionIdentity{Group: "acme" + string(rune(0xE0041)), Program: true, Manifests: gatherIdentity.Manifests}
	unassigned := wire.CollectionIdentity{Group: "acme" + string(unassignedHere(t)), Program: true, Manifests: gatherIdentity.Manifests}
	routed := wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: gatherIdentity.Manifests}
	for name, id := range map[string]wire.CollectionIdentity{"cleared": tagged, "not made here": unassigned, "routed otherwise": routed} {
		t.Run(name, func(t *testing.T) {
			client, device := open(t)
			store := entstore.New(client)
			var logged bytes.Buffer
			sub := noteSubscriber(t, store, &logged)
			batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
				Entries: prepared(t, map[string]any{"edge.site": "dc1"}), By: facts.Provenance{Method: method, Collection: id}}
			if err := handle(t, sub, batch); err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
			if len(lines) < 2 {
				t.Fatalf("logged %d lines, want the identity line and the built-in note:\n%.400s", len(lines), logged.String())
			}
			for _, line := range lines {
				if len(line) > 2048 || strings.Contains(line, override) {
					t.Errorf("a line of %d bytes repeats the method: %.300s", len(line), line)
				}
			}
		})
	}
}

// TestSubscriber_SaysNothingOfABatchItDidNotStore: a store that fails (its
// database closed) returns the error, so the bus redelivers the batch, and
// the consumer says nothing about the identity or the built-in facts, which
// it says once the store takes the batch.
func TestSubscriber_SaysNothingOfABatchItDidNotStore(t *testing.T) {
	client, device := open(t)
	store := entstore.New(client)
	var logged bytes.Buffer
	sub := noteSubscriber(t, store, &logged)
	tagged := wire.CollectionIdentity{Group: "acme" + string(rune(0xE0041)), Program: true, Manifests: gatherIdentity.Manifests}
	batch := facts.Batch{JobID: "job-1", DeviceID: device, Attempt: 1, ObservedAt: time.Now(),
		Entries: prepared(t, map[string]any{"edge.site": "dc1"}), By: facts.Provenance{Method: "newer.gather", Collection: tagged}}
	_ = client.Close()
	if err := handle(t, sub, batch); err == nil {
		t.Fatal("a batch the store failed to write was acknowledged")
	}
	if notes := noteLines(logged.String()); len(notes) != 0 || len(identityLines(logged.String())) != 0 {
		t.Errorf("said something of a batch it did not store:\n%s", logged.String())
	}
}
