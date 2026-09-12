// Package native: tests for the Walk tier's run journal sink.
//
// These reuse this package's own mockBus and failingBus rather than a
// stand-in of their own, so the subject under test is the publisher and
// not a second fake that could disagree with the first.
package native

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// quietLogger is a logger that discards everything, for the cases that
// are not about what was logged.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// journalEntries builds a level's worth of entries with the sequence
// numbers the engine would have given them.
func journalEntries(runID string, sequences ...int) []engine.JournalEntry {
	entries := make([]engine.JournalEntry, 0, len(sequences))
	for _, seq := range sequences {
		entries = append(entries, engine.JournalEntry{
			RunID:    runID,
			Sequence: seq,
			NodeID:   "tasks[0]",
			FQCN:     "exec.command",
			DeviceID: "device-1",
			Outcome:  engine.OutcomeChanged,
		})
	}
	return entries
}

// decodeBatch pulls the journal batch out of the most recent publish.
func decodeBatch(t *testing.T, bus *mockBus) journalBatch {
	t.Helper()
	if len(bus.published) == 0 {
		t.Fatal("nothing was published")
	}
	var batch journalBatch
	if err := json.Unmarshal(bus.published[len(bus.published)-1].evt.Data, &batch); err != nil {
		t.Fatalf("the published payload is not a journal batch: %v", err)
	}
	return batch
}

