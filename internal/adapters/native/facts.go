// Package native: the Walk tier's fact sink (Phase 41). The Crawl tier
// stores facts in files beside the journal; a Runner has no database, so this
// side publishes one facts.Batch per task that emitted facts onto the job's
// facts subject, for the Controller to verify and store, as the journal
// publisher does for the journal.
package native

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// factsPublisher is an engine.FactRecorder that publishes onto the job's
// facts subject. Built per dispatch, like the journal publisher, so it is
// never shared between the Agent's workers.
type factsPublisher struct {
	bus      event.Bus
	jobID    string
	deviceID string
	attempt  int
	runnerID string
	// fence is the device lease's fencing token, read at each Record as the
	// journal publisher reads it: the run's order on this device (Phase
	// 41b). A shared lease, or one lost, gives no order.
	fence *lock.Fence
}

// newFactsPublisher builds the sink for one dispatch; the attempt and the
// lease's fence come from the delivery's context, as the journal's do.
func newFactsPublisher(ctx context.Context, bus event.Bus, jobID, deviceID, runnerID string) *factsPublisher {
	attempt, _ := journal.AttemptFrom(ctx)
	return &factsPublisher{bus: bus, jobID: jobID, deviceID: deviceID, attempt: attempt, runnerID: runnerID, fence: lock.FenceFrom(ctx)}
}

// Record implements engine.FactRecorder. The device is always the one the
// dispatch names: a Runner's run is scoped to it, so a run naming another
// is refused rather than published under it.
func (p *factsPublisher) Record(ctx context.Context, device string, obs facts.Observation) error {
	if len(obs.Entries) == 0 {
		return nil
	}
	if device != p.deviceID {
		return fmt.Errorf("facts for device %s in a dispatch for device %s", device, p.deviceID)
	}
	by := obs.By
	by.Job, by.Attempt, by.Runner = p.jobID, p.attempt, p.runnerID
	if revision, epoch, ok := p.fence.Token(); ok {
		by.Order = facts.Order{Source: "lease:" + epoch, Token: revision}
	}
	batch := facts.Batch{JobID: p.jobID, DeviceID: p.deviceID, Attempt: p.attempt, ObservedAt: obs.At.UTC(), Entries: obs.Entries, By: by}
	key := p.idempotencyKey(batch)
	wrapped, err := event.WrapPayload(key, facts.EventType, batch)
	if err != nil {
		return fmt.Errorf("failed to wrap a facts batch for job %s: %w", p.jobID, err)
	}
	if err := p.bus.Publish(event.WithIdempotencyKey(ctx, key), topology.FactsSubject(p.jobID), *wrapped); err != nil {
		return fmt.Errorf("failed to publish a facts batch for job %s: %w", p.jobID, err)
	}
	return nil
}

// idempotencyKey names this batch so a retried publish reproduces it: the
// dispatch, the attempt, and a digest of what it holds (each key's hash, and
// when it was observed).
func (p *factsPublisher) idempotencyKey(b facts.Batch) string {
	var parts []string
	for _, e := range b.Entries {
		parts = append(parts, e.Key+"="+e.Hash)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n") + "\n" + b.ObservedAt.Format(time.RFC3339Nano)))
	return fmt.Sprintf("facts:%s:%s:%d:%s", p.jobID, p.deviceID, p.attempt, hex.EncodeToString(sum[:12]))
}
