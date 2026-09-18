// This file records the master encryption key a controller runs with in the
// encryption key registry, by fingerprint, the first time any controller
// runs with it.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
)

// recordKeyFirstUse registers the current master key as first used by this
// database, unless a row already names it.
//
// It is how a key generated where no database was reachable still enters
// the activity trail. The setup command records a key it generates when it
// can reach the database, as generated; a Helm install generates its Secret
// before any database exists, so the first controller to start with that key
// records it here instead, as first used. The two are distinguishable in the
// trail because they are different facts.
//
// It never stops the controller. A registry that cannot be written is an
// audit gap to log, not a reason to refuse to serve, and several replicas
// starting together are expected to race here: the registry's unique index
// turns every loser into a no-op.
func recordKeyFirstUse(ctx context.Context, client *ent.Client, logger *slog.Logger) {
	key, err := crypto.DecodeKey(os.Getenv("MASTER_ENCRYPTION_KEY"), "environment variable MASTER_ENCRYPTION_KEY")
	if err != nil {
		// loadEnvelopeService has already accepted this value, so this is
		// unreachable in practice; logging it is still the honest answer.
		logger.Warn("could not record the master key in the activity trail", slog.String("error", err.Error()))
		return
	}
	registry := keyregistry.NewAuditedStore(
		keyregistry.NewEntStore(client),
		activity.NewEntStore(client),
		func(context.Context) string { return "controller" },
		logger,
	)
	record, created, err := registry.Register(ctx, keyregistry.Record{
		Fingerprint: crypto.Fingerprint(key),
		Version:     getenv("MASTER_ENCRYPTION_KEY_VERSION", "v1"),
		Origin:      keyregistry.OriginFirstUse,
		Possession:  keyregistry.PossessionNotApplicable,
	})
	if err != nil {
		logger.Warn("could not record the master key in the activity trail", slog.String("error", err.Error()))
		return
	}
	if created {
		logger.Info("recorded the master key as first used by this database",
			slog.String("key", keyregistry.Short(record.Fingerprint)))
	}
}
