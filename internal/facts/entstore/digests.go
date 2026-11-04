// Package entstore: keyed digests on the Controller (Phase 41c, internal/facts'
// keyed digests). A Runner holds no key, so it seals a raw digest to the
// Controller's public key; here, after a batch's hashes are verified and
// before anything is written, each sealed digest is opened, held to the
// device and fact it was sealed for, and replaced by the Controller's keyed
// digest, and the entry's hash recomputed over what is stored. A Controller
// without the keys, or a sealed digest that will not open as this fact's,
// stores the fact without it: never sealed, never raw.
package entstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// sealKeyLabel separates the key pair Runners seal to from the HMAC key both
// derived from one Controller secret, and names the seal's suite, so a change
// of suite is a deliberate new label (and a re-key), never a silent one.
const sealKeyLabel = "pleiades/facts/seal-key/v1/" + sealed.MethodKey

// DigestKeys derives, from the Controller's one digest secret (at least 32
// bytes, used for nothing else), the key pair Runners seal raw digests to and
// the key that keys them, so neither is stored and every replica given the
// same secret derives the same pair. The public half rides each dispatch.
func DigestKeys(secret []byte) (*sealed.PrivateKey, *facts.Keyer, error) {
	keyer, err := facts.NewKeyer(secret, "controller")
	if err != nil {
		return nil, nil, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sealKeyLabel))
	open, err := sealed.DeriveKey(mac.Sum(nil))
	if err != nil {
		return nil, nil, fmt.Errorf("facts: deriving the digest seal key: %w", err)
	}
	return open, keyer, nil
}

// WithDigestKeys opens sealed digests with open and keys them with keyer.
// Without it every sealed digest is dropped.
func WithDigestKeys(open *sealed.PrivateKey, keyer *facts.Keyer) SubscriberOption {
	return func(s *Subscriber) {
		s.digestOpen, s.digestKeyer = open, keyer
	}
}

// keyDigests is entries with every sealed digest keyed, or dropped when it
// cannot be, each changed entry's hash recomputed, and the keys of the facts
// whose digest was dropped. jobID and deviceID are the batch's, which a sealed
// digest must name.
func (s *Subscriber) keyDigests(jobID, deviceID string, entries []facts.Entry) ([]facts.Entry, []string) {
	out := entries[:0]
	var lost []string
	for _, e := range entries {
		obj, ok := e.Value.(map[string]any)
		if !ok {
			out = append(out, e)
			continue
		}
		text, sealedHere := obj[facts.SealedDigestField]
		if !sealedHere {
			out = append(out, e)
			continue
		}
		value := make(map[string]any, len(obj)+1)
		for name, v := range obj {
			value[name] = v
		}
		delete(value, facts.SealedDigestField)
		sealedText, _ := text.(string)
		if keyed, ok := s.openDigest(jobID, deviceID, e.Key, sealedText); ok {
			value[facts.KeyedDigestField], value[facts.KeyedDigestKeyField] = keyed, s.digestKeyer.ID()
		} else {
			lost = append(lost, e.Key)
		}
		hash, err := facts.Hash(e.Key, value)
		if err != nil {
			// Verify encoded this value a moment ago, so this cannot fail;
			// if it did, an entry with no hash is not stored.
			continue
		}
		e.Value, e.Hash = value, hash
		out = append(out, e)
	}
	return out, lost
}

// openDigest is the keyed digest of the raw digest sealedText holds, when the
// Controller has its keys and sealedText opens, as a fact digest, to one
// sealed in jobID for deviceID's fact key.
func (s *Subscriber) openDigest(jobID, deviceID, key, sealedText string) (string, bool) {
	if s.digestOpen == nil || s.digestKeyer == nil {
		return "", false
	}
	file, err := sealed.Parse([]byte(sealedText))
	if err != nil || file.Purpose != sealed.PurposeFactDigest {
		return "", false
	}
	plaintext, err := file.OpenWithKey(s.digestOpen)
	if err != nil {
		return "", false
	}
	raw, err := facts.OpenPlaintext(plaintext, jobID, deviceID, key)
	if err != nil {
		return "", false
	}
	keyed, err := s.digestKeyer.Seal(deviceID, key, raw)
	return keyed, err == nil
}
