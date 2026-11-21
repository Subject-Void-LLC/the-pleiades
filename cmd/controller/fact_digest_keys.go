// Package main: the Controller's key for digests of files not everyone may
// read (internal/facts' keyed digests). PLEIADES_FACTS_DIGEST_KEY is one
// secret, base64 of 32 bytes, used for nothing else; from it every replica
// derives the same key pair Runners seal raw digests to and the same key that
// keys them, so nothing is stored and no replica disagrees. It is not derived
// from MASTER_ENCRYPTION_KEY, which rotates: a rotation would re-key every
// digest at once and read as every private file changing.
//
// The same secret is what stored facts are tokenized and keyed under
// (internal/facts' atrest.go): the token a fact's row is found by and its
// keyed hash, each under a key derived with a label of its own, none of them
// the digest key. Without the secret the Controller stores no fact at all
// (loadFactAtRestKeys): the fail-closed choice, since the alternative is a
// table of plaintext keys and guessable hashes.
//
// Changing the secret is a rotation with both keys live: the new one in
// PLEIADES_FACTS_DIGEST_KEY and the old one in
// PLEIADES_FACTS_DIGEST_KEY_PREVIOUS. The store re-keys each device's facts
// from the old keys to the new ones the first time it reads or writes the
// device, and runFactsRekey does the rest in one pass at start
// (facts_rekey.go). What a Runner sealed to the old secret's public half (a
// job dispatched before the change) still opens, with the old secret's key
// pair, and its digests are keyed under the new keyer.
package main

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// factDigestKeyEnv names the Controller's digest secret.
const factDigestKeyEnv = "PLEIADES_FACTS_DIGEST_KEY"

// factDigestKeyPreviousEnv names the digest secret a change of it moves away
// from, kept while the facts stored under it are re-keyed (entstore's
// rekey.go) and while jobs dispatched under it still seal to it.
const factDigestKeyPreviousEnv = "PLEIADES_FACTS_DIGEST_KEY_PREVIOUS"

// factDigestKeys is what the Controller derives from its facts secret for
// what Runners seal. The zero value holds no keys.
type factDigestKeys struct {
	// open is the key pair Runners seal to; its public half rides every
	// dispatch.
	open *sealed.PrivateKey
	// keyer keys every digest the consumer opens, whichever key opened it.
	keyer *facts.Keyer
	// previous is the key pair of PLEIADES_FACTS_DIGEST_KEY_PREVIOUS, which
	// opens what a job dispatched under that secret still seals; nil outside
	// a change of the secret.
	previous *sealed.PrivateKey
}

// loadFactDigestKeys reads the digest secret: with none, no keys and a
// warning, so every restricted digest is dropped rather than kept; a secret
// that will not decode stops the Controller, since an operator who set one
// means it to be used. Beside it, the previous secret's key pair, when
// PLEIADES_FACTS_DIGEST_KEY_PREVIOUS is set (previousFactSecret). A previous
// secret with no current one is refused by loadFactAtRestKeys, which runs
// next.
func loadFactDigestKeys(logger *slog.Logger) (factDigestKeys, error) {
	if os.Getenv(factDigestKeyEnv) == "" {
		logger.Warn("no digest of a file not everyone may read is kept: set " + factDigestKeyEnv + " (base64 of 32 bytes) to keep them keyed; on Compose, `make setup` then `make up` writes one")
		return factDigestKeys{}, nil
	}
	secret, err := decodeEnvelopeKey(factDigestKeyEnv)
	if err != nil {
		return factDigestKeys{}, err
	}
	open, keyer, err := entstore.DigestKeys(secret)
	if err != nil {
		return factDigestKeys{}, err
	}
	// Named by its identity, never its value, so replicas configured with
	// different keys show in their logs rather than as files that drift.
	logger.Info("keeping digests of files not everyone may read keyed", slog.String("key", keyer.ID()))
	keys := factDigestKeys{open: open, keyer: keyer}
	previousSecret, err := previousFactSecret(secret)
	if err != nil {
		return factDigestKeys{}, err
	}
	if previousSecret == nil {
		return keys, nil
	}
	// Only the key pair: what it opens is keyed under the current keyer, so
	// the previous secret keys nothing new.
	previous, _, err := entstore.DigestKeys(previousSecret)
	if err != nil {
		return factDigestKeys{}, err
	}
	keys.previous = previous
	// Named by the public key's fingerprint, which a sealed file names too.
	logger.Info("opening facts Runners sealed to the previous facts key, for jobs dispatched under it, and keying their digests under the current one",
		slog.String("seal_key", previous.Public().Fingerprint()))
	return keys, nil
}

