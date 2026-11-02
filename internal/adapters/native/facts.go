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
}

// newFactsPublisher builds the sink for one dispatch; the attempt comes from
// the delivery, as the journal's does.
func newFactsPublisher(ctx context.Context, bus event.Bus, jobID, deviceID string) *factsPublisher {
	attempt, _ := journal.AttemptFrom(ctx)
	return &factsPublisher{bus: bus, jobID: jobID, deviceID: deviceID, attempt: attempt}
}

// Record implements engine.FactRecorder. The device is always the one the
// dispatch names: a Runner's run is scoped to it, so a run naming another
// is refused rather than published under it.
func (p *factsPublisher) Record(ctx context.Context, device string, entries []facts.Entry, at time.Time) error {
	if len(entries) == 0 {
		return nil
	}
	if device != p.deviceID {
		return fmt.Errorf("facts for device %s in a dispatch for device %s", device, p.deviceID)
	}
	batch := facts.Batch{JobID: p.jobID, DeviceID: p.deviceID, Attempt: p.attempt, ObservedAt: at.UTC(), Entries: entries}
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
