// Package engine: what becomes of a raw digest a method hands over (Phase
// 41c, internal/facts' keyed digests). A method reading a file only some may
// read puts its SHA-256 under facts.RawDigestField; nothing past the method
// may keep or show that value, since a digest of a small secret lets anyone
// who reads it test guesses. Right after the method returns, before its stats
// reach the report, a register, set_fact or the fact store, every raw digest
// is keyed (the Crawl tier, which holds the project's key), sealed to the
// Controller's public key (a Runner, which holds no key), or dropped (any
// executor told neither, so a composition root that forgets fails closed).
package engine

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// DigestSealer seals a raw digest's plaintext (facts.SealPlaintext) to the
// Controller's public key, returning the sealed text.
type DigestSealer func(plaintext []byte) (string, error)

// WithFactDigestKeyer keys every raw digest a method hands over with k, as the
// Crawl tier does with its project's key.
func WithFactDigestKeyer(k *facts.Keyer) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.digestKeyer = k
	}
}

// WithFactDigestSealer seals every raw digest a method hands over with seal,
// as a Runner does with the Controller's public key.
func WithFactDigestSealer(seal DigestSealer) CollectionActionExecutorOption {
	return func(e *collectionActionExecutor) {
		e.digestSealer = seal
	}
}

// WithSealedFactDigests lets a sealed digest through to the fact recorder,
// for a Runner whose recorder publishes to the Controller that opens it.
// Without it the fact store drops one (facts.Guard.AllowSealed).
func WithSealedFactDigests() ExecutorOption {
	return func(x *Executor) {
		x.sealedDigests = true
	}
}

// digestsOf is stats with every raw digest keyed, sealed or dropped: keyed
// when the executor holds a keyer, sealed when it holds a sealer and the task
// ran on a device, dropped otherwise. Any value an object holding one may be,
// a fact or an ordinary stat.
func (e *collectionActionExecutor) digestsOf(device inventory.InventoryItem, stats map[string]any) map[string]any {
	deviceID := ""
	if device != nil {
		deviceID = string(device.ID())
	}
	for key, value := range stats {
		obj, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if _, raw := obj[facts.RawDigestField]; !raw {
			continue
		}
		switch {
		case deviceID == "":
			// A digest belongs to a device's fact; with none it has nowhere
			// to be bound, and is dropped.
			stats[key] = facts.ScrubDigests(obj, false)
		case e.digestKeyer != nil:
			stats[key] = e.digestKeyer.KeyRaw(deviceID, key, obj)
		case e.digestSealer != nil:
			stats[key] = facts.ReplaceRaw(obj, func(raw string) (map[string]any, error) {
				plaintext, err := facts.SealPlaintext(deviceID, key, raw)
				if err != nil {
					return nil, err
				}
				sealed, err := e.digestSealer(plaintext)
				if err != nil {
					return nil, err
				}
				return map[string]any{facts.SealedDigestField: sealed}, nil
			})
		default:
			stats[key] = facts.ScrubDigests(obj, false)
		}
	}
	return stats
}
