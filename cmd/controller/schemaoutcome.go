// This file reports what opening the database did to its schema, so an
// operator reading a controller's startup log can tell an upgrade from an
// ordinary restart, and a replica that lost the race to migrate from one that
// did the work.
package main

import (
	"context"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"
)

// logSchemaOutcome logs what migrating the database at startup found and did.
//
// Every field is a migration file name or a count, never data from a table,
// so nothing here can carry a secret.
func logSchemaOutcome(ctx context.Context, logger *slog.Logger, outcome migrate.Outcome) {
	if len(outcome.NotReclaimed) > 0 {
		// The migration is applied; what it deleted may still be readable in
		// the database's file. Each entry is a file name and the database's
		// own refusal of a fixed statement.
		logger.WarnContext(ctx, "a migration deleted rows whose bytes could not be removed from the database file; on SQLite, run VACUUM once the controller is stopped",
			"migrations", outcome.NotReclaimed)
	}
	switch {
	case len(outcome.Newer) > 0:
		// Serving a database a newer build moved on. Worth saying at warning
		// level: it is expected for the length of a rolling upgrade or after
		// a rollback, and a sign of a stalled one if it persists.
		logger.WarnContext(ctx, "serving a database a newer build migrated, within this build's compatibility window",
			"newer_migrations", outcome.Newer, "floor", outcome.Floor)
	case len(outcome.Applied) > 0 || len(outcome.AppliedElsewhere) > 0:
		logger.InfoContext(ctx, "database schema upgraded",
			"applied", len(outcome.Applied),
			"applied_by_another_controller", len(outcome.AppliedElsewhere))
	default:
		logger.DebugContext(ctx, "database schema already current")
	}
}
