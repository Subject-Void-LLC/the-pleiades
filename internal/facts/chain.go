// Package facts: the per-device hash chain over stored stable values (Phase
// 41b). Each value stored carries the chain hash of the device's value stored
// before it, so editing or deleting a stored value breaks the chain at the
// value after it, which VerifyChain finds.
//
// This is tamper-evident, not tamper-proof. Whoever can write the store and
// holds its key can rewrite the whole chain from the edit onward, and cutting
// values off the end leaves a shorter chain that still verifies; anchoring
// the chain's head outside the store is Part XVI's work. Volatile values are
// not chained: they keep one value each and are replaced by design.
//
// A link is keyed (Keys.ChainHash). Everything it covers but the fact's key
// sits beside it in the same row in the clear (the device, the link before,
// the keyed hash, the time, the provenance), so an unkeyed link, as the chain
// was first written, let anyone holding a copy of the store confirm a guessed
// key with one SHA-256 per guess. Under its own key no guess can be tested.
package facts

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"time"
)

// Link is what the chain covers of one stored value, in the order the store
// wrote them.
type Link struct {
	Key        string
	Value      any
	Hash       string
	ObservedAt time.Time
	By         Provenance
	// Prev is the chain hash of the device's value stored before this one,
	// "" for the first.
	Prev string
	// Chain is this value's chain hash, as the store wrote it.
	Chain string
}

// ChainHash is a value's chain hash: HMAC-SHA-256, hex-encoded, under the
// chain key (derived under its own label, neither the hash key nor the token
// key), over chainInput: the device it belongs to, the previous value's chain
// hash and what the chain covers of this one (its key, its keyed fact hash,
// when it was observed and by which run). Naming the device means one
// device's history copied to another does not verify there. Keying it means
// a row's other columns, which a copy of the store shows, cannot be combined
// with a guessed key to confirm the guess.
func (k *Keys) ChainHash(device, prev, key, hash string, observedAt time.Time, by Provenance) (string, error) {
	if err := k.load(); err != nil {
		return "", err
	}
	canon, err := chainInput(device, prev, key, hash, observedAt, by)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, k.chain)
	mac.Write(canon)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// chainInput is what one chain link covers, as the bytes it is keyed over:
// the canonical JSON of [device, prev, key, hash, the time as stored, by]. A
// JSON array is unambiguous, so no two different inputs give the same bytes.
func chainInput(device, prev, key, hash string, observedAt time.Time, by Provenance) ([]byte, error) {
	return Canonical([]any{device, prev, key, hash, StoredTime(observedAt).Format(time.RFC3339Nano), by})
}

