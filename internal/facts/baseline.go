// Package facts: baselines and drift over stored facts (Phase 41d).
//
// A baseline is a named, immutable set of (key, keyed hash) pairs for one
// device, taken from the device's own current stable facts or from another
// device's (a golden host). Drift is what has changed since: each stable key
// added, removed or changed against the baseline. Volatile keys are never
// compared, since they move on their own.
//
// A keyed hash is bound to its device (Keys.Hash covers the device), so a
// golden baseline cannot copy the source's hashes: each pair's hash is
// computed again, from the source's value, for the target. A value that
// itself carries a digest keyed for one device (KeyedDigestField, which the
// keyer binds to the device and the fact) is the same file or configuration on
// two devices with two different digests, so it cannot be compared across
// devices and is recorded not comparable, never reported as drift.
//
// Drift here is computed on read, from Current: the Crawl CLI keeps no
// projection. A store keeps no absence, so a fact a later gather no longer
// reports keeps its last value in Current: removed is a key the baseline has
// that the device has never reported (a golden key), and a key whose value
// the device's latest gather of its family did not confirm is listed as
// unconfirmed, not as drift. A device never gathered reads unknown, never in sync, and so
// does a baseline taken under other keys, whose hashes say nothing under
// these.
package facts

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Bounds on what a baseline may hold.
const (
	// MaxBaselineNameBytes bounds a baseline's name.
	MaxBaselineNameBytes = 64
	// MaxBaselineReasonBytes bounds why it was taken.
	MaxBaselineReasonBytes = 1024
	// MaxBaselineByBytes bounds who took it.
	MaxBaselineByBytes = 256
	// MaxBaselineSelectors bounds how many key patterns one may name.
	MaxBaselineSelectors = 64
	// MaxBaselinePairs bounds how many keys one baseline holds.
	MaxBaselinePairs = 4 * MaxFactsPerObservation
	// maxBaselinePlainBytes bounds one baseline's line before it is sealed.
	maxBaselinePlainBytes = 32 << 20
)

// Baseline is one named, immutable baseline of a device.
type Baseline struct {
	// Name names it, unique among the device's baselines.
	Name string `json:"name"`
	// Device is the device it is for, by its ID.
	Device string `json:"device"`
	// From is the device it was taken from, by its ID, when that is another
	// device (a golden baseline); empty when it was taken from Device itself.
	From string `json:"from,omitempty"`
	// FromName is From's inventory name when it was taken.
	FromName string `json:"from_name,omitempty"`
	// TakenAt is when it was taken.
	TakenAt time.Time `json:"taken_at"`
	// By is who took it: the operating system user.
	By string `json:"by"`
	// Reason is why it was taken, as its taker wrote it.
	Reason string `json:"reason"`
	// KeysID is the identity of the keys its hashes were keyed under
	// (Keys.ID): a baseline under other keys cannot be compared.
	KeysID string `json:"keys_id"`
	// Keys are the key patterns it was taken over (KeySelector); empty means
	// every stable key.
	Keys KeySelector `json:"keys,omitempty"`
	// Pairs are its keys, in key order.
	Pairs []BaselinePair `json:"pairs"`
}

// BaselinePair is one key of a baseline.
type BaselinePair struct {
	// Key is the fact's key.
	Key string `json:"key"`
	// Hash is the value's keyed hash for the baseline's device; empty when
	// the pair is not comparable.
	Hash string `json:"hash,omitempty"`
	// Value is the value itself, kept sealed with the baseline so a drift can
	// say what it was; absent when the pair is not comparable.
	Value any `json:"value,omitempty"`
	// NotComparable marks a golden pair whose value carries a digest keyed
	// for its source device, which no value of another device can equal.
	NotComparable bool `json:"not_comparable,omitempty"`
}

// KeySelector is the keys a baseline covers: each pattern a key exactly, or
// a prefix ending in "*" (package.*). Empty selects every key.
type KeySelector []string

