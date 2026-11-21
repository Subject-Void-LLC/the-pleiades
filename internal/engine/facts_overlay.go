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
// number is a json.Number in both). The overlay does not wait for the store:
// a run whose secret_mask holds its facts until it ends, or whose store
// write failed, still reads what it gathered. Which emitted facts a task
// sees: those of tasks in earlier levels, on the same device. A sibling in
// the same level (a parallel group, or another item of the same loop) runs
// at the same time and is not seen; the order inside a level is not one a
// runbook can rely on, so it is not offered.
//
// A check stores nothing, and its tasks' emissions are predictions, so they
// are not overlaid, with one exception: a built-in method whose manifest
// declares it read-only (Reversibility.ReadOnly) runs its check by reading
// the device, and what it emits then is an observation. Those are overlaid
// for later checked tasks only, never for a real one, and never stored.
package engine

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// errFactWithheld is the answer for a stored value holding a secret this run
// knows: it is not read, and the key reads as unknown.
var errFactWithheld = errors.New("its stored value holds a secret this run knows, so it is not read")

// factKnowledge is one run's facts: each device's snapshot and overlay.
type factKnowledge struct {
	// keys are every key a condition of the run reads, which a snapshot
	// reads together.
	keys []string
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
	real, observed map[string]overlaid
	// realBy and observedBy are the methods that emitted on the device, so
	// a key one of them declares and did not emit reads as absent.
	realBy, observedBy map[string]bool
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
	k := &factKnowledge{devices: map[string]*deviceFacts{}, missed: map[string]map[string]bool{}}
	seen := map[string]bool{}
	for _, read := range FactReadsOf(dag) {
		for _, key := range read.Keys {
			if !seen[key] {
				seen[key] = true
				k.keys = append(k.keys, key)
			}
		}
	}
	return k
}

// device is device's entry, made on first use.
func (k *factKnowledge) device(device string) *deviceFacts {
	k.mu.Lock()
	defer k.mu.Unlock()
	d := k.devices[device]
	if d == nil {
		d = &deviceFacts{real: map[string]overlaid{}, observed: map[string]overlaid{}, realBy: map[string]bool{}, observedBy: map[string]bool{}}
		k.devices[device] = d
	}
	return d
}

// observe overlays what one level's results emitted, and notes each node a
// check did not run on. It runs at the level's end, on the walk's goroutine,
// before the level's facts are stored or held.
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
		keys, into := nr.factKeys, false
		if len(nr.observedKeys) > 0 {
			keys, into = nr.observedKeys, true
		}
		if len(keys) == 0 || nr.Err != nil || nr.Skipped || nr.Device == "" {
			continue
		}
		observed := make(map[string]any, len(keys))
		for _, key := range keys {
			if v, ok := nr.Stats[key]; ok {
				observed[key] = v
			}
		}
		// The guard the store's write uses (storeFacts), so what is overlaid
		// is what would be stored.
		guard := facts.Guard{Secrets: r.secrets.Snapshot(), Shaped: redact.SecretShaped, SecretName: redact.SecretName, Declared: declaredFacts(nr.factMethod), AllowSealed: r.x.sealedDigests}
		entries, _, err := facts.Prepare(observed, guard)
		if err != nil || len(entries) == 0 {
			continue
		}
		at := nr.FinishedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		d := k.device(nr.Device)
		values, by := d.real, d.realBy
		if into {
			values, by = d.observed, d.observedBy
		}
		for _, e := range entries {
			values[e.Key] = overlaid{value: e.Value, kind: e.Kind, seenAt: at, level: k.level}
		}
		by[nr.factMethod] = true
	}
}

// didNotRun reports whether node's check did not run on device.
func (k *factKnowledge) didNotRun(node, device string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	on := k.missed[node]
	return on[device] || on[""]
}

// answer is what device's key reads as: this run's newest emission of it,
// then the store's answer, as of now. checked adds what read-only checks
// observed. An error says the store could not be read, and only an overlaid
// value answers then. Every value is held to the run's guard as it stands
// now, since a value a later secret_mask made secret must no longer be read.
func (k *factKnowledge) answer(ctx context.Context, r *run, device, key string, checked bool) (FactAnswer, error) {
	d := k.device(device)
	d.once.Do(func() { d.stored, d.storeErr = r.readStored(ctx, device, k.keys) })
	guard := facts.Guard{Secrets: r.secrets.Snapshot(), Shaped: redact.SecretShaped}
	withheld := false
	if o, ok := d.newest(key, checked); ok {
		if facts.Readable(o.value, guard) {
			return FactAnswer{State: FactPresent, Value: o.value, SeenAt: o.seenAt, Kind: o.kind}, nil
		}
		// What the store answers instead is what it would hold: the run
		// stores no value its guard refuses.
		withheld = true
	}
	if d.storeErr != nil {
		return FactAnswer{}, d.storeErr
	}
	stored := d.stored[key]
	switch {
	case stored.State == FactPresent && facts.Readable(stored.Value, guard):
		return stored, nil
	case stored.State == FactPresent:
		return FactAnswer{}, errFactWithheld
	case stored.State == FactAbsent:
		return stored, nil
	case withheld:
		return FactAnswer{}, errFactWithheld
	case d.emittedBy(key, checked):
		return FactAnswer{State: FactAbsent}, nil
	}
	return FactAnswer{}, nil
}

// newest is the latest emission of key: a real one, or for a checked
// reader an observed one when it came later.
func (d *deviceFacts) newest(key string, checked bool) (overlaid, bool) {
	real, ok := d.real[key]
	if !checked {
		return real, ok
	}
	if seen, have := d.observed[key]; have && (!ok || seen.level > real.level) {
		return seen, true
	}
	return real, ok
}

// emittedBy reports whether a method that emitted facts on the device this
// run declares key, so its not emitting key means the device lacks it.
func (d *deviceFacts) emittedBy(key string, checked bool) bool {
	for method := range d.realBy {
		if methodDeclaresFact(method, key) {
			return true
		}
	}
	if checked {
		for method := range d.observedBy {
			if methodDeclaresFact(method, key) {
				return true
			}
		}
	}
	return false
}

// readStored is device's stored answer for keys, through the run's reader.
func (r *run) readStored(ctx context.Context, device string, keys []string) (map[string]FactAnswer, error) {
	if r.x.factReader == nil {
		return nil, ErrNoFactReader
	}
	answers, err := r.x.factReader.ReadFacts(ctx, device, keys)
	if err != nil {
		return nil, err
	}
	if answers == nil {
		answers = map[string]FactAnswer{}
	}
	return answers, nil
}
