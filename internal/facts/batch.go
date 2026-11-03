// Package facts: what a Walk-tier Runner publishes for the Controller to
// store (Phase 41), as internal/journal's Batch is for the journal.
package facts

import "time"

// EventType names a published Batch.
const EventType = "job.facts"

// Batch is one task's facts on one device of one dispatch.
type Batch struct {
	JobID      string    `json:"job_id"`
	DeviceID   string    `json:"device_id"`
	Attempt    int       `json:"attempt"`
	ObservedAt time.Time `json:"observed_at"`
	Entries    []Entry   `json:"entries"`
	// By is the run, task and Runner that observed the entries, and the
	// run's order (Phase 41b). The Controller holds its job and attempt to
	// the batch's own.
	By Provenance `json:"by,omitzero"`
}

// Verify re-checks entries a Runner published, which the Controller treats
// as untrusted input: each must pass Prepare's rules again under guard (a
// valid key, a bounded value, nothing shaped like a secret) and carry the
// hash Prepare computes for its value, so a published hash cannot vouch for
// a value it does not describe. An entry's kind is its own unless guard
// declares the method's, when the two must agree; a kind that is neither
// stable nor volatile is refused. It returns the entries that pass,
// recomputed, and how many did not.
func Verify(entries []Entry, guard Guard) ([]Entry, int, error) {
	observed := make(map[string]any, len(entries))
	claimed := make(map[string]string, len(entries))
	refused := 0
	kinds := make(map[string]Kind, len(entries))
	for _, e := range entries {
		if _, dup := observed[e.Key]; dup {
			refused++
			continue
		}
		observed[e.Key] = e.Value
		claimed[e.Key] = e.Hash
		kinds[e.Key] = e.Kind
	}
	// Without the method's declaration, each entry keeps the kind the Runner
	// sent; with it, a kind that disagrees is refused below.
	declared := guard.Declared
	guard.Declared = func(key string) (Spec, bool) {
		if declared != nil {
			return declared(key)
		}
		if k := kinds[key]; k.Valid() {
			return Spec{Kind: k}, true
		}
		return Spec{Kind: KindStable}, kinds[key] == ""
	}
	prepared, problems, err := Prepare(observed, guard)
	if err != nil {
		return nil, len(entries), err
	}
	refused += len(problems)
	out := prepared[:0]
	for _, e := range prepared {
		if claimed[e.Key] != e.Hash || kinds[e.Key] != "" && kinds[e.Key] != e.Kind {
			refused++
			continue
		}
		out = append(out, e)
	}
	return out, refused, nil
}
