// These tests pin what Inspect answers for every state a database can be in,
// that it never writes, and that a database it cannot read is an error rather
// than a database that looks new. That last one matters more than it looks:
// compose's make up takes its pre-upgrade backup only when Inspect says
// "pending", so a read failure answered as "fresh" would upgrade a full
// database with no backup behind it.
package migrate

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// TestInspect_AnswersForEveryState walks one database through its life and
// checks the verdict at each step, on both dialects.
func TestInspect_AnswersForEveryState(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db := freshDB(t, dialectName)
		names := namesOf(t, dialectName)
		inspect := func() Plan {
			t.Helper()
			plan, err := Inspect(ctx, dialectName, db)
			if err != nil {
				t.Fatalf("Inspect(): %v", err)
			}
			return plan
		}

		// Nothing yet, and inspecting must not change that.
		plan := inspect()
		if plan.Verdict != VerdictFresh || len(plan.Pending) != len(names) || plan.Recorded != 0 {
			t.Fatalf("a new database: %+v; want fresh with all %d pending", plan, len(names))
		}
		if exists, err := versionTableExists(ctx, db, migrationSources[dialectName]); err != nil || exists {
			t.Fatalf("Inspect created the history table (%v, %v); it must never write", exists, err)
		}

		// Part way: an upgrade with data behind it.
		middle := names[len(names)/2]
		if _, err := applyPending(ctx, db, migrationSources[dialectName], names, middle, gateStrict); err != nil {
			t.Fatalf("applying through %s: %v", middle, err)
		}
		plan = inspect()
		if plan.Verdict != VerdictPending || plan.DatabaseHead != middle || plan.BuildHead != names[len(names)-1] {
			t.Fatalf("a part-migrated database: %+v; want pending from %s", plan, middle)
		}
		for _, p := range plan.Pending {
			if _, contracted := contracts[dialectName][p.Name]; (p.Kind == "contract") != contracted {
				t.Errorf("pending %s is reported as %s", p.Name, p.Kind)
			}
		}

		// Up to date.
		if err := Apply(ctx, dialectName, db); err != nil {
			t.Fatalf("Apply(): %v", err)
		}
		if plan = inspect(); plan.Verdict != VerdictCurrent || !plan.Serves() {
			t.Fatalf("an up to date database: %+v; want current", plan)
		}

		// A newer build migrated it further, within the window, then past it.
		next := nextName(t, names[len(names)-1], "from_a_newer_build")
		if _, err := db.ExecContext(ctx, migrationSources[dialectName].insertVersion, next, time.Now().UTC(), names[len(names)-1]); err != nil {
			t.Fatalf("recording a newer build's migration: %v", err)
		}
		if plan = inspect(); plan.Verdict != VerdictNewerWithinWindow || !plan.Serves() || len(plan.Newer) != 1 {
			t.Fatalf("a newer database within the window: %+v", plan)
		}
		if _, err := db.ExecContext(ctx, `UPDATE schema_migrations SET compatible_from = version WHERE version = '`+next+`'`); err != nil {
			t.Fatalf("raising the floor: %v", err)
		}
		if plan = inspect(); plan.Verdict != VerdictRefused || plan.Serves() || plan.Refusal == "" {
			t.Fatalf("a newer database past the window: %+v; want refused with a reason", plan)
		}
	})
}

// TestInspect_AnUnreadableDatabaseIsAnErrorNotANewOne is the control for the
// reason this file exists: a closed database must fail, not look empty.
func TestInspect_AnUnreadableDatabaseIsAnErrorNotANewOne(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		db := freshDB(t, dialectName)
		if err := Apply(context.Background(), dialectName, db); err != nil {
			t.Fatalf("Apply(): %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		plan, err := Inspect(context.Background(), dialectName, db)
		if err == nil {
			t.Fatalf("Inspect() on a closed database = %+v with no error; a make up would skip its backup on that answer", plan)
		}
	})
}

// TestInspect_RefusesAHistoryWithoutItsKey proves the read-only path refuses
// the same keyless table the apply path does.
func TestInspect_RefusesAHistoryWithoutItsKey(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		db := freshDB(t, dialectName)
		if _, err := db.Exec(`CREATE TABLE schema_migrations (version TEXT, applied_at TIMESTAMP NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		plan, err := Inspect(context.Background(), dialectName, db)
		if err != nil || plan.Verdict != VerdictRefused {
			t.Fatalf("Inspect() of a keyless history = %+v, %v; want refused", plan, err)
		}
	})
}

// TestApplyWith_RefuseToUpgrade proves the admin commands' option: a new
// database is migrated, one already current is used, and one with data and
// migrations pending is refused and left exactly as it was.
func TestApplyWith_RefuseToUpgrade(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		opts := Options{RefuseToUpgrade: true, AllowNewerWithinWindow: true}
		names := namesOf(t, dialectName)

		fresh := freshDB(t, dialectName)
		if out, err := ApplyWith(ctx, dialectName, fresh, opts); err != nil || len(out.Applied) != len(names) {
			t.Fatalf("a new install = %+v, %v; want every migration applied", out, err)
		}
		if _, err := ApplyWith(ctx, dialectName, fresh, opts); err != nil {
			t.Fatalf("a current database = %v; want it used", err)
		}

		behind := freshDB(t, dialectName)
		middle := names[len(names)/2]
		if _, err := applyPending(ctx, behind, migrationSources[dialectName], names, middle, gateStrict); err != nil {
			t.Fatal(err)
		}
		_, err := ApplyWith(ctx, dialectName, behind, opts)
		if !errors.Is(err, ErrUpgradeRequired) {
			t.Fatalf("a database with migrations pending = %v; want ErrUpgradeRequired", err)
		}
		if got := len(historyOf(t, behind, dialectName)); got != len(names)/2+1 {
			t.Errorf("the refused database records %d migrations; want it untouched at %d", got, len(names)/2+1)
		}
	})
}

// TestPlanForNewDatabase_IsWhatAnEmptyDatabaseAnswers pins the one property
// that function exists for: cmd/controller answers `migrate --plan` from it
// when the SQLite file it would open does not exist yet, so its answer has to
// be the answer that file would give the moment it did exist and held
// nothing. An unknown dialect is an error rather than an empty plan, because
// an empty plan reads as "nothing to do".
func TestPlanForNewDatabase_IsWhatAnEmptyDatabaseAnswers(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		fromEmpty, err := Inspect(context.Background(), dialectName, freshDB(t, dialectName))
		if err != nil {
			t.Fatalf("Inspect() on an empty database: %v", err)
		}
		fromNothing, err := PlanForNewDatabase(dialectName)
		if err != nil {
			t.Fatalf("PlanForNewDatabase(): %v", err)
		}
		if !reflect.DeepEqual(fromNothing, fromEmpty) {
			t.Errorf("PlanForNewDatabase() = %+v; want exactly what an empty database answers, %+v", fromNothing, fromEmpty)
		}
		if fromNothing.Verdict != VerdictFresh || len(fromNothing.Pending) != len(namesOf(t, dialectName)) || fromNothing.Recorded != 0 {
			t.Errorf("PlanForNewDatabase() = %+v; want fresh, nothing recorded, every migration pending", fromNothing)
		}
	})

	if plan, err := PlanForNewDatabase("oracle"); err == nil {
		t.Errorf("PlanForNewDatabase(\"oracle\") = %+v, nil; want an error naming the dialect", plan)
	}
}
