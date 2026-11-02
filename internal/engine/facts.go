// Package engine: storing what a run's tasks observed as their devices'
// facts (Phase 41, internal/facts).
//
// A method emits a fact with sdk.EmitFact; the engine reads which of a
// result's stats are facts (sdk.StatFactKeys) and, after every node of a
// level has returned, hands each device's to the FactRecorder the composition
// root wired: the Crawl CLI's file store under .pleiades/facts, or on the Walk
// tier a publisher the Controller stores from. Only a real run's facts are
// stored, from a task that succeeded and was not skipped; a check predicts
// nothing about a device's state worth keeping.
//
// Storing is the journal's kind of write: a failure is logged and counted and
// never changes the run's outcome, since a fact is a record of the run, not
// part of it. A value holding one of the run's secrets is never stored
// (facts.Prepare), and one past the bounds is left out and named in the log.
package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// FactRecorder stores the facts a task observed on a device. It must be
// safe for concurrent use.
type FactRecorder interface {
	// Record stores device's prepared facts, observed at.
	Record(ctx context.Context, device string, entries []facts.Entry, at time.Time) error
}

// WithFactRecorder stores real runs' facts through fr. Without it a run
// stores none.
func WithFactRecorder(fr FactRecorder) ExecutorOption {
	return func(x *Executor) {
		x.facts = fr
	}
}

// factRecordTimeout bounds one device's write, detached from the run's own
// cancellation as the journal's is, so a run cut short still records what
// its finished tasks saw.
const factRecordTimeout = 30 * time.Second

// recordFacts stores what the level's real tasks emitted, one Record per
// task and device.
func (r *run) recordFacts(ctx context.Context, results []NodeResult) {
	if r.x.facts == nil {
		return
	}
	for _, nr := range results {
		if len(nr.factKeys) == 0 || nr.Err != nil || nr.Skipped || nr.Device == "" {
			continue
		}
		observed := make(map[string]any, len(nr.factKeys))
		for _, key := range nr.factKeys {
			if v, ok := nr.Stats[key]; ok {
				observed[key] = v
			}
		}
		entries, problems, err := facts.Prepare(observed, facts.Guard{Secrets: r.secrets.Snapshot(), Shaped: redact.SecretShaped, SecretName: redact.SecretName})
		for _, p := range problems {
			slog.Warn("a fact was not stored", "node", nr.NodeID, "device", nr.Device, "fact", p.Key, "reason", p.Reason)
		}
		if err != nil {
			slog.Warn("a task's facts were not stored", "node", nr.NodeID, "device", nr.Device, "error", err)
			continue
		}
		if len(entries) == 0 {
			continue
		}
		at := nr.FinishedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), factRecordTimeout)
		if err := r.x.facts.Record(wctx, nr.Device, entries, at); err != nil {
			r.factFailures++
			slog.Error("storing a task's facts failed; the run goes on", "node", nr.NodeID, "device", nr.Device, "error", err, "failures_this_run", r.factFailures)
		}
		cancel()
	}
}
