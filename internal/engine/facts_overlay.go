// Package engine: what one run knows of its devices' facts, for the facts
// root (facts_root.go): a snapshot of the store, read once per device, under
// the facts this run's own tasks have emitted since it began.
//
// A device's stored values are read at the first condition that touches the
// facts root on it, for every key any condition of the run names, through the
// FactReader the composition root wired (one read per device per run, never
// one per task). Over that snapshot lies the overlay: what this run's tasks
// emitted on the device, taken at each level's end, after facts.Prepare and
// its guard, so a value there is the value the store would hand back (a
// number is a json.Number in both). Only the keys some condition of the run
// names are kept, so a gather of thousands of packages holds none of them
// unless a condition reads one. The overlay does not wait for the store: a
// run whose secret_mask holds its facts until it ends, or whose store write
// failed, still reads what it gathered. Which emitted facts a task sees: those
// of tasks in earlier levels, on the same device. A sibling in the same level
// (a parallel group, or another item of the same loop) runs at the same time
// and is not seen; the order inside a level is not one a runbook can rely on,
// so it is not offered.
//
// The overlay is also the one place a key can read absent: this run made the
// call, so it knows what the call was asked (facts_answer.go).
//
// A check stores nothing, and its tasks' emissions are predictions, so they
// are not overlaid, with one exception: a built-in method whose manifest
// declares it read-only and whose check is its own run (facts_check.go)
// checks by reading the device, and what it emits then is an observation.
// Those are overlaid for later checked tasks only, never for a real one, and
// never stored.
package engine

import (
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// factKnowledge is one run's facts: each device's snapshot and overlay.
type factKnowledge struct {
	// keys are every key a condition of the run reads, which a snapshot
	// reads together and the overlay keeps; read is the same as a set.
	keys []string
	read map[string]bool
	// level counts the level ends that have overlaid facts, so a later
	// emission wins over an earlier one.
	level int

	mu      sync.Mutex
	devices map[string]*deviceFacts
	// missed is, per node, the devices (or "" for every device) its check
	// did not run on: an observing method there observed nothing, so a
	// checked task reading what it would observe cannot be checked.
	missed map[string]map[string]bool
}

// deviceFacts is one device's snapshot and overlay.
type deviceFacts struct {
	once     sync.Once
	stored   map[string]FactAnswer
	storeErr error
	// real is what this run's real tasks emitted; observed what its
	// read-only checks observed. Each is written only at a level's end.
	real, observed emitted
}

// emitted is what one kind of task (real, or checked by reading) emitted on
// a device this run, of the keys the run's conditions read.
type emitted struct {
	// values are the kept values, by key.
	values map[string]overlaid
	// askedBy are the methods that emitted on the device from a call with
	// nothing narrowing it, so a key one of them declares by name and did
	// not emit reads as absent (lookedFor).
	askedBy map[string]bool
	// withheld are the keys a call emitted that the facts guard refused
	// (a secret, a wrong type, a value too large): read as unknown, never as
	// absent, since the device does have them.
	withheld map[string]bool
}

// overlaid is one emitted value.
type overlaid struct {
	value  any
	kind   facts.Kind
	seenAt time.Time
	level  int
}

// newFactKnowledge is the knowledge of a run of dag.
func newFactKnowledge(dag *DAG) *factKnowledge {
	k := &factKnowledge{read: map[string]bool{}, devices: map[string]*deviceFacts{}, missed: map[string]map[string]bool{}}
	for _, read := range FactReadsOf(dag) {
		for _, key := range read.Keys {
			if !k.read[key] {
				k.read[key] = true
				k.keys = append(k.keys, key)
			}
		}
	}
	return k
}

// newEmitted is an empty record of emissions.
func newEmitted() emitted {
	return emitted{values: map[string]overlaid{}, askedBy: map[string]bool{}, withheld: map[string]bool{}}
}

// device is device's entry, made on first use.
func (k *factKnowledge) device(device string) *deviceFacts {
	k.mu.Lock()
	defer k.mu.Unlock()
	d := k.devices[device]
	if d == nil {
		d = &deviceFacts{real: newEmitted(), observed: newEmitted()}
		k.devices[device] = d
	}
	return d
}

// observe overlays what one level's results emitted, of the keys the run's
// conditions read, and notes each node a check did not run on. It runs at the
// level's end, on the walk's goroutine, before the level's facts are stored
// or held. A run whose conditions read no fact keeps nothing.
func (k *factKnowledge) observe(r *run, results []NodeResult) {
	k.level++
	for _, nr := range results {
		if nr.Unchecked || nr.Conditional {
			k.mu.Lock()
			if k.missed[nr.NodeID] == nil {
				k.missed[nr.NodeID] = map[string]bool{}
			}
			k.missed[nr.NodeID][nr.Device] = true
			k.mu.Unlock()
		}
		keys, observedInCheck := nr.factKeys, false
		if len(nr.observedKeys) > 0 {
			keys, observedInCheck = nr.observedKeys, true
		}
		if len(k.keys) == 0 || len(keys) == 0 || nr.Err != nil || nr.Skipped || nr.Device == "" {
			continue
		}
		observed := map[string]any{}
		for _, key := range keys {
			if v, ok := nr.Stats[key]; ok && k.read[key] {
				observed[key] = v
			}
		}
		// The guard the store's write uses (storeFacts), so what is overlaid
		// is what would be stored.
		guard := facts.Guard{Secrets: r.secrets.Snapshot(), Shaped: redact.SecretShaped, SecretName: redact.SecretName, Declared: declaredFacts(nr.factMethod), AllowSealed: r.x.sealedDigests}
		entries, problems, err := facts.Prepare(observed, guard)
		if err != nil {
			continue
		}
		at := nr.FinishedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		d := k.device(nr.Device)
		into := &d.real
		if observedInCheck {
			into = &d.observed
		}
		for _, e := range entries {
			into.values[e.Key] = overlaid{value: e.Value, kind: e.Kind, seenAt: at, level: k.level}
			delete(into.withheld, e.Key)
		}
		for _, p := range problems {
			into.withheld[p.Key] = true
		}
		if !callNarrowed(r.dag.Nodes[nr.NodeID], nr.factMethod) {
			into.askedBy[nr.factMethod] = true
		}
	}
}

// callNarrowed reports whether task's call of method may have been asked for
// less than every fact method declares: it passes any parameter of its own (a
// filter, a subset, a list of paths or names), or method takes a parameter a
// run setting can fill unseen. The test reads no method's meaning, so it is
// broad: a parameter that narrows nothing (insecure_skip_host_key_verify)
// counts too, and the key then reads unknown, never absent. A result naming
// no task of the DAG (an undo step) is narrowed.
func callNarrowed(task *Task, method string) bool {
	if task == nil {
		return true
	}
	for name := range task.Params {
		if !collection.IsReservedParam(name) {
			return true
		}
	}
	m, ok := collection.ManifestOf(method)
	if !ok {
		return true
	}
	for _, p := range m.Doc.Params {
		if p.RunSetting {
			return true
		}
	}
	return false
}

// didNotRun reports whether node's check did not run on device.
func (k *factKnowledge) didNotRun(node, device string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	on := k.missed[node]
	return on[device] || on[""]
}
