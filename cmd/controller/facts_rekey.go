// This file runs the pass that finishes a change of the facts key
// (PLEIADES_FACTS_DIGEST_KEY_PREVIOUS beside a new PLEIADES_FACTS_DIGEST_KEY)
// without waiting for every device to be read or written, and reports it in
// words an operator can act on.
package main

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
)

// factsRekeyer is what runFactsRekey needs of the fact store.
type factsRekeyer interface {
	RekeyAll(ctx context.Context) (entstore.RekeyCount, error)
}

// runFactsRekey re-keys every device whose stored facts still name the
// previous facts key (entstore's RekeyAll) and logs what it did, by counts
// and device IDs, never a key, a token or a value. The last line says
// whether the previous key is still needed: it may be unset only once a
// pass reports no device remaining and every replica runs with the new key,
// since a replica still holding only the old key can store a gather under
// it until it restarts.
func runFactsRekey(ctx context.Context, store factsRekeyer, logger *slog.Logger) {
	count, err := store.RekeyAll(ctx)
	logger.Info("facts key rotation pass finished",
		slog.Int("devices", count.Devices),
		slog.Int("facts", count.Facts),
		slog.Int("coverages", count.Coverages),
		slog.Int("observations", count.Observations),
		slog.Int("refused", count.Refused),
		slog.Int("failed", count.Failed),
		slog.Int("remaining", count.Remaining))
	if count.Dropped > 0 {
		logger.Warn("facts key rotation dropped coverage tokens no stored fact held; they matched nothing before and match nothing now",
			slog.Int("tokens", count.Dropped))
	}
	if count.Refused > 0 {
		// Named so an operator can look at them; their facts do not verify
		// under the previous key, and removing it loses them for good.
		logger.Warn("facts key rotation refused devices whose stored facts do not verify under the previous key; they keep it and are served nothing",
			slog.Int("devices", count.Refused), slog.Any("first", count.RefusedDevices))
	}
	if err != nil {
		logger.Error("facts key rotation could not finish its pass; keep "+factDigestKeyPreviousEnv+" set, and restart for another pass",
			slog.String("error", err.Error()))
		return
	}
	if !count.Complete() {
		logger.Warn("facts key rotation is incomplete: some devices still hold facts under the previous key; keep " + factDigestKeyPreviousEnv + " set and restart for another pass")
		return
	}
	logger.Info("facts key rotation complete: no stored fact needs " + factDigestKeyPreviousEnv + " any more; once every replica runs with the new " + factDigestKeyEnv + ", unset it")
}
