// Package native: sealing raw digests to the Controller (internal/facts'
// keyed digests). A Runner holds no key that could key a digest of a file
// not everyone may read, so it seals each to the public key the dispatch
// carries (wire.DispatchPayload.FactSealKey); the stream and this Runner's
// hashes then hold only ciphertext, and the Controller opens and keys it.
// A dispatch with no key, or one that will not parse, seals nothing, and the
// engine drops every such digest.
package native

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/sealed"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// factSealing is the collection executor's options and the executor's option
// for payload's raw digests: sealed to its key and let through to the
// Controller, or, with no usable key, neither (the executor's option then does
// nothing), so each is dropped and the task's warnings say so. A key that will
// not parse is also returned as an error, for the adapter to log.
func factSealing(payload wire.DispatchPayload) ([]engine.CollectionActionExecutorOption, engine.ExecutorOption, error) {
	none := engine.ExecutorOption(func(*engine.Executor) {})
	if payload.FactSealKey == "" {
		return nil, none, nil
	}
	pub, err := sealed.ParsePublicKey(payload.FactSealKey)
	if err != nil {
		return nil, none, err
	}
	return []engine.CollectionActionExecutorOption{engine.WithFactDigestSealer(sealerFor(payload.JobID, pub))}, engine.WithSealedFactDigests(), nil
}

// sealerFor seals a digest to pub with jobID bound in too, so the Controller
// refuses a sealed digest replayed into another job.
func sealerFor(jobID string, pub sealed.PublicKey) engine.DigestSealer {
	return func(deviceID, factKey, raw string) (string, error) {
		plaintext, err := facts.SealPlaintext(jobID, deviceID, factKey, raw)
		if err != nil {
			return "", err
		}
		file, err := sealed.SealToKey(pub, sealed.PurposeFactDigest, plaintext)
		return string(file), err
	}
}
