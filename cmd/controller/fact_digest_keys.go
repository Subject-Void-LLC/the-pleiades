// Package main: the Controller's key for digests of files not everyone may
// read (internal/facts' keyed digests). PLEIADES_FACTS_DIGEST_KEY is one
// secret, base64 of 32 bytes, used for nothing else; from it every replica
// derives the same key pair Runners seal raw digests to and the same key that
// keys them, so nothing is stored and no replica disagrees. It is not derived
// from MASTER_ENCRYPTION_KEY, which rotates: a rotation would re-key every
// digest at once and read as every private file changing.
package main

import (
	"log/slog"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
)

// factDigestKeyEnv names the Controller's digest secret.
const factDigestKeyEnv = "PLEIADES_FACTS_DIGEST_KEY"

// loadFactDigestKeys reads the digest secret: with none, no keys and a
// warning, so every restricted digest is dropped rather than kept; a secret
// that will not decode stops the Controller, since an operator who set one
// means it to be used.
func loadFactDigestKeys(logger *slog.Logger) (*sealed.PrivateKey, *facts.Keyer, error) {
	if os.Getenv(factDigestKeyEnv) == "" {
		logger.Warn("no digest of a file not everyone may read is kept: set " + factDigestKeyEnv + " (base64 of 32 bytes) to keep them keyed")
		return nil, nil, nil
	}
	secret, err := decodeEnvelopeKey(factDigestKeyEnv)
	if err != nil {
		return nil, nil, err
	}
	return entstore.DigestKeys(secret)
}
