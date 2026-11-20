// Benchmarks of the Controller's fact store on SQLite (Phase 41h): what one
// unchanged gather of a large host costs to store, and what reading the
// device's current facts costs as its gathers accumulate, since a value's
// last confirmation is now read from them rather than kept on it.
package entstore_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// benchKeys is a large host's gather: a package family of this many keys.
const benchKeys = 2000

// benchGather is one gather of benchKeys unchanged values by run n.
func benchGather(b *testing.B, n int) facts.Observation {
	b.Helper()
	observed := make(map[string]any, benchKeys)
	for i := 0; i < benchKeys; i++ {
		observed[fmt.Sprintf("package.p%04d", i)] = map[string]any{"version": "1.0", "arch": "amd64"}
	}
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
	if err != nil || len(problems) != 0 {
		b.Fatalf("Prepare: %v, %v", problems, err)
	}
	t0 := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	return facts.Observation{Entries: entries, At: t0.Add(time.Duration(n) * time.Hour), By: facts.Provenance{Run: fmt.Sprintf("r%d", n), Order: facts.Order{Source: facts.ClockSource, Token: uint64(n)}}}
}

// BenchmarkStore_UnchangedGather stores a gather of benchKeys values that
// match what is stored: one observation, and before 41h one UPDATE per key.
func BenchmarkStore_UnchangedGather(b *testing.B) {
	client, device := open(b)
	store := entstore.New(client)
	ctx := context.Background()
	if err := store.Record(ctx, device, benchGather(b, 1)); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.Record(ctx, device, benchGather(b, i+2)); err != nil {
			b.Fatal(err)
		}
	}
}

// changedGather is benchGather with every value new in run n, so each gather
// writes benchKeys rows and extends the chain by as many.
func changedGather(b *testing.B, n int) facts.Observation {
	b.Helper()
	observed := make(map[string]any, benchKeys)
	for i := 0; i < benchKeys; i++ {
		observed[fmt.Sprintf("package.p%04d", i)] = map[string]any{"version": fmt.Sprintf("1.%d", n), "arch": "amd64"}
	}
	obs := benchGather(b, n)
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
	if err != nil || len(problems) != 0 {
		b.Fatalf("Prepare: %v, %v", problems, err)
	}
	obs.Entries = entries
	return obs
}

// BenchmarkStore_ChangedGather stores a gather whose every value changed: a
// row per key, each chained to the one before.
func BenchmarkStore_ChangedGather(b *testing.B) {
	client, device := open(b)
	store := entstore.New(client)
	ctx := context.Background()
	gathers := make([]facts.Observation, b.N)
	for i := range gathers {
		gathers[i] = changedGather(b, i+1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := store.Record(ctx, device, gathers[i]); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/benchKeys, "ns/fact")
}

// BenchmarkSubscriber_Handle is the Controller's whole path for one published
// batch of benchKeys changed values: decoding it, re-checking every entry
// (facts.Verify) and storing it, as the durable consumer does.
func BenchmarkSubscriber_Handle(b *testing.B) {
	client, device := open(b)
	sub := entstore.NewSubscriber(entstore.New(client), nil, nil)
	events := make([]event.Event, b.N)
	for i := range events {
		obs := changedGather(b, i+1)
		batch := facts.Batch{JobID: "job-bench", DeviceID: device, Attempt: 1, ObservedAt: obs.At, Entries: obs.Entries, By: facts.Provenance{Job: "job-bench", Runner: "runner-bench", Attempt: 1, Order: facts.Order{Source: "lease:bench", Token: uint64(i + 1)}}}
		data, err := json.Marshal(batch)
		if err != nil {
			b.Fatal(err)
		}
		events[i] = event.Event{ID: fmt.Sprintf("e%d", i), Type: facts.EventType, Data: data, Subject: topology.FactsSubject("runner-bench", "job-bench")}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sub.Handle(events[i]); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/benchKeys, "ns/fact")
}

// BenchmarkStore_Current reads the current facts of a device with benchKeys
// values and the given number of gathers stored.
func BenchmarkStore_Current(b *testing.B) {
	for _, gathers := range []int{1, 24, 168} {
		b.Run(fmt.Sprintf("gathers=%d", gathers), func(b *testing.B) {
			client, device := open(b)
			store := entstore.New(client)
			ctx := context.Background()
			for n := 1; n <= gathers; n++ {
				if err := store.Record(ctx, device, benchGather(b, n)); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if current, err := store.Current(ctx, device); err != nil || len(current) != benchKeys {
					b.Fatalf("Current: %d, %v", len(current), err)
				}
			}
		})
	}
}
