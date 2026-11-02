// Package facts persists what a run's tasks observed about their devices
// (Phase 41): the values a method emits through sdk.EmitFact, one row per
// device and key, written only when a value changes, so a key's rows are the
// history of its value and the newest is its current one.
//
// This package holds what both tiers share: a fact's hash, the bounds a value
// must keep (Prepare), and the Crawl CLI's file store under .pleiades/facts.
// It imports no store and no ent, so the engine prepares observations with
// it; the engine reaches a store only through its own port
// (engine.FactRecorder). The Controller's store over the ent Fact table is
// internal/facts/entstore.
package facts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Bounds on what one observation may hold. A fact is device-supplied data
// crossing into storage and, through the API, back out to a client, so each
// is bounded rather than trusted.
const (
	// MaxKeyBytes bounds a key's length, as the ent column does.
	MaxKeyBytes = 256
	// MaxValueBytes bounds one value's canonical JSON.
	MaxValueBytes = 64 << 10
	// MaxFactsPerObservation bounds how many keys one task's observation
	// may hold.
	MaxFactsPerObservation = 256
)

// Entry is one stored fact: a value of a key, when it was first observed with
// that value, and when it was last confirmed.
type Entry struct {
	Key        string    `json:"key"`
	Value      any       `json:"value"`
	Hash       string    `json:"hash"`
	ObservedAt time.Time `json:"observed_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// Store persists observations and reads them back.
type Store interface {
	// Record stores what a task observed on device at, as Prepare returned
	// it: a row for each key whose hash differs from its newest row, and
	// the last-seen time of every key observed.
	Record(ctx context.Context, device string, entries []Entry, at time.Time) error
	// Current is the newest value of each of device's keys, by key.
	Current(ctx context.Context, device string) ([]Entry, error)
	// History is every value device's key has had, oldest first.
	History(ctx context.Context, device, key string) ([]Entry, error)
}

// ErrInvalid marks an observation refused as a whole: a device with no name,
// or more keys than MaxFactsPerObservation.
var ErrInvalid = errors.New("facts: invalid observation")

// Canonical returns value's canonical JSON: encoding/json's, after the value
// has been through JSON once with its numbers kept as written, so a value
// hashes the same whatever Go types carried it (an int in process, a float64
// after a child process boundary) and map keys are in order.
func Canonical(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var normalized any
	if err := dec.Decode(&normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

// Hash is a fact's hash: SHA-256, hex-encoded, over the canonical JSON of the
// pair [key, value].
func Hash(key string, value any) (string, error) {
	canon, err := Canonical([]any{key, value})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// ValidKey reports whether key may name a fact: non-empty, within
// MaxKeyBytes, and printable with no space, so a key reads the same in a
// report, a URL and a file.
func ValidKey(key string) bool {
	if key == "" || len(key) > MaxKeyBytes {
		return false
	}
	for _, r := range key {
		if !unicode.IsPrint(r) || unicode.IsSpace(r) || r == '/' {
			return false
		}
	}
	return true
}

// Problem is why one key of an observation was not stored.
type Problem struct {
	Key    string
	Reason string
}

// Guard is what keeps a secret out of storage: the values the run knows to be
// secrets, and two checks a caller supplies from internal/redact (this package
// imports only the standard library): Shaped, whether text holds something
// shaped like a secret (a private key, a bearer token, a URL with a
// password), and SecretName, whether a key is named like one (password,
// token). Either may be nil. It is a strip list, never a proof: a secret the
// run does not know, in a shape redact does not recognize, is not caught.
type Guard struct {
	Secrets    []string
	Shaped     func(text string) bool
	SecretName func(name string) bool
}

// Prepare checks an observation and returns the facts that may be stored,
// each with its hash, and why each other key was not: a key that is not
// ValidKey or is named like a secret, a value that will not encode or is past
// MaxValueBytes, or one holding a known secret or text shaped like one (a
// secret is never stored, masked or not). An observation with more than
// MaxFactsPerObservation keys is refused whole.
func Prepare(observed map[string]any, guard Guard) ([]Entry, []Problem, error) {
	if len(observed) > MaxFactsPerObservation {
		return nil, nil, fmt.Errorf("%w: %d facts, more than %d", ErrInvalid, len(observed), MaxFactsPerObservation)
	}
	var out []Entry
	var problems []Problem
	for key, value := range observed {
		if !ValidKey(key) {
			problems = append(problems, Problem{Key: key, Reason: "not a fact name: empty, too long, or holding a space, a slash or an unprintable character"})
			continue
		}
		if guard.SecretName != nil && guard.SecretName(key) {
			problems = append(problems, Problem{Key: key, Reason: "it is named like a secret, which is never stored"})
			continue
		}
		canon, err := Canonical(value)
		if err != nil {
			problems = append(problems, Problem{Key: key, Reason: "its value does not encode as JSON: " + err.Error()})
			continue
		}
		if len(canon) > MaxValueBytes {
			problems = append(problems, Problem{Key: key, Reason: fmt.Sprintf("its value is %d bytes, more than %d", len(canon), MaxValueBytes)})
			continue
		}
		if holdsSecret(canon, guard.Secrets) {
			problems = append(problems, Problem{Key: key, Reason: "its value holds a secret of this run, which is never stored"})
			continue
		}
		if guard.Shaped != nil && guard.Shaped(string(canon)) {
			problems = append(problems, Problem{Key: key, Reason: "its value holds text shaped like a secret, which is never stored"})
			continue
		}
		hash, err := Hash(key, value)
		if err != nil {
			problems = append(problems, Problem{Key: key, Reason: err.Error()})
			continue
		}
		var normalized any
		dec := json.NewDecoder(bytes.NewReader(canon))
		dec.UseNumber()
		if err := dec.Decode(&normalized); err != nil {
			problems = append(problems, Problem{Key: key, Reason: err.Error()})
			continue
		}
		out = append(out, Entry{Key: key, Value: normalized, Hash: hash})
	}
	return out, problems, nil
}

// holdsSecret reports whether canonical JSON text holds any secret, as text
// or as JSON would write it.
func holdsSecret(canon []byte, secrets []string) bool {
	text := string(canon)
	for _, s := range secrets {
		if s == "" {
			continue
		}
		if strings.Contains(text, s) {
			return true
		}
		if quoted, err := json.Marshal(s); err == nil && strings.Contains(text, strings.Trim(string(quoted), `"`)) {
			return true
		}
	}
	return false
}
