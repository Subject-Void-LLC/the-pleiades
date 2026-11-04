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
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// DigestSealer seals one fact's raw digest to the Controller's public key,
// returning the sealed text. The caller binds what it knows besides the
// device and fact (a Runner, its job) into what it seals.
type DigestSealer func(deviceID, factKey, raw string) (string, error)

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
// ran on a device, dropped otherwise, and dropped wherever it sits deeper than
// a stat's own object. A digest that is not kept is said in the task's
// warnings, by fact and reason and never by value, so a missing key or a
// failed seal is seen rather than read later as each file changing.
func (e *collectionActionExecutor) digestsOf(device inventory.InventoryItem, stats map[string]any) map[string]any {
	deviceID := ""
	if device != nil {
		deviceID = string(device.ID())
	}
	lost := map[string]string{}
	for key, value := range stats {
		if key == sdk.StatWarnings {
			continue
		}
		obj, ok := value.(map[string]any)
		if _, raw := obj[facts.RawDigestField]; !ok || !raw {
			// No digest of its own; any deeper is dropped.
			stats[key] = facts.ScrubDigests(value, false)
			continue
		}
		switch {
		case deviceID == "":
			// A digest belongs to a device's fact; with none it has nowhere
			// to be bound.
			stats[key], lost[key] = facts.ScrubDigests(obj, false), "the task ran on no device"
		case e.digestKeyer != nil:
			keyed := facts.ScrubDigests(e.digestKeyer.KeyRaw(deviceID, key, obj), false)
			if keyed.(map[string]any)[facts.KeyedDigestField] == nil {
				lost[key] = "the project's digest key could not be read, or the method's digest was not one"
			}
			stats[key] = keyed
		case e.digestSealer != nil:
			sealed := facts.ScrubDigests(facts.ReplaceRaw(obj, func(raw string) (map[string]any, error) {
				text, err := e.digestSealer(deviceID, key, raw)
				if err != nil {
					return nil, err
				}
				return map[string]any{facts.SealedDigestField: text}, nil
			}), true)
			if sealed.(map[string]any)[facts.SealedDigestField] == nil {
				lost[key] = "it could not be sealed to the Controller"
			}
			stats[key] = sealed
		default:
			stats[key], lost[key] = facts.ScrubDigests(obj, false), "this run holds no key to keep it under (a Controller without PLEIADES_FACTS_DIGEST_KEY)"
		}
	}
	keys := make([]string, 0, len(lost))
	for key := range lost {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		appendWarning(stats, fmt.Sprintf("the digest of %s was not kept: %s", key, lost[key]))
	}
	return stats
}

// appendWarning adds warning to stats' warnings (sdk.StatWarnings).
func appendWarning(stats map[string]any, warning string) {
	switch existing := stats[sdk.StatWarnings].(type) {
	case []string:
		stats[sdk.StatWarnings] = append(existing, warning)
	case []interface{}:
		stats[sdk.StatWarnings] = append(existing, warning)
	default:
		stats[sdk.StatWarnings] = []string{warning}
	}
}

// scrubResultDigests holds a node's stats, whatever executor produced them,
// to the rule every result is held to (facts.ScrubDigests): no raw digest at
// any depth, and a sealed one only where this run publishes to a Controller.
func scrubResultDigests(stats map[string]any, allowSealed bool) map[string]any {
	for key, value := range stats {
		stats[key] = facts.ScrubDigests(value, allowSealed)
	}
	return stats
}
