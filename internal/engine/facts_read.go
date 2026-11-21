// Package engine: the read side of Phase 41's facts. A run reads a device's
// stored facts through FactReader, set beside FactRecorder, so a condition's
// facts root (facts_root.go) never touches a store directly.
//
// The port asks for named keys of one device, which is what a condition
// needs and what a Runner could later be served over the wire (only the keys
// its runbook names, never a device's whole store). Each key is answered
// present, absent or unknown, and a device whose store cannot be read at all
// is an error, never an empty answer. A fact's history is not the engine's to
// read: the CLI's own facts commands read it from the store itself.
package engine

import (
	"context"
	"errors"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// FactState is what a store can say of one key of one device.
type FactState int

const (
	// FactUnknown says the store cannot answer: no method that emits the key
	// has observed the device (it has never been gathered for it), or what
	// the store kept cannot say. It is the zero value, so a key a reader
	// leaves out of its answer is unknown, never absent.
	FactUnknown FactState = iota
	// FactAbsent says a method that emits the key has observed the device
	// and the store holds no value for it.
	FactAbsent
	// FactPresent says the store holds a value.
	FactPresent
)

// FactAnswer is a store's answer for one key of one device.
type FactAnswer struct {
	State FactState
	// Value is the value as the store returns it, the form facts.Prepare
	// leaves it in (a number is a json.Number). Set only when present.
	Value any
	// SeenAt is when the value was last confirmed, by the reader's clock; a
	// reader that is served an age rather than a time (a Runner, later)
	// sets it to its own now less that age.
	SeenAt time.Time
	// Kind is what the emitting method declared: stable or volatile.
	Kind facts.Kind
}

// FactReader reads named keys of one device's stored facts. Each tier's
// store is behind it. It must be safe for concurrent use.
type FactReader interface {
	// ReadFacts answers each of keys for device. A key the answer leaves
	// out is unknown. An error says the device's stored facts cannot be
	// read at all (the store is there and will not open, or its key is
	// gone), which a reader of any of its keys must treat as unknown.
	ReadFacts(ctx context.Context, device string, keys []string) (map[string]FactAnswer, error)
}

// WithFactReader gives the run a way to read stored facts. Without it a read
// reports that no store is wired, never an empty answer.
func WithFactReader(fr FactReader) ExecutorOption {
	return func(x *Executor) {
		x.factReader = fr
	}
}

// ErrNoFactReader is returned by a read when the executor was built without
// WithFactReader: an unwired store must not look like a device with no facts.
var ErrNoFactReader = errors.New("engine: no fact reader is wired, so stored facts cannot be read")

// StoredFact is one fact as a run reads it: its value and how old it is, which
// every read carries (Phase 41e's age rule). Age is measured from when the
// value was last confirmed, not when it changed.
type StoredFact struct {
	Key   string
	Value any
	Age   time.Duration
	// Known says the store holds a value (State is FactPresent).
	Known bool
	// State tells an absent key from an unknown one.
	State FactState
}

// ReadFact returns device's current stored value for key, with its age at now.
// A key the device has no value for is Known false, never an error: State
// says whether it is absent or unknown.
func (x *Executor) ReadFact(ctx context.Context, device, key string, now time.Time) (StoredFact, error) {
	if x.factReader == nil {
		return StoredFact{}, ErrNoFactReader
	}
	answers, err := x.factReader.ReadFacts(ctx, device, []string{key})
	if err != nil {
		return StoredFact{}, err
	}
	a := answers[key]
	if a.State != FactPresent {
		return StoredFact{Key: key, State: a.State}, nil
	}
	return StoredFact{Key: key, Value: a.Value, Age: now.Sub(a.SeenAt), Known: true, State: FactPresent}, nil
}

// AnswerFacts is a store's answer for keys of one device, from what it holds
// of them (current: each key it holds, at its current value) and the methods
// whose observations of the device it keeps (observers). A key current holds
// is present. One it does not hold is absent when some observer's manifest
// declares it (that method looked at the device and stored no value for it)
// and unknown otherwise: a device only a package gatherer has observed has
// not been asked its operating system. A composition root's reader builds
// its answer here, so every tier draws the line in one place.
//
// What an observation looked for is the method's declaration, not its call:
// a gather narrowed by a filter still counts as looking for every key its
// method declares.
func AnswerFacts(keys []string, current map[string]facts.Entry, observers []string, now time.Time) map[string]FactAnswer {
	out := make(map[string]FactAnswer, len(keys))
	for _, key := range keys {
		if e, ok := current[key]; ok {
			seen := e.LastSeenAt
			if seen.IsZero() {
				seen = now
			}
			out[key] = FactAnswer{State: FactPresent, Value: e.Value, SeenAt: seen, Kind: e.Kind}
			continue
		}
		for _, method := range observers {
			if methodDeclaresFact(method, key) {
				out[key] = FactAnswer{State: FactAbsent}
				break
			}
		}
	}
	return out
}

// methodDeclaresFact reports whether method declares key among the facts it
// may emit, by key or by family (collection.FactFor), reading the registry or,
// on a process that registers no Collection, the module catalog.
func methodDeclaresFact(method, key string) bool {
	m, ok := collection.ManifestOf(method)
	if !ok {
		return false
	}
	_, declared := collection.FactFor(m.Facts, key)
	return declared
}
