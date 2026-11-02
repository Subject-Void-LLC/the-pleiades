// Package native: tests for the Walk tier's fact sink (facts.go), over this
// package's own mockBus and failingBus.
package native

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

func factEntries(t *testing.T) []facts.Entry {
	t.Helper()
	entries, _, err := facts.Prepare(map[string]any{"ansible_kernel": "6.1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// TestFactsPublisher_PublishesTheDispatchsBatchOnItsJobsSubject: the batch
// names the dispatch's job, device and attempt, rides the job's own facts
// subject, and a retried publish reproduces its key while another
// observation does not.
func TestFactsPublisher_PublishesTheDispatchsBatchOnItsJobsSubject(t *testing.T) {
	bus := &mockBus{}
	p := newFactsPublisher(journal.WithAttempt(context.Background(), 3), bus, "job-1", "device-1")
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	for range 2 {
		if err := p.Record(context.Background(), "device-1", factEntries(t), at); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Record(context.Background(), "device-1", factEntries(t), at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(bus.published) != 3 || bus.published[0].topic != topology.FactsSubject("job-1") {
		t.Fatalf("published %+v", bus.published)
	}
	var batch facts.Batch
	if err := json.Unmarshal(bus.published[0].evt.Data, &batch); err != nil {
		t.Fatal(err)
	}
	if batch.JobID != "job-1" || batch.DeviceID != "device-1" || batch.Attempt != 3 || len(batch.Entries) != 1 || !batch.ObservedAt.Equal(at) {
		t.Errorf("batch = %+v", batch)
	}
	if bus.published[0].evt.ID != bus.published[1].evt.ID || bus.published[1].evt.ID == bus.published[2].evt.ID {
		t.Errorf("keys %q, %q, %q: want a retry to repeat and a new observation to differ",
			bus.published[0].evt.ID, bus.published[1].evt.ID, bus.published[2].evt.ID)
	}
}

func TestFactsPublisher_RefusesWhatItCannotPublish(t *testing.T) {
	bus := &mockBus{}
	p := newFactsPublisher(context.Background(), bus, "job-1", "device-1")
	if err := p.Record(context.Background(), "device-1", nil, time.Now()); err != nil || len(bus.published) != 0 {
		t.Errorf("no facts: %v, published %d", err, len(bus.published))
	}
	if err := p.Record(context.Background(), "device-2", factEntries(t), time.Now()); err == nil || len(bus.published) != 0 {
		t.Errorf("another device's facts: %v, published %d", err, len(bus.published))
	}
	failing := newFactsPublisher(context.Background(), failingBus{}, "job-1", "device-1")
	if err := failing.Record(context.Background(), "device-1", factEntries(t), time.Now()); err == nil {
		t.Error("a failed publish was not reported")
	}
}
