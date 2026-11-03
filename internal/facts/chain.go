// Package facts: the per-device hash chain over stored stable values (Phase
// 41b). Each value stored carries the chain hash of the device's value stored
// before it, so editing or deleting a stored value breaks the chain at the
// value after it, which VerifyChain finds.
//
// This is tamper-evident, not tamper-proof. Whoever can write the store can
// rewrite the whole chain from the edit onward, and cutting values off the
// end leaves a shorter chain that still verifies; anchoring the chain's head
// outside the store is Part XVI's work. Volatile values are not chained: they
// keep one value each and are replaced by design.
package facts

import (
	"crypto/sha256"
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

// ChainHash is a value's chain hash: SHA-256 over the canonical JSON of the
// previous value's chain hash and what the chain covers of this one (its key,
// its fact hash, when it was observed and by which run).
func ChainHash(prev, key, hash string, observedAt time.Time, by Provenance) (string, error) {
	canon, err := Canonical([]any{prev, key, hash, observedAt.UTC().Format(time.RFC3339Nano), by})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
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
	// Values is how many chained values were checked.
	Values int `json:"values"`
	// Unchained is how many stored values carry no chain at all (stored
	// before Phase 41b); they come first and are not checked.
	Unchained int `json:"unchained"`
	// Head is the last value's chain hash, which a caller may record
	// elsewhere to notice the end being cut off later.
	Head string `json:"head,omitempty"`
	// Break is the first value that fails, nil when every one verifies.
	Break *Break `json:"break,omitempty"`
}

// VerifyChain walks links in the order they were stored and reports the first
// that fails: a value whose fact hash does not describe it (an edited value),
// one whose Prev is not the chain hash of the value before it (one deleted or
// moved), or one whose own chain hash does not match what it covers (an
// edited key, time or provenance). Links with no chain at all may only come
// first: they were stored before the chain existed.
func VerifyChain(links []Link) Verdict {
	var v Verdict
	prev := ""
	chained := false
	for i, l := range links {
		if l.Chain == "" && !chained {
			v.Unchained++
			continue
		}
		chained = true
		v.Values++
		fail := func(reason string) Verdict {
			v.Break = &Break{Index: i, Key: l.Key, Reason: reason}
			return v
		}
		hash, err := Hash(l.Key, l.Value)
		if err != nil || hash != l.Hash {
			return fail("the stored value is not the one its fact hash describes")
		}
		if l.Prev != prev {
			return fail(fmt.Sprintf("it names %s as the value stored before it, and the value stored before it is %s", short(l.Prev), short(prev)))
		}
		want, err := ChainHash(l.Prev, l.Key, l.Hash, l.ObservedAt, l.By)
		if err != nil || want != l.Chain {
			return fail("its chain hash does not match its key, time or provenance")
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