// SameMAC reports whether a and b, two MACs as text (a keyed hash, a chain
// link, a token), are the same, in time that does not depend on where they
// first differ, so how long a check takes says nothing of how much of a
// forged MAC was right.
func SameMAC(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// BreakKind names what a chain check found wrong with one link, for a
// caller that tells breaks apart (the Controller's inspection of a device's
// stored history, internal/facts/entstore's inspect.go). Break.Reason says
// the same in words.
type BreakKind string

// The ways one link fails, in the order VerifyChain checks them.
const (
	// BreakValue: the stored value is not the one its keyed fact hash
	// describes (an edited value, or one keyed under another key).
	BreakValue BreakKind = "hash_mismatch"
	// BreakUnchained: the value carries no chain link at all.
	BreakUnchained BreakKind = "chain_missing"
	// BreakLink: its link names another value than the one stored before it:
	// a value deleted, inserted or moved, a gap or a reordering.
	BreakLink BreakKind = "chain_link_mismatch"
	// BreakChainHash: its chain hash does not match its device, key, time or
	// provenance: one of those edited, or a link made under another key.
	BreakChainHash BreakKind = "chain_hash_mismatch"
)

// Break is where a device's chain first fails to verify.
type Break struct {
	// Index is the position of the value, from 0, in the order the store
	// wrote them.
	Index int `json:"index"`
	// Key is that value's key.
	Key string `json:"key"`
	// Reason says what did not match.
	Reason string `json:"reason"`
	// Kind is what did not match, as a name a program tells apart. It is
	// not part of any document this package's callers publish (the fact
	// chain route and `pleiades facts verify` print Index, Key and Reason).
	Kind BreakKind `json:"-"`
}

// Verdict is what VerifyChain found.
type Verdict struct {
	// Values is how many stored values were checked.
	Values int `json:"values"`
	// Head is the last value's chain hash, which a caller may record
	// elsewhere to notice the end being cut off later.
	Head string `json:"head,omitempty"`
	// Break is the first value that fails, nil when every one verifies.
	Break *Break `json:"break,omitempty"`
}

// VerifyChain walks links in the order they were stored and reports the first
// that fails: a value whose keyed fact hash, recomputed under keys, does not
// describe it (an edited value, or one keyed under another key),
// one whose Prev is not the chain hash of the value before it (one deleted or
// moved), or one whose own chain hash, recomputed under keys, does not match
// what it covers (an edited key, time or provenance, or a link made under
// another key). A link with no chain at all is a break
// wherever it stands: every stable value has been chained since Phase 41b,
// and nothing deployed predates it, so no value is exempt. An exemption for
// an unchained prefix let anyone who can write the store add rows at the
// start of a device's history that verified (FAILURE_PATTERNS 921).
func VerifyChain(device string, links []Link, keys *Keys) Verdict {
	w := NewChainWalk(device, keys)
	for _, l := range links {
		if w.Next(l) != nil {
			break
		}
	}
	return w.Verdict()
}

// ChainWalk is VerifyChain one link at a time, for a caller that reads a
// device's history in pages and holds one page at a time: the same checks,
// in the same order, with the same verdict.
type ChainWalk struct {
	device string
	keys   *Keys
	// v is the verdict so far; prev is the chain hash of the last link that
	// verified, "" before the first.
	v    Verdict
	prev string
}

// NewChainWalk starts a walk over device's chain, checked under keys.
func NewChainWalk(device string, keys *Keys) *ChainWalk {
	return &ChainWalk{device: device, keys: keys}
}

// Next checks l, the next link in the order the store wrote them, and
// returns the walk's break: nil while every link so far verifies. Once one
// fails, Next checks nothing more and returns that break again.
func (w *ChainWalk) Next(l Link) *Break {
	if w.v.Break != nil {
		return w.v.Break
	}
	i := w.v.Values
	w.v.Values++
	fail := func(kind BreakKind, reason string) *Break {
		w.v.Break = &Break{Index: i, Key: l.Key, Reason: reason, Kind: kind}
		return w.v.Break
	}
	hash, err := w.keys.Hash(w.device, l.Key, l.Value)
	if err != nil || !SameMAC(hash, l.Hash) {
		return fail(BreakValue, "the stored value is not the one its fact hash describes")
	}
	// Before the link to the value before it, so a row with no chain is
	// named for that rather than for the link it also lacks (FAILURE_PATTERNS 921).
	if l.Chain == "" {
		return fail(BreakUnchained, "it carries no chain, though every stored value is chained")
	}
	if l.Prev != w.prev {
		return fail(BreakLink, fmt.Sprintf("it names %s as the value stored before it, and the value stored before it is %s", short(l.Prev), short(w.prev)))
	}
	want, err := w.keys.ChainHash(w.device, l.Prev, l.Key, l.Hash, l.ObservedAt, l.By)
	if err != nil || !SameMAC(want, l.Chain) {
		return fail(BreakChainHash, "its chain hash does not match its device, key, time or provenance")
	}
	w.prev = l.Chain
	return nil
}

// Verdict is what the walk has found: how many links it checked, the break
// when one failed, and the head (the last link's chain hash) only when none
// did, as VerifyChain reports.
func (w *ChainWalk) Verdict() Verdict {
	v := w.v
	if v.Break == nil {
		v.Head = w.prev
	}
	return v
}

// short is a chain hash cut to its first twelve characters, "nothing" for
// the empty one.
func short(h string) string {
	switch {
	case h == "":
		return "nothing"
	case len(h) > 12:
		return h[:12]
	}
	return h
}
