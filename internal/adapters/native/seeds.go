// Package native: login seed material a dispatch must not carry.
//
// A method whose manifest sets SeedsLogin receives the seed_* secrets
// (wire.SecretSeedUsername and the rest) about the device whose login it
// writes into a machine it creates. The engine derives them, from the
// executing build's own manifest and the credential store, and only on the
// pleiades CLI: on a Runner the engine refuses a seeding call outright
// (engine's seedLogin), and no Controller puts a seed_* key on a dispatch.
// So a dispatch carrying one was not built by any Controller's code path:
// a forged payload, or a later Controller that decides seeds from its own
// catalog, which may differ from this Runner's. Either way the decision is
// not the Controller's to make, and the key is dropped before any method
// can read it.
package native

import (
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// seedSecretPrefix opens the name of every login seed key wire declares
// (SecretSeedUsername, SecretSeedPassword, SecretSeedIOSSecret and the
// rest); no other secret key carries it, which seeds_test.go holds.
const seedSecretPrefix = "seed_"

// withoutDeliveredSeeds returns payload with every seed_* key removed from
// its Secrets, in a copy so the caller's map is untouched, and logs the
// names (never the values) of the keys it removed. A payload with none is
// returned as it is.
func withoutDeliveredSeeds(logger *slog.Logger, payload wire.DispatchPayload) wire.DispatchPayload {
	var dropped []string
	for key := range payload.Secrets {
		if strings.HasPrefix(key, seedSecretPrefix) {
			dropped = append(dropped, key)
		}
	}
	if len(dropped) == 0 {
		return payload
	}
	kept := maps.Clone(payload.Secrets)
	for _, key := range dropped {
		delete(kept, key)
	}
	payload.Secrets = kept
	slices.Sort(dropped)
	logger.Warn("the dispatch carried login seed material, which only the engine derives and no Controller sends to a Runner; it was dropped before any method could read it",
		slog.String("job", payload.JobID), slog.String("device", payload.DeviceName), slog.Any("keys", dropped))
	return payload
}
