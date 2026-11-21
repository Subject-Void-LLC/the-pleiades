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

// Break is where a device's chain first fails to verify.
type Break struct {
	// Index is the position of the value, from 0, in the order the store
	// wrote them.
	Index int `json:"index"`
	// Key is that value's key.
	Key string `json:"key"`
	// Reason says what did not match.
	Reason string `json:"reason"`
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
	var v Verdict
	prev := ""
	for i, l := range links {
		v.Values++
		fail := func(reason string) Verdict {
			v.Break = &Break{Index: i, Key: l.Key, Reason: reason}
			return v
		}
		hash, err := keys.Hash(device, l.Key, l.Value)
		if err != nil || !SameMAC(hash, l.Hash) {
			return fail("the stored value is not the one its fact hash describes")
		}
		// Before the link to the value before it, so a row with no chain is
		// named for that rather than for the link it also lacks (FAILURE_PATTERNS 921).
		if l.Chain == "" {
			return fail("it carries no chain, though every stored value is chained")
		}
		if l.Prev != prev {
			return fail(fmt.Sprintf("it names %s as the value stored before it, and the value stored before it is %s", short(l.Prev), short(prev)))
		}
		want, err := keys.ChainHash(device, l.Prev, l.Key, l.Hash, l.ObservedAt, l.By)
		if err != nil || !SameMAC(want, l.Chain) {
			return fail("its chain hash does not match its device, key, time or provenance")
		}
		prev = l.Chain
	}
	v.Head = prev
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
