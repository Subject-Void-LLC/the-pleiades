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
	"unicode/utf8"
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
	// may hold: room for a family of one fact per item (Phase 41c, a
	// package or a service each), which a large host has thousands of.
	MaxFactsPerObservation = 8192
	// MaxObservationBytes bounds the canonical JSON of every value one
	// observation stores, together, so many keys cannot each carry the
	// largest value.
	MaxObservationBytes = 8 << 20
)

// Entry is one stored fact: a value of a key, when it was first observed with
// that value, and when it was last confirmed, with the runs that did each.
type Entry struct {
	Key        string    `json:"key"`
	Value      any       `json:"value"`
	Hash       string    `json:"hash"`
	ObservedAt time.Time `json:"observed_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	// Kind is what the emitting method declared: stable keeps every value,
	// volatile only the latest. A value stored before Phase 41b reads as
	// stable.
	Kind Kind `json:"kind"`
	// By is the run that first observed this value; LastBy the run that last
	// confirmed it. Both are empty for a value stored before Phase 41b.
	By     Provenance `json:"by,omitzero"`
	LastBy Provenance `json:"last_by,omitzero"`
	// OrderedByClock, in a history, says this value's place after the one
	// before it was decided by the clock, because the two runs' orders do
	// not compare (two tiers, a counter that started again, or one unknown).
	OrderedByClock bool `json:"ordered_by_clock,omitempty"`
}

// Store persists observations and reads them back.
type Store interface {
	// Record stores what a task observed on device: for a stable key, a
	// value for each key whose hash differs from the value it held as of
	// the observation's order; for a volatile key, its value when it is the
	// latest; and when each value was last confirmed, and by which run.
	Record(ctx context.Context, device string, obs Observation) error
	// Current is the current value of each of device's keys, by key: the
	// one most recently confirmed, by the run order where it compares.
	Current(ctx context.Context, device string) ([]Entry, error)
	// History is every value device's key has had, oldest first; a
	// volatile key has one.
	History(ctx context.Context, device, key string) ([]Entry, error)
	// Verify checks device's hash chain (VerifyChain).
	Verify(ctx context.Context, device string) (Verdict, error)
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
// MaxKeyBytes, valid UTF-8, and printable with no space, so a key reads the
// same in a report, a URL and a file. Ranging over a string reads a byte that
// is not UTF-8 as U+FFFD, which is printable, so validity is asked first
// (sweep item 16).
func ValidKey(key string) bool {
	if key == "" || len(key) > MaxKeyBytes || !utf8.ValidString(key) {
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
// password), and SecretName, whether a name marks one (password,
// token), asked of a fact's key and of every field name inside its value.
// Either may be nil. It is a strip list, never a proof: a secret the
// run does not know, in a shape redact does not recognize, is not caught.
type Guard struct {
	Secrets    []string
	Shaped     func(text string) bool
	SecretName func(name string) bool
	// Declared is what the emitting method declared about a key (Phase
	// 41b): a key it did not declare, or a value of another type, is not
	// stored. A Spec with no Type checks no type. Nil stores every key as
	// stable and checks no type.
	Declared func(key string) (Spec, bool)
	// AllowSealed keeps a digest sealed to the Controller's key
	// (SealedDigestField): set where one is expected, a sealing Runner or the
	// Controller about to open it. A raw digest (RawDigestField) is dropped
	// everywhere.
	AllowSealed bool
}

// nameOf is the part of key the secret-name guard reads: the whole key, or
// for a key in a declared family (package.<name>) only the family's prefix,
// since what follows it is an item's identifier (a package called
// password-store), not the name of a field that could hold a secret. Its
// value is still held to every other check.
func nameOf(key string, guard Guard) string {
	if guard.Declared == nil {
		return key
	}
	if spec, ok := guard.Declared(key); ok && spec.Family != "" && strings.HasPrefix(key, spec.Family) {
		return spec.Family
	}
	return key
}

// Prepare checks an observation and returns the facts that may be stored,
// each with its hash, and why each other key was not: a key that is not
// ValidKey or is named like a secret, a value that will not encode or is past
// MaxValueBytes, one holding a known secret or text shaped like one, or one
// with a field at any depth named like a secret (a secret is never stored,
// masked or not). An observation with more than
// MaxFactsPerObservation keys, or whose stored values come to more than
// MaxObservationBytes, is refused whole.
func Prepare(observed map[string]any, guard Guard) ([]Entry, []Problem, error) {
	if len(observed) > MaxFactsPerObservation {
		return nil, nil, fmt.Errorf("%w: %d facts, more than %d", ErrInvalid, len(observed), MaxFactsPerObservation)
	}
	var out []Entry
	var problems []Problem
	total := 0
	for key, value := range observed {
		// No digest nothing keyed is stored, whatever path brought it here.
		value = ScrubDigests(value, guard.AllowSealed)
		if !ValidKey(key) {
			problems = append(problems, Problem{Key: key, Reason: "not a fact name: empty, too long, or holding a space, a slash or an unprintable character"})
			continue
		}
		if guard.SecretName != nil && guard.SecretName(nameOf(key, guard)) {
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
		spec := Spec{Kind: KindStable}
		if guard.Declared != nil {
			declared, ok := guard.Declared(key)
			if !ok {
				problems = append(problems, Problem{Key: key, Reason: "the method does not declare this fact"})
				continue
			}
			spec = declared
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
		if spec.Type != "" {
			if reason := checkType(spec.Type, normalized); reason != "" {
				problems = append(problems, Problem{Key: key, Reason: reason})
				continue
			}
		}
		// A field inside the value is held to the rule the key was: an
		// object fact may carry a password as well as be named for one.
		if guard.SecretName != nil {
			if field, ok := secretField(normalized, guard.SecretName); ok {
				problems = append(problems, Problem{Key: key, Reason: fmt.Sprintf("its value has a field named like a secret (%q), which is never stored", field)})
				continue
			}
		}
		total += len(canon)
		out = append(out, Entry{Key: key, Value: normalized, Hash: hash, Kind: spec.Kind})
	}
	if total > MaxObservationBytes {
		return nil, nil, fmt.Errorf("%w: its values come to %d bytes, more than %d", ErrInvalid, total, MaxObservationBytes)
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
		// A secret holding a byte that is not UTF-8 reaches canonical text
		// with each such byte a literal U+FFFD, since Canonical decodes and
		// encodes again; neither form above is that one (sweep item 16).
		if stored := asStoredText(s); stored != s && strings.Contains(text, stored) {
			return true
		}
	}
	return false
}

// asStoredText is s as canonical JSON text holds it: each byte that is not
// UTF-8 a U+FFFD, as encoding/json writes one per byte.
func asStoredText(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteRune(utf8.RuneError)
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
