//go:build integration

// This file holds the strong form of the migration parity claim: that a
// dialect's committed migrations bring a fresh database exactly to the
// schema ent currently desires, with nothing left over.
//
// It is dialect native by construction. It never string-compares
// PostgreSQL DDL against SQLite DDL, which would be meaningless; it asks
// each dialect's own ent driver whether anything remains to apply, using
// the same Schema.WriteTo mechanism internal/ent/migrate/gen uses to
// produce the files in the first place, so the check and the generator
// cannot disagree about what "up to date" means.
//
// It needs a real server of each dialect, which is why it sits behind the
// integration tag while parity_test.go's cheap structural checks run on
// every build.
package ent_test

import (
	"bytes"
	"context"
	"testing"

	"entgo.io/ent/dialect/sql/schema"
)

// TestParity_MigrationsLeaveNothingToApply proves, for every dialect,
// that opening a fresh database applies a complete schema.
//
// What would have to break for this to fail: a schema edit committed
// without regenerating that dialect's migration (the common case), a
// generated migration that does not actually express what the schema
// declares, or a dialect whose driver renders a column type the migration
// did not create. Any of those leaves a residual diff here.
func TestParity_MigrationsLeaveNothingToApply(t *testing.T) {
	for _, backend := range conformanceBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			client, _ := openConformanceClient(t, backend)

			var buf bytes.Buffer
			// WithDropColumn(true) matches the generator's own setting.
			// Without it ent never proposes a DROP COLUMN, so a migration
			// set that left a stale column behind would diff clean and
			// this test would pass while being wrong.
			if err := client.Schema.WriteTo(ctx, &buf, schema.WithDropColumn(true)); err != nil {
				t.Fatalf("diffing the migrated schema: %v", err)
			}

			if buf.Len() != 0 {
				t.Fatalf("after applying every committed %s migration, ent still wants to change the schema.\n"+
					"Regenerate with: go run internal/ent/migrate/gen/main.go %s <name>\n"+
					"Residual DDL:\n%s", backend.name, backend.name, buf.String())
			}
		})
	}
}
