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
// (facts_rekey.go).
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
// rekey.go).
const factDigestKeyPreviousEnv = "PLEIADES_FACTS_DIGEST_KEY_PREVIOUS"

// loadFactDigestKeys reads the digest secret: with none, no keys and a
// warning, so every restricted digest is dropped rather than kept; a secret
// that will not decode stops the Controller, since an operator who set one
// means it to be used.
func loadFactDigestKeys(logger *slog.Logger) (*sealed.PrivateKey, *facts.Keyer, error) {
	if os.Getenv(factDigestKeyEnv) == "" {
		logger.Warn("no digest of a file not everyone may read is kept: set " + factDigestKeyEnv + " (base64 of 32 bytes) to keep them keyed; on Compose, `make setup` then `make up` writes one")
		return nil, nil, nil
	}
	secret, err := decodeEnvelopeKey(factDigestKeyEnv)
	if err != nil {
		return nil, nil, err
	}
	open, keyer, err := entstore.DigestKeys(secret)
	if err != nil {
		return nil, nil, err
	}
	// Named by its identity, never its value, so replicas configured with
	// different keys show in their logs rather than as files that drift.
	logger.Info("keeping digests of files not everyone may read keyed", slog.String("key", keyer.ID()))
	return open, keyer, nil
}

// factDigestOptions is the Worker's and the facts consumer's options for the
// digest keys: the public half on every dispatch, and the keys to open and
// key what Runners seal. With no keys, neither, so every such digest is
// dropped. main uses it, and so does its test.
func factDigestOptions(open *sealed.PrivateKey, keyer *facts.Keyer) ([]dispatch.WorkerOption, []entstore.SubscriberOption) {
	if open == nil || keyer == nil {
		return nil, nil
	}
	return []dispatch.WorkerOption{dispatch.WithFactSealKey(open.Public().String())},
		[]entstore.SubscriberOption{entstore.WithDigestKeys(open, keyer)}
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

// loadPreviousFactKeys derives the previous at-rest keys from
// PLEIADES_FACTS_DIGEST_KEY_PREVIOUS, the facts key a rotation moves away
// from, or none when it is not set. It is held to the same rule as the
// current one (base64 of 32 bytes) and refused when it is the current
// secret, current, since keeping one key twice re-keys nothing and an
// operator who set it meant another. Only the at-rest keys are derived from
// it. The digest keys (loadFactDigestKeys) are the new secret's alone, so a
// batch or a digest a Runner sealed to the previous secret's public half (a
// job dispatched before the change) does not open here, and is dropped and
// logged as one sealed to another key.
func loadPreviousFactKeys(logger *slog.Logger, current []byte) (*facts.Keys, error) {
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
	previous, err := entstore.AtRestKeys(secret)
	if err != nil {
		return nil, err
	}
	logger.Info("re-keying facts stored under the previous facts key as each device is read or written, and in one pass at start",
		slog.String("keys", previous.ID()))
	return previous, nil
}
