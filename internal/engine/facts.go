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
// method observed it, and the recorder adds the run's order.
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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

// recordFacts stores what the level's real tasks emitted, one Record per
// task and device.
func (r *run) recordFacts(ctx context.Context, results []NodeResult) {
	if r.x.facts == nil {
		return
	}
	masked := maskedLater(r.dag)
	for _, nr := range results {
		if len(nr.factKeys) == 0 || nr.Err != nil || nr.Skipped || nr.Device == "" {
			continue
		}
		observed := make(map[string]any, len(nr.factKeys))
		for _, key := range nr.factKeys {
			if masked[nr.factRegister][key] {
				// A secret_mask runs on a later task and reaches back to this
				// one's register, so the value is not in the run's secrets
				// yet; the runbook says now that it will be.
				slog.Warn("a fact was not stored", "node", nr.NodeID, "device", nr.Device, "fact", key, "reason", "a secret_mask in this runbook marks it secret")
				continue
			}
			if v, ok := nr.Stats[key]; ok {
				observed[key] = v
			}
		}
		guard := facts.Guard{Secrets: r.secrets.Snapshot(), Shaped: redact.SecretShaped, SecretName: redact.SecretName, Declared: declaredFacts(nr.factMethod)}
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
		obs := facts.Observation{Entries: entries, At: at, By: facts.Provenance{Run: r.runID, Node: nr.NodeID, Method: nr.factMethod, Order: nr.factOrder}}
		if err := r.x.facts.Record(wctx, nr.Device, obs); err != nil {
			r.factFailures++
			slog.Error("storing a task's facts failed; the run goes on", "node", nr.NodeID, "device", nr.Device, "error", err, "failures_this_run", r.factFailures)
		}
		cancel()
	}
}

// declaredFacts is what method's manifest declares about each fact it may
// emit (Phase 41b): a key it does not declare, or a value of another type,
// is not stored. A method the registry does not hold declares nothing, so
// none of its facts is stored.
func declaredFacts(method string) func(key string) (facts.Spec, bool) {
	d, _ := collection.Lookup(method)
	return func(key string) (facts.Spec, bool) {
		spec, ok := collection.FactFor(d.Manifest.Facts, key)
		return facts.SpecOf(string(spec.Kind), string(spec.Type), spec.Key, spec.Prefix), ok
	}
}

// maskedLater is, for each register, the fields some task's secret_mask in
// the runbook marks secret. A secret_mask runs on its own, later task, after
// the facts of the task whose register it names were stored, so the run's
// known secrets cannot catch them; the runbook says ahead which fields it
// will mask, and none of them is stored. It reads every task the runbook
// has, not only those a --tags selection kept: a run that keeps the task
// registering a value and drops the one masking it has still been told the
// value is secret.
func maskedLater(d *DAG) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if d == nil {
		return out
	}
	tasks := d.Nodes
	if d.full != nil {
		tasks = d.full.nodes
	}
	for _, task := range tasks {
		if task == nil || task.SecretMask == nil || task.SecretMask.Register == "" {
			continue
		}
		if out[task.SecretMask.Register] == nil {
			out[task.SecretMask.Register] = map[string]bool{}
		}
		for _, f := range task.SecretMask.Fields {
			out[task.SecretMask.Register][f] = true
		}
	}
	return out
}