func TestJournalPublisherStampsTheDispatchIdentity(t *testing.T) {
	// JobID and Attempt are the sink's to stamp, not the run's to
	// report. A run cannot know which dispatch it is serving, and the
	// engine leaves both at zero for exactly that reason.
	bus := &mockBus{}
	ctx := journal.WithAttempt(context.Background(), 3)
	p := newJournalPublisher(ctx, bus, quietLogger(), "job-1", "device-1")

	if err := p.Record(ctx, journalEntries("run-1", 1, 2)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	batch := decodeBatch(t, bus)
	if batch.JobID != "job-1" || batch.Attempt != 3 || batch.DeviceID != "device-1" {
		t.Errorf("batch = %+v, want job-1 / device-1 / attempt 3", batch)
	}
	if len(batch.Entries) != 2 {
		t.Fatalf("batch carries %d entries, want 2", len(batch.Entries))
	}
	for i, e := range batch.Entries {
		if e.JobID != "job-1" {
			t.Errorf("entry %d carries job id %q, want job-1", i, e.JobID)
		}
		if e.Attempt != 3 {
			t.Errorf("entry %d carries attempt %d, want 3", i, e.Attempt)
		}
		if e.RunID != "run-1" {
			t.Errorf("entry %d lost its run id: %q", i, e.RunID)
		}
	}
}

func TestJournalPublisherDoesNotMutateTheCallersEntries(t *testing.T) {
	// The engine still holds the slice it handed over. A sink that
	// stamped in place would be writing into another package's state at
	// a level barrier.
	bus := &mockBus{}
	ctx := journal.WithAttempt(context.Background(), 2)
	p := newJournalPublisher(ctx, bus, quietLogger(), "job-1", "device-1")

	entries := journalEntries("run-1", 1)
	if err := p.Record(ctx, entries); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if entries[0].JobID != "" || entries[0].Attempt != 0 {
		t.Errorf("Record stamped the caller's own entry: %+v", entries[0])
	}
}

func TestJournalPublisherUsesTheJobsOwnSubject(t *testing.T) {
	bus := &mockBus{}
	ctx := journal.WithAttempt(context.Background(), 0)
	p := newJournalPublisher(ctx, bus, quietLogger(), "job-1", "device-1")

	if err := p.Record(ctx, journalEntries("run-1", 1)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got, want := bus.published[0].topic, topology.JournalSubject("job-1"); got != want {
		t.Errorf("published to %q, want %q", got, want)
	}
}

func TestJournalPublisherKeyIsStableAcrossARetry(t *testing.T) {
	// A redelivered dispatch re-running the same level must produce the
	// same key, or the duplicate rows it writes are uncollapsible. RunID
	// is deliberately not part of the key because the engine mints a
	// fresh one on every Run call, which is exactly what a redelivery
	// does.
	bus := &mockBus{}
	ctx := journal.WithAttempt(context.Background(), 1)
	p := newJournalPublisher(ctx, bus, quietLogger(), "job-1", "device-1")

	if err := p.Record(ctx, journalEntries("run-first", 1, 2)); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	if err := p.Record(ctx, journalEntries("run-second", 1, 2)); err != nil {
		t.Fatalf("second Record: %v", err)
	}
	if bus.published[0].evt.ID != bus.published[1].evt.ID {
		t.Errorf("the same level under two run ids published two keys: %q and %q",
			bus.published[0].evt.ID, bus.published[1].evt.ID)
	}
	if !strings.Contains(bus.published[0].evt.ID, "job-1") {
		t.Errorf("the key does not name the dispatch: %q", bus.published[0].evt.ID)
	}
}

func TestJournalPublisherKeyDistinguishesLevelsAndAttempts(t *testing.T) {
	bus := &mockBus{}
	base := context.Background()

	first := newJournalPublisher(journal.WithAttempt(base, 1), bus, quietLogger(), "job-1", "device-1")
	if err := first.Record(base, journalEntries("run-1", 1, 2)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := first.Record(base, journalEntries("run-1", 3)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	retry := newJournalPublisher(journal.WithAttempt(base, 2), bus, quietLogger(), "job-1", "device-1")
	if err := retry.Record(base, journalEntries("run-2", 1, 2)); err != nil {
		t.Fatalf("Record: %v", err)
	}

	keys := map[string]bool{}
	for _, p := range bus.published {
		if keys[p.evt.ID] {
			t.Errorf("two different batches share the key %q", p.evt.ID)
		}
		keys[p.evt.ID] = true
	}
	if len(keys) != 3 {
		t.Errorf("three distinct batches produced %d keys", len(keys))
	}
}

func TestJournalPublisherPublishesNothingForAnEmptyLevel(t *testing.T) {
	bus := &mockBus{}
	p := newJournalPublisher(context.Background(), bus, quietLogger(), "job-1", "device-1")
	if err := p.Record(context.Background(), nil); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(bus.published) != 0 {
		t.Errorf("an empty level published %d messages", len(bus.published))
	}
}

func TestJournalPublisherReportsAPublishFailure(t *testing.T) {
	// The engine logs and counts this and leaves the run's outcome
	// alone. What must not happen is a panic or a swallowed error: on
	// this tier an execution error Naks for redelivery, so a failed
	// audit write that became one would re-run the runbook against the
	// same device.
	p := newJournalPublisher(context.Background(), failingBus{}, quietLogger(), "job-1", "device-1")
	err := p.Record(context.Background(), journalEntries("run-1", 1))
	if err == nil {
		t.Fatal("Record swallowed a publish failure")
	}
	if !strings.Contains(err.Error(), "job-1") {
		t.Errorf("the error does not name the dispatch: %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("the error was misreported as a cancellation: %v", err)
	}
}

func TestNewJournalPublisherDefaultsAMissingAttemptToZero(t *testing.T) {
	// A caller outside the Runner's delivery path has no redelivery
	// count to give, and zero is what JournalEntry documents for a tier
	// with no dispatch at all.
	bus := &mockBus{}
	p := newJournalPublisher(context.Background(), bus, quietLogger(), "job-1", "device-1")
	if err := p.Record(context.Background(), journalEntries("run-1", 1)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := decodeBatch(t, bus).Attempt; got != 0 {
		t.Errorf("attempt = %d, want 0 when the context carries none", got)
	}
}

func TestJournalPublisherCarriesTheAttemptAcrossADetachedContext(t *testing.T) {
	// The whole reason the attempt travels as a context value is that it
	// survives the two detachments between the Runner and the sink: the
	// Runner's own lease context and the engine's WithoutCancel. Both
	// keep values and drop only cancellation.
	bus := &mockBus{}
	delivered := journal.WithAttempt(context.Background(), 4)
	p := newJournalPublisher(delivered, bus, quietLogger(), "job-1", "device-1")

	detached, cancel := context.WithCancel(context.WithoutCancel(delivered))
	cancel()
	recordCtx := context.WithoutCancel(detached)

	if err := p.Record(recordCtx, journalEntries("run-1", 1)); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := decodeBatch(t, bus).Attempt; got != 4 {
		t.Errorf("attempt = %d, want 4 after two detachments", got)
	}
}

func TestAdapterExecutePublishesTheRunJournal(t *testing.T) {
	// The wiring proof. Everything above tests the publisher directly;
	// this drives the real Adapter.Execute so a sink that was written but
	// never attached to the executor would fail here rather than pass
	// every unit test.
	bus := &mockBus{}
	runbooks := writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: noop\n    params:\n      changed: true\n")
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	ctx := journal.WithAttempt(context.Background(), 2)
	payload := wire.DispatchPayload{
		JobID: "job-1", RunbookID: "pb-1",
		DeviceID: "device-1", DeviceName: "router1", DeviceHost: "10.0.0.1",
	}
	if err := adapter.Execute(ctx, payload); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var batches []journalBatch
	for _, p := range bus.published {
		if p.topic != topology.JournalSubject("job-1") {
			continue
		}
		var batch journalBatch
		if err := json.Unmarshal(p.evt.Data, &batch); err != nil {
			t.Fatalf("a message on the journal subject is not a batch: %v", err)
		}
		batches = append(batches, batch)
	}
	if len(batches) == 0 {
		t.Fatal("Execute published no run journal at all, so the sink is wired to nothing")
	}

	var entries int
	for _, batch := range batches {
		if batch.JobID != "job-1" || batch.DeviceID != "device-1" {
			t.Errorf("batch names %s/%s, want job-1/device-1", batch.JobID, batch.DeviceID)
		}
		if batch.Attempt != 2 {
			t.Errorf("batch carries attempt %d, want the 2 the delivery set", batch.Attempt)
		}
		entries += len(batch.Entries)
	}
	if entries != 1 {
		t.Errorf("the journal recorded %d entries for a one-task runbook, want 1", entries)
	}
}
