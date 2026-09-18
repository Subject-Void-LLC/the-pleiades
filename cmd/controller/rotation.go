// This file runs the master-key rotation the ROTATE_ENCRYPTION_KEYS
// variable asks for, and reports it per table in words an operator can act
// on.
package main

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// runKeyRotation re-encrypts every column held under the master key and logs
// one line per table, then one line saying whether the previous key can be
// removed.
//
// client must carry installCryptoHooks with envelopeSvc, which is what makes
// a read open a row under either configured key and a write seal it under
// the current one.
//
// The final line is the one the production guide tells an operator to wait
// for. It says the previous key is no longer needed only when every table
// was listed and no readable row was skipped. Before this function existed
// the controller rotated devices alone and logged a count, and an operator
// who followed the guide and removed the old key lost every credential and
// every saved survey answer.
func runKeyRotation(ctx context.Context, client *ent.Client, envelopeSvc *crypto.EnvelopeService, logger *slog.Logger) {
	results, err := crypto.RotateAll(ctx, client, envelopeSvc)

	complete := err == nil
	unreadable := 0
	for _, r := range results {
		logger.Info("key rotation pass finished",
			slog.String("table", r.Table),
			slog.Int("rotated", r.Count.Rotated),
			slog.Int("skipped", r.Count.Skipped),
			slog.Int("unreadable", r.Count.Unreadable))
		if !r.Count.Complete() {
			complete = false
		}
		unreadable += r.Count.Unreadable
	}

	if err != nil {
		logger.Error("key rotation could not read every table; keep MASTER_ENCRYPTION_KEY_PREVIOUS set",
			slog.String("error", err.Error()))
	}
	if unreadable > 0 {
		// Named separately because it is not a reason to keep the previous
		// key: these rows opened under neither key, so removing one changes
		// nothing for them. It is a reason to find out where they came from.
		logger.Warn("key rotation found rows that open under neither configured key; removing the previous key does not change them",
			slog.Int("rows", unreadable))
	}
	if !complete {
		logger.Warn("key rotation is incomplete: some rows still need MASTER_ENCRYPTION_KEY_PREVIOUS; keep it set and restart with ROTATE_ENCRYPTION_KEYS=true for another pass")
		return
	}
	logger.Info("key rotation complete: no row needs MASTER_ENCRYPTION_KEY_PREVIOUS any more")
}
