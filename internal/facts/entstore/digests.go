// Package entstore: keyed digests on the Controller (Phase 41c, internal/facts'
// keyed digests). A Runner holds no key, so it seals a raw digest to the
// Controller's public key; here, after a batch's hashes are verified and
// before anything is written, each sealed digest is opened, held to the
// device and fact it was sealed for, and replaced by the Controller's keyed
// digest, and the entry's hash recomputed over what is stored. A Controller
// without the keys, or a sealed digest that will not open as this fact's,
// stores the fact without it: never sealed, never raw. During a change of the
// facts secret, what a Runner sealed to the previous secret's key opens too
// (WithPreviousDigestKey), and is keyed under the current keyer.
package entstore

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
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

// AtRestKeys derives, from the same Controller secret, the keys the store
// tokenizes fact keys and keys fact hashes under (facts.Keys), each under a
// label of its own: none is the keyed-digest key or the seal key above.
func AtRestKeys(secret []byte) (*facts.Keys, error) {
	return facts.NewKeys(secret, "controller")
}

// WithDigestKeys opens sealed digests with open and keys them with keyer.
// Without it every sealed digest is dropped.
func WithDigestKeys(open *sealed.PrivateKey, keyer *facts.Keyer) SubscriberOption {
	return func(s *Subscriber) {
		s.digestOpen, s.digestKeyer = open, keyer
	}
}

// WithPreviousDigestKey also opens what Runners sealed to previous, the seal
// key derived from the facts secret a change of it moves away from
// (PLEIADES_FACTS_DIGEST_KEY_PREVIOUS). A job dispatched under that secret
// carries its public half, so its Runner seals the job's batches and digests
// to it, however long after the change the job runs or the broker redelivers
// what it published. It opens nothing without WithDigestKeys, and a digest it
// opens is keyed under that option's keyer, the current one, as any other:
// the previous secret keys nothing new.
func WithPreviousDigestKey(previous *sealed.PrivateKey) SubscriberOption {
	return func(s *Subscriber) {
		s.digestOpenPrevious = previous
	}
}

// sealKeyFor is the key that opens file, chosen by the recipient file names:
// the current seal key, or the previous one (WithPreviousDigestKey), with
// previous true for the second. A file naming any other recipient, a file not
// sealed to a key, and a controller with no current key get none, so nothing
// opens with a key that is neither.
//
// The key is chosen by that name rather than by trying each key until one
// opens. Both open exactly the same files, since OpenWithKey refuses a key
// the file does not name before it decrypts anything; choosing by name also
// says which key opened a file (Handle notes what the previous key still
// opens) and refuses a file sealed to neither key without any decryption. It
// is sound because the name decides nothing by itself: the Recipient header
// is bound into the encryption (internal/sealed's binding, which is HPKE's
// info), so a header naming one of these keys on a file sealed to another
// sends the file to a key that cannot open it, and a header changed after
// sealing makes the file refuse to open even with the key it was sealed to.
func (s *Subscriber) sealKeyFor(file *sealed.File) (key *sealed.PrivateKey, previous bool) {
	if s.digestOpen == nil || file.Method != sealed.MethodKey {
		return nil, false
	}
	if file.Recipient == s.digestOpen.Public().Fingerprint() {
		return s.digestOpen, false
	}
	// Compared only when a previous key is held, so an absent one matches
	// nothing (a key-sealed file always names a recipient: sealed.Parse).
	if s.digestOpenPrevious != nil && file.Recipient == s.digestOpenPrevious.Public().Fingerprint() {
		return s.digestOpenPrevious, true
	}
	return nil, false
}

// keyDigests is entries with every sealed digest keyed, or dropped when it
// cannot be, each changed entry's hash recomputed, the keys of the facts
// whose digest was dropped, and how many digests opened with the previous
// seal key. jobID and deviceID are the batch's, which a sealed digest must
// name.
func (s *Subscriber) keyDigests(jobID, deviceID string, entries []facts.Entry) ([]facts.Entry, []string, int) {
	out := entries[:0]
	var lost []string
	underPrevious := 0
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
		if keyed, previous, ok := s.openDigest(jobID, deviceID, e.Key, sealedText); ok {
			// Keyed by the current keyer whichever key opened it.
			value[facts.KeyedDigestField], value[facts.KeyedDigestKeyField] = keyed, s.digestKeyer.ID()
			if previous {
				underPrevious++
			}
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
	return out, lost, underPrevious
}

// openDigest is the keyed digest of the raw digest sealedText holds, when the
// Controller has its keys and sealedText opens, as a fact digest, to one
// sealed in jobID for deviceID's fact key, and whether the previous seal key
// opened it (sealKeyFor). Either way it is keyed under the current keyer.
func (s *Subscriber) openDigest(jobID, deviceID, key, sealedText string) (keyed string, previous, ok bool) {
	if s.digestOpen == nil || s.digestKeyer == nil {
		return "", false, false
	}
	file, err := sealed.Parse([]byte(sealedText))
	if err != nil || file.Purpose != sealed.PurposeFactDigest {
		return "", false, false
	}
	open, previous := s.sealKeyFor(file)
	if open == nil {
		return "", false, false
	}
	plaintext, err := file.OpenWithKey(open)
	if err != nil {
		return "", false, false
	}
	raw, err := facts.OpenPlaintext(plaintext, jobID, deviceID, key)
	if err != nil {
		return "", false, false
	}
	keyed, err = s.digestKeyer.Seal(deviceID, key, raw)
	return keyed, previous, err == nil
}

// openBatch is the entries batch's Runner sealed to this controller, and
// whether the previous seal key opened them: the sealed file must be a fact
// batch, name a key this controller holds (sealKeyFor), open with it, and name
// the batch that carries it. Every refusal is fixed text, naming no byte the
// batch delivered.
func (s *Subscriber) openBatch(batch facts.Batch) ([]facts.Entry, bool, error) {
	if s.digestOpen == nil {
		return nil, false, errors.New("this controller has no key to open sealed facts with (PLEIADES_FACTS_DIGEST_KEY is not set)")
	}
	file, err := sealed.Parse([]byte(batch.Sealed))
	if err != nil || file.Purpose != sealed.PurposeFactBatch {
		return nil, false, errors.New("the sealed entries are not a sealed fact batch")
	}
	open, previous := s.sealKeyFor(file)
	if open == nil {
		return nil, false, errors.New("the sealed entries are sealed to a key this controller does not hold, neither PLEIADES_FACTS_DIGEST_KEY's nor PLEIADES_FACTS_DIGEST_KEY_PREVIOUS's")
	}
	plaintext, err := file.OpenWithKey(open)
	if err != nil {
		return nil, false, errors.New("the sealed entries do not open with the key they name")
	}
	entries, err := facts.OpenSealedEntries(plaintext, batch)
	return entries, previous, err
}
