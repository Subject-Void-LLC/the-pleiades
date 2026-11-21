// Package facts: what a Walk-tier Runner publishes for the Controller to
// store (Phase 41), as internal/journal's Batch is for the journal.
package facts

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// EventType names a published Batch.
const EventType = "job.facts"

// Batch is one task's facts on one device of one dispatch.
type Batch struct {
	JobID      string    `json:"job_id"`
	DeviceID   string    `json:"device_id"`
	Attempt    int       `json:"attempt"`
	ObservedAt time.Time `json:"observed_at"`
	// Entries are the facts, in plaintext: what a Runner of an earlier
	// release published, and what the Controller holds once it has opened
	// Sealed. A Runner of this release publishes none here.
	Entries []Entry `json:"entries,omitempty"`
	// Sealed is the entries sealed to the Controller's public key (the
	// dispatch's fact seal key), as SealPlaintextOf made them: the stream
	// between a Runner and the Controller then holds no fact's key or value.
	Sealed string `json:"sealed,omitempty"`
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
// declares its key, when the two must agree and the value must be of the
// declared type; a kind that is neither stable nor volatile is refused. It returns the entries that pass,
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
	// A key guard declares is held to that declaration, and a kind that
	// disagrees is refused below; any other key (an external Collection's,
	// which only a Runner loads) keeps the kind the Runner sent.
	declared := guard.Declared
	guard.Declared = func(key string) (Spec, bool) {
		if declared != nil {
			if spec, ok := declared(key); ok {
				return spec, true
			}
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

// sealedBatch is what a Runner seals for one batch: the entries, and the
// batch they belong to, so the Controller refuses sealed entries moved into
// another batch (another job, device, attempt or Runner, or another time).
type sealedBatch struct {
	JobID      string    `json:"job_id"`
	DeviceID   string    `json:"device_id"`
	Attempt    int       `json:"attempt"`
	Runner     string    `json:"runner"`
	ObservedAt time.Time `json:"observed_at"`
	Entries    []Entry   `json:"entries"`
}

// SealPlaintextOf is the plaintext a Runner seals for b: b's entries bound to
// b's own job, device, attempt, Runner and time.
func SealPlaintextOf(b Batch) ([]byte, error) {
	return json.Marshal(sealedBatch{JobID: b.JobID, DeviceID: b.DeviceID, Attempt: b.Attempt,
		Runner: b.By.Runner, ObservedAt: b.ObservedAt.UTC(), Entries: b.Entries})
}

// ErrSealedElsewhere reports sealed entries that open, and name another batch
// than the one that carries them.
var ErrSealedElsewhere = errors.New("facts: the sealed entries name another job, device, attempt, runner or time than their batch")

// OpenSealedEntries is the entries plaintext holds, when it was sealed for b.
// Numbers are kept as written, so a value hashes as it did on the Runner.
func OpenSealedEntries(plaintext []byte, b Batch) ([]Entry, error) {
	var opened sealedBatch
	dec := json.NewDecoder(bytes.NewReader(plaintext))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opened); err != nil {
		return nil, errors.New("facts: the sealed entries do not decode")
	}
	if opened.JobID != b.JobID || opened.DeviceID != b.DeviceID || opened.Attempt != b.Attempt ||
		opened.Runner != b.By.Runner || !opened.ObservedAt.Equal(b.ObservedAt) {
		return nil, ErrSealedElsewhere
	}
	return opened.Entries, nil
}