// ParseKeySelector reads a comma-separated list of key patterns, refusing an
// empty pattern, a "*" anywhere but at the end, and more than
// MaxBaselineSelectors.
func ParseKeySelector(list string) (KeySelector, error) {
	if strings.TrimSpace(list) == "" {
		return nil, nil
	}
	var sel KeySelector
	for _, p := range strings.Split(list, ",") {
		p = strings.TrimSpace(p)
		body := strings.TrimSuffix(p, "*")
		switch {
		case p == "":
			return nil, fmt.Errorf("%w: an empty key pattern", ErrInvalid)
		case strings.Contains(body, "*"):
			return nil, fmt.Errorf("%w: key pattern %q: a * may only end a pattern", ErrInvalid, p)
		case body != "" && !wellFormedKey(body):
			return nil, fmt.Errorf("%w: key pattern %q is not a fact name or prefix", ErrInvalid, p)
		}
		sel = append(sel, p)
	}
	if len(sel) > MaxBaselineSelectors {
		return nil, fmt.Errorf("%w: %d key patterns, more than %d", ErrInvalid, len(sel), MaxBaselineSelectors)
	}
	return sel, nil
}

// Matches reports whether key is one the selector covers.
func (s KeySelector) Matches(key string) bool {
	if len(s) == 0 {
		return true
	}
	for _, p := range s {
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		} else if key == p {
			return true
		}
	}
	return false
}

// checkBaselineName refuses a name that is empty, too long, or holds anything
// but letters, digits, '.', '_' and '-' after its first character.
func checkBaselineName(name string) error {
	if name == "" || len(name) > MaxBaselineNameBytes {
		return fmt.Errorf("%w: a baseline's name is 1 to %d characters", ErrInvalid, MaxBaselineNameBytes)
	}
	for i, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || i > 0 && (r == '.' || r == '_' || r == '-')
		if !ok {
			return fmt.Errorf("%w: baseline name %q: letters, digits, '.', '_' and '-', starting with a letter or digit", ErrInvalid, name)
		}
	}
	return nil
}

// checkBaselineText refuses text (a reason, a user) that is empty, too long,
// not UTF-8, or holds a control character.
func checkBaselineText(what, text string, maxBytes int) error {
	if strings.TrimSpace(text) == "" || len(text) > maxBytes || !utf8.ValidString(text) {
		return fmt.Errorf("%w: a baseline's %s is 1 to %d bytes of text", ErrInvalid, what, maxBytes)
	}
	for _, r := range text {
		if r < 0x20 || r >= 0x7F && r <= 0x9F {
			return fmt.Errorf("%w: a baseline's %s holds a control character", ErrInvalid, what)
		}
	}
	return nil
}

// holdsField reports whether v, a decoded value, has an object field named
// one of names at any depth.
func holdsField(v any, names ...string) bool {
	switch x := v.(type) {
	case map[string]any:
		for name, field := range x {
			for _, n := range names {
				if name == n {
					return true
				}
			}
			if holdsField(field, names...) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if holdsField(item, names...) {
				return true
			}
		}
	}
	return false
}

// DeviceBound reports whether value carries a digest keyed for one device
// (KeyedDigestField, which Keyer.Seal binds to the device and the fact), at
// any depth: no other device's value can equal it.
func DeviceBound(value any) bool {
	return holdsField(value, KeyedDigestField)
}

// HoldsDigest reports whether value carries a digest of any kind at any
// depth (a plain, raw, sealed or keyed one): a value a drift report names as
// changed without showing it.
func HoldsDigest(value any) bool {
	return holdsField(value, KeyedDigestField, PlainDigestField, RawDigestField, SealedDigestField)
}

// BaselinePairs is the pairs of a baseline for target, taken from source's
// current entries (target's own when golden is false): each stable key the
// selector covers, in key order, hashed for target under keys. Under golden a
// device-bound value is recorded not comparable, with neither hash nor value.
func BaselinePairs(keys *Keys, target string, golden bool, source []Entry, sel KeySelector) ([]BaselinePair, error) {
	var pairs []BaselinePair
	seen := map[string]bool{}
	for _, e := range source {
		if e.Kind == KindVolatile || seen[e.Key] || !sel.Matches(e.Key) {
			continue
		}
		seen[e.Key] = true
		if golden && DeviceBound(e.Value) {
			pairs = append(pairs, BaselinePair{Key: e.Key, NotComparable: true})
			continue
		}
		// Hashed again for target, never copied: a keyed hash covers its
		// device, so the source's hash would never equal the target's.
		hash, err := keys.Hash(target, e.Key, e.Value)
		if err != nil {
			return nil, fmt.Errorf("facts: keying %s for the baseline: %w", ShownKey(e.Key), err)
		}
		pairs = append(pairs, BaselinePair{Key: e.Key, Hash: hash, Value: e.Value})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })
	return pairs, nil
}
