// Package native: what the Walk tier's fact sink costs over a real broker
// (Phase 41's fact write throughput): a large host's gather, split into
// broker-sized batches and published durably to JetStream, as a Runner does.
package native

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// benchGatherKeys is a large host's gather: a package family this big.
const benchGatherKeys = 2000

// BenchmarkFactsPublisher_LargeGatherOverJetStream publishes one gather of
// benchGatherKeys facts per iteration through the Runner's publisher to a real
// NATS JetStream, each iteration a new observation, so no batch is a duplicate
// the stream's dedup window would drop.
func BenchmarkFactsPublisher_LargeGatherOverJetStream(b *testing.B) {
	ctx := context.Background()
	bus, err := event.NewNatsBus(ctx, testsupport.StartNATS(b).URL(), nil, topology.StreamProvisioner, topology.DefaultOutageBudget, false)
	if err != nil {
		b.Fatalf("NewNatsBus: %v", err)
	}
	observed := make(map[string]any, benchGatherKeys)
	for i := 0; i < benchGatherKeys; i++ {
		observed[fmt.Sprintf("package.p%04d", i)] = map[string]any{"version": "1.0", "arch": "amd64", "source": "dpkg"}
	}
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
	if err != nil || len(problems) != 0 {
		b.Fatalf("Prepare: %v, %v", problems, err)
	}
	dispatch := lock.WithFence(journal.WithAttempt(ctx, 1), lock.NewFence(1, "bench"))
	_, sealKey := factSealPair(b)
	p := newFactsPublisher(dispatch, bus, nil, "job-bench", "device-bench", "runner-bench", sealKey)
	t0 := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Record(ctx, "device-bench", facts.Observation{Entries: entries, At: t0.Add(time.Duration(i) * time.Second)}); err != nil {
			b.Fatalf("Record: %v", err)
		}
	}
	b.StopTimer()
	perGather := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.ReportMetric(perGather/benchGatherKeys, "ns/fact")
}
