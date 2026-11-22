// Package engine: one key of one device, as a condition of this run reads it
// (facts_overlay.go holds what it is read from).
//
// Present comes from this run's newest emission of the key, else from the
// store. Absent comes only from this run: a call, with nothing narrowing it,
// of a built-in that declares the key by name and claims that not emitting
// it proves the device lacks it (collection.FactSpec.AbsentWhenNotEmitted),
// emitted on the device and did not emit the key. No built-in claims it yet:
// facts.gather reads with commands whose failure it cannot tell from an empty
// answer (a root-only file read as `cat ... 2>/dev/null; true`). Everything
// else is unknown: a key a method declares without the claim, a family's item
// nobody listed,
// a key a narrowed call did not emit, a key only the store could speak for
// (it keeps what was emitted, not what was looked for), a key the facts guard
// withheld. Unknown fails the reading task (facts_view.go).
package engine

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
)

// errFactWithheld is the answer for a value the run's guard refuses: one
// holding a secret this run knows, or one the facts guard refused when it was
// observed (a secret, a wrong type, too large). It is not read, and the key
// reads as unknown, never absent, since the device does have it.
var errFactWithheld = errors.New("its value is withheld: it holds a secret this run knows, or the facts guard refused it when it was observed, so it is not read")

// answer is what device's key reads as: this run's newest emission of it,
// then the store's answer, as of now. checked adds what read-only checks
// observed. An error says the store could not be read, or the value is
// withheld, and only an overlaid value answers then. Every value is held to
// the run's guard as it stands now, since a value a later secret_mask made
// secret must no longer be read.
func (k *factKnowledge) answer(ctx context.Context, r *run, device, key string, checked bool) (FactAnswer, error) {
	d := k.device(device)
	d.once.Do(func() { d.stored, d.storeErr = r.readStored(ctx, device, k.keys) })
	guard := facts.Guard{Secrets: r.secrets.Snapshot(), Shaped: redact.SecretShaped}
	withheld := d.real.withheld[key] || (checked && d.observed.withheld[key])
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
	case stored.State == FactPresent, withheld:
		return FactAnswer{}, errFactWithheld
	case stored.State == FactAbsent:
		// A reader that keeps what its observations looked for may say so.
		return stored, nil
	case d.lookedFor(key, checked):
		return FactAnswer{State: FactAbsent}, nil
	}
	return FactAnswer{}, nil
}

// newest is the latest emission of key: a real one, or for a checked
// reader an observed one when it came later.
func (d *deviceFacts) newest(key string, checked bool) (overlaid, bool) {
	real, ok := d.real.values[key]
	if !checked {
		return real, ok
	}
	if seen, have := d.observed.values[key]; have && (!ok || seen.level > real.level) {
		return seen, true
	}
	return real, ok
}

// lookedFor reports whether a call this run made on the device, with nothing
// narrowing it, was of a built-in claiming key's absence when it does not
// emit it, so its not emitting key means the device lacks it.
func (d *deviceFacts) lookedFor(key string, checked bool) bool {
	for method := range d.real.askedBy {
		if methodClaimsAbsence(method, key) {
			return true
		}
	}
	if checked {
		for method := range d.observed.askedBy {
			if methodClaimsAbsence(method, key) {
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