// factDigestOptions is the Worker's and the facts consumer's options for the
// digest keys: the current public half on every dispatch, and the keys to
// open and key what Runners seal, with the previous key pair beside them
// during a change of the secret. With no keys, neither, so every such digest
// is dropped. main uses it, and so does its test.
func factDigestOptions(keys factDigestKeys) ([]dispatch.WorkerOption, []entstore.SubscriberOption) {
	if keys.open == nil || keys.keyer == nil {
		return nil, nil
	}
	consumer := []entstore.SubscriberOption{entstore.WithDigestKeys(keys.open, keys.keyer)}
	if keys.previous != nil {
		consumer = append(consumer, entstore.WithPreviousDigestKey(keys.previous))
	}
	// Only the current public half rides a dispatch, so every job dispatched
	// from now on seals to the current key.
	return []dispatch.WorkerOption{dispatch.WithFactSealKey(keys.open.Public().String())}, consumer
}

// loadFactAtRestKeys derives the keys stored facts are tokenized and keyed
// under from the digest secret, and the previous keys from
// PLEIADES_FACTS_DIGEST_KEY_PREVIOUS when it is set (loadPreviousFactKeys).
// With no secret there are none, and the fact store then stores nothing and
// reads nothing (facts.ErrNoKeys): every facts batch a Runner publishes is
// dropped with an error in the log, and the facts routes answer 503. That is
// said once here, as a warning, so an operator who never set the secret
// learns what it costs. A secret that will not decode stops the Controller,
// as it does for the digest keys.
func loadFactAtRestKeys(logger *slog.Logger) (*facts.Keys, *facts.Keys, error) {
	if os.Getenv(factDigestKeyEnv) == "" {
		if os.Getenv(factDigestKeyPreviousEnv) != "" {
			return nil, nil, fmt.Errorf("%s is set but %s is not: the previous facts key is kept only beside a new one, to re-key the facts stored under it", factDigestKeyPreviousEnv, factDigestKeyEnv)
		}
		logger.Warn("no fact is stored: stored facts are sealed under keys derived from " + factDigestKeyEnv + " (base64 of 32 bytes), and without it this controller keeps none rather than keep them readable; on Compose, `make setup` then `make up` writes one")
		return nil, nil, nil
	}
	secret, err := decodeEnvelopeKey(factDigestKeyEnv)
	if err != nil {
		return nil, nil, err
	}
	keys, err := entstore.AtRestKeys(secret)
	if err != nil {
		return nil, nil, err
	}
	// Named by identity, never value: every stored row carries it, and a
	// replica holding another secret refuses the rows this one wrote.
	logger.Info("storing facts sealed, found by token and compared by keyed hash", slog.String("keys", keys.ID()))
	previous, err := loadPreviousFactKeys(logger, secret)
	if err != nil {
		return nil, nil, err
	}
	return keys, previous, nil
}

// previousFactSecret is PLEIADES_FACTS_DIGEST_KEY_PREVIOUS decoded, the facts
// secret a rotation moves away from, or nil when it is not set. It is held
// to the same rule as the current secret, current (base64 of 32 bytes), and
// refused when it is current, since keeping one key twice re-keys nothing
// and an operator who set it meant another. The at-rest keys stored facts
// are re-keyed from (loadPreviousFactKeys) and the key pair that opens what
// Runners sealed to it (loadFactDigestKeys) both come from it.
func previousFactSecret(current []byte) ([]byte, error) {
	if os.Getenv(factDigestKeyPreviousEnv) == "" {
		return nil, nil
	}
	secret, err := decodeEnvelopeKey(factDigestKeyPreviousEnv)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(secret, current) == 1 {
		return nil, fmt.Errorf("%s is the same key as %s: set it to the key the stored facts were sealed under before the change, or unset it", factDigestKeyPreviousEnv, factDigestKeyEnv)
	}
	return secret, nil
}

// loadPreviousFactKeys derives the previous at-rest keys from
// PLEIADES_FACTS_DIGEST_KEY_PREVIOUS (previousFactSecret), or none when it is
// not set. The previous secret's key pair, which opens what a job dispatched
// under it seals, is loadFactDigestKeys'.
func loadPreviousFactKeys(logger *slog.Logger, current []byte) (*facts.Keys, error) {
	secret, err := previousFactSecret(current)
	if err != nil || secret == nil {
		return nil, err
	}
	previous, err := entstore.AtRestKeys(secret)
	if err != nil {
		return nil, err
	}
	logger.Info("re-keying facts stored under the previous facts key as each device is read or written, and in one pass at start",
		slog.String("keys", previous.ID()))
	return previous, nil
}
