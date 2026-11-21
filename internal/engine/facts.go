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
// A method declares each fact it emits (collection.Manifest.Facts): a key it
// did not declare is named in the log and not stored, and a volatile one
// keeps only its latest value. Each stored value says which run, node and
// method observed it, and the Collection that method came from (Phase
// 136b), and the recorder adds the run's order.
//
// Storing is the journal's kind of write: a failure is logged and counted and
// never changes the run's outcome, since a fact is a record of the run, not
// part of it. A value holding one of the run's secrets is never stored
// (facts.Prepare), and one past the bounds is left out and named in the log.
package engine

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// FactRecorder stores the facts a task observed on a device. It must be
// safe for concurrent use.
type FactRecorder interface {
	// Record stores device's prepared facts. The engine fills the
	// observation's run, node and method; a recorder adds what only its
	// tier knows (the run order, the job, the attempt, the Runner).
	Record(ctx context.Context, device string, obs facts.Observation) error
}

// FactOrderer is a FactRecorder that keeps an order of its own between
// observations: the Crawl CLI's project run counter. The engine asks it for
// the order when a task that emitted facts finishes, since its facts are
// stored only when its level ends, which can be much later and in another
// order than the observations were made (two runs at once on one device). A
// recorder without one (the Walk tier's, whose order is the dispatch's lease)
// stamps its own at Record.
type FactOrderer interface {
	ObservationOrder() (facts.Order, error)
}

// observationOrder is the recorder's order for an observation now, or the
// unknown order when it keeps none or cannot say; a value of unknown order is
// placed by the clock.
func (r *run) observationOrder(method string) facts.Order {
	orderer, ok := r.x.facts.(FactOrderer)
	if !ok {
		return facts.Order{}
	}
	order, err := orderer.ObservationOrder()
	if err != nil {
		slog.Warn("a task's facts get no run order; the clock will place them", "method", method, "error", err)
		return facts.Order{}
	}
	return order
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

// recordFacts takes what the level's real tasks emitted. A run whose runbook
// masks no value stores each task's facts as it goes. A run whose runbook has
// any secret_mask holds them instead, until flushFacts can guard them by value
// (owner decision 2026-11-04, Phase 41b): a value only a masking task makes
// secret is not yet a known secret when the task that observed it finishes.
func (r *run) recordFacts(ctx context.Context, results []NodeResult) {
	r.noteRan(results)
	// What the level emitted is the run's to read from the next level on,
	// whether it is stored now, held for a secret_mask, or fails to store,
	// and whatever recorder there is (facts_overlay.go).
	if r.knowledge != nil {
		r.knowledge.observe(r, results)
	}
	if r.x.facts == nil {
		return
	}
	if len(maskTasks(r.dag)) > 0 {
		r.heldFacts = append(r.heldFacts, results...)
		return
	}
	r.storeFacts(ctx, results)
}

// noteRan records which nodes ran without error, so flushFacts can tell
// whether every masking task did.
func (r *run) noteRan(results []NodeResult) {
	for _, nr := range results {
		if nr.Err == nil && !nr.Skipped {
			if r.ranOK == nil {
				r.ranOK = map[string]bool{}
			}
			r.ranOK[nr.NodeID] = true
		}
	}
}

// flushFacts stores the held facts when every masking task ran and succeeded,
// now that their values are known secrets, and discards them otherwise: a run
// that ends before a mask runs (cancelled, failed, or filtered by --tags) has
// not made the value secret, so none of its facts may be stored.
func (r *run) flushFacts(ctx context.Context) {
	if len(r.heldFacts) == 0 {
		return
	}
	held := r.heldFacts
	r.heldFacts = nil
	if r.x.facts == nil {
		return
	}
	var notRun []string
	for _, id := range maskTasks(r.dag) {
		if !r.ranOK[id] {
			notRun = append(notRun, id)
		}
	}
	if len(notRun) > 0 {
		slog.Warn("facts held for a secret_mask were not stored: the run ended before every masking task ran successfully", "held_results", len(held), "masking_tasks_not_run", notRun)
		return
	}
	r.storeFacts(ctx, held)
}

// storeFacts stores what the results' tasks emitted, one Record per task and
// device. Every value is guarded by value against the run's known secrets,
// which by the time a held run flushes include every value a secret_mask made
// secret, whatever key it was stored under.
func (r *run) storeFacts(ctx context.Context, results []NodeResult) {
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
		guard := facts.Guard{Secrets: r.secrets.Snapshot(), Shaped: redact.SecretShaped, SecretName: redact.SecretName, Declared: declaredFacts(nr.factMethod), AllowSealed: r.x.sealedDigests}
		entries, problems, err := facts.Prepare(observed, guard)
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
		by := r.factProvenance(nr)
		// Each run of a looped or retried node is its own observation, told
		// apart by its iteration where two could finish at one instant.
		obs := facts.Observation{Entries: entries, At: at, By: by, Iteration: nr.Iteration}
		if err := r.x.facts.Record(wctx, nr.Device, obs); err != nil {
			r.factFailures++
			slog.Error("storing a task's facts failed; the run goes on", "node", nr.NodeID, "device", nr.Device, "error", err, "failures_this_run", r.factFailures)
		}
		cancel()
	}
}

// factProvenance is the provenance the engine stamps on nr's facts: the run,
// node and method, the order taken when the task finished, and the identity
// the journal records for the same node (Phase 136b): the core's, from what
// it linked or its loader hashed, never one the method sent. It runs on the
// walk's goroutine after recordLevel, as the identity cache requires.
func (r *run) factProvenance(nr NodeResult) facts.Provenance {
	by := facts.Provenance{Run: r.runID, Node: nr.NodeID, Method: nr.factMethod, Order: nr.factOrder,
		Collection: r.collectionIdentity(resolveFQCN(nr.factMethod), nr.Provider)}
	if err := by.CheckCollection(); err != nil {
		// Never expected: a built-in's identity is its own group's, and
		// collection.Register keeps a program's methods out of every group.
		// A store refuses the observation whole over it, so the facts are
		// kept and the identity is not.
		slog.Warn("a task's facts are stored with no Collection identity", "node", nr.NodeID, "device", nr.Device, "reason", err)
		by.Collection = wire.CollectionIdentity{}
	}
	return by
}

// declaredFacts is what method's manifest declares about each fact it may
// emit (Phase 41b): a key it does not declare, or a value of another type,
// is not stored. A method the registry does not hold declares nothing, so
// none of its facts is stored.
func declaredFacts(method string) func(key string) (facts.Spec, bool) {
	d, _ := collection.Lookup(method)
	return func(key string) (facts.Spec, bool) {
		spec, ok := collection.FactFor(d.Manifest.Facts, key)
		return facts.SpecOf(string(spec.Kind), string(spec.Type), spec.Key, spec.NamesItems()), ok
	}
}

// maskTasks is the IDs of every task in the runbook that is a secret_mask, the
// whole runbook rather than a --tags selection: a masking task a selection
// drops still makes its value secret, so it is one the run must see run.
func maskTasks(d *DAG) []string {
	if d == nil {
		return nil
	}
	tasks := d.Nodes
	if d.full != nil {
		tasks = d.full.nodes
	}
	var ids []string
	for id, task := range tasks {
		if task != nil && task.SecretMask != nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}
