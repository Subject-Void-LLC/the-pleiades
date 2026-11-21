// The contract guard (guard.go) on the real facts at rest contract (SQLite
// 0052, PostgreSQL 0049), against real databases of both dialects, through
// the same ApplyWith a controller opens its database with.
//
// Each test is the case it names: an older controller still running keeps the
// contract out and leaves the database as it was; none running, or only a gone
// one, lets it in; and once it is in, a build from before it is refused at
// start while a build at its floor is not.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// placeholderFor is the nth bind parameter in the dialect's spelling, as
// seedPlaintextFact wants it.
func placeholderFor(dialectName string) func(int) string {
	if dialectName == "postgres" {
		return func(n int) string { return "$" + strconv.Itoa(n) }
	}
	return func(int) string { return "?" }
}

// atFactAtRest returns a fresh database of the dialect migrated through the
// migration before facts at rest, holding one plaintext fact as the build
// before it stored one, with the dialect's migrations and the contract's two
// steps.
func atFactAtRest(t *testing.T, dialectName string) (*sql.DB, []string, [2]string) {
	t.Helper()
	db := freshDB(t, dialectName)
	names := namesOf(t, dialectName)
	steps := factAtRest[dialectName]
	if _, err := applyPending(context.Background(), db, migrationSources[dialectName], names, steps[0], gateStrict); err != nil {
		t.Fatalf("applying through %s: %v", steps[0], err)
	}
	seedPlaintextFact(t, db, placeholderFor(dialectName))
	return db, names, steps
}

// requireUntouched fails unless db still holds the plaintext fact, its schema
// is model, and its history is history rows long without the contract: what a
// refused contract must leave.
func requireUntouched(t *testing.T, dialectName string, db *sql.DB, model schemaModel, history int, contract string) {
	t.Helper()
	after := historyOf(t, db, dialectName)
	if len(after) != history || recorded(after, contract) {
		t.Errorf("the history holds %d rows (contract recorded: %t); want the %d it held, without %s", len(after), recorded(after, contract), history, contract)
	}
	if !reflect.DeepEqual(readModel(t, dialectName, db), model) {
		t.Error("the schema changed although the contract was refused")
	}
	var key string
	if err := db.QueryRow(`SELECT "key" FROM facts`).Scan(&key); err != nil || key != "package.zq9plainkey" {
		t.Errorf("the fact stored before the contract reads %q (%v); want it kept, untouched", key, err)
	}
}

// TestApply_AnOlderControllerRunningKeepsTheContractOut: a controller of the
// build before facts at rest, seen three seconds ago, and this build opening
// the database as the server does. The open is refused, naming the controller
// and its build, saying what to do, and leaving the history, the schema and
// the plaintext fact exactly as they were.
func TestApply_AnOlderControllerRunningKeepsTheContractOut(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		db, _, steps := atFactAtRest(t, dialectName)
		seedController(t, dialectName, db, "older-a", steps[0], 3)
		model, history := readModel(t, dialectName, db), len(historyOf(t, db, dialectName))

		_, err := ApplyWith(context.Background(), dialectName, db, Options{AllowNewerWithinWindow: true})
		if !errors.Is(err, ErrOlderControllersRunning) {
			t.Fatalf("ApplyWith() with an older controller running = %v; want ErrOlderControllersRunning", err)
		}
		for _, want := range []string{steps[1], `instance "older-a"`, `migrations through "` + steps[0] + `"`, "Nothing was applied.", "Stop every controller older than " + steps[1]} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the refusal does not say %q:\n%v", want, err)
			}
		}
		requireUntouched(t, dialectName, db, model, history, steps[1])
	})
}

// TestApply_AForeseenRefusalAppliesNothing: two migrations short of facts at
// rest, with a controller of that build running. The expand migration ahead
// of the contract is not applied either, because the refusal is foreseen
// before anything is.
func TestApply_AForeseenRefusalAppliesNothing(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db := freshDB(t, dialectName)
		names := namesOf(t, dialectName)
		at := slices.Index(names, factAtRest[dialectName][1])
		twoShort := names[at-2]
		if _, err := applyPending(ctx, db, migrationSources[dialectName], names, twoShort, gateStrict); err != nil {
			t.Fatalf("applying through %s: %v", twoShort, err)
		}
		seedController(t, dialectName, db, "older-b", twoShort, 3)

		_, err := ApplyWith(ctx, dialectName, db, Options{AllowNewerWithinWindow: true})
		if !errors.Is(err, ErrOlderControllersRunning) || !strings.Contains(err.Error(), "Nothing was applied.") {
			t.Fatalf("ApplyWith() = %v; want the refusal made before anything is applied", err)
		}
		if recorded(historyOf(t, db, dialectName), names[at-1]) {
			t.Errorf("%s was applied although the contract after it was going to be refused", names[at-1])
		}
	})
}

// TestApplyOne_TheContractsOwnTransactionDecides: the check inside the
// contract's transaction refuses on its own, with no check before it. Then,
// with the heartbeat table renamed away, the check before anything is applied
// passes the database (a pending migration could still create the table) and
// the one inside the transaction refuses it, since nothing can say who runs.
func TestApplyOne_TheContractsOwnTransactionDecides(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db, names, steps := atFactAtRest(t, dialectName)
		src := migrationSources[dialectName]
		script := readMigration(t, src, steps[1])
		seedController(t, dialectName, db, "older-c", steps[0], 3)
		model, history := readModel(t, dialectName, db), len(historyOf(t, db, dialectName))

		err := applyOne(ctx, db, src, steps[1], script, floorAfter(dialectName, names, steps[1]), guardFor(dialectName, names, steps[1]))
		if !errors.Is(err, ErrOlderControllersRunning) || !strings.Contains(err.Error(), steps[1]+" and every migration after it were not applied.") {
			t.Fatalf("applyOne() with an older controller running = %v; want the refusal from inside the transaction", err)
		}
		requireUntouched(t, dialectName, db, model, history, steps[1])

		exec(t, db, `ALTER TABLE controller_instances RENAME TO controller_instances_away`)
		model = readModel(t, dialectName, db)
		_, err = ApplyWith(ctx, dialectName, db, Options{AllowNewerWithinWindow: true})
		if !errors.Is(err, ErrOlderControllersRunning) || !strings.Contains(err.Error(), "no controller_instances table") {
			t.Fatalf("ApplyWith() with no heartbeat table = %v; want the contract refused", err)
		}
		requireUntouched(t, dialectName, db, model, history, steps[1])
	})
}

// TestApply_TheContractAppliesWhenNoOlderControllerRuns: with nobody
// recorded, with only controllers the floor allows (one at it, one newer),
// and with an older controller last seen past the liveness window, the
// contract applies, says it did, and deletes the plaintext fact.
func TestApply_TheContractAppliesWhenNoOlderControllerRuns(t *testing.T) {
	gone := int64(ControllerLiveWithin/time.Second) + 15
	cases := []struct {
		name string
		seed func(t *testing.T, dialectName string, db *sql.DB, names []string, steps [2]string)
	}{
		{"nobody recorded", func(*testing.T, string, *sql.DB, []string, [2]string) {}},
		{"only controllers the floor allows", func(t *testing.T, dialectName string, db *sql.DB, names []string, steps [2]string) {
			seedController(t, dialectName, db, "at-the-floor", steps[1], 3)
			seedController(t, dialectName, db, "newer", nextName(t, headOf(names), "from_a_newer_build"), 3)
		}},
		{"an older controller gone past the window", func(t *testing.T, dialectName string, db *sql.DB, _ []string, steps [2]string) {
			seedController(t, dialectName, db, "older-gone", steps[0], gone)
		}},
	}
	forEachDialect(t, func(t *testing.T, dialectName string) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				db, names, steps := atFactAtRest(t, dialectName)
				tc.seed(t, dialectName, db, names, steps)

				out, err := ApplyWith(context.Background(), dialectName, db, Options{AllowNewerWithinWindow: true})
				if err != nil {
					t.Fatalf("ApplyWith() = %v; want the contract applied", err)
				}
				if !slices.Contains(out.Applied, steps[1]) || !slices.Equal(out.Contracts, []string{steps[1]}) || out.AppliedFloor != floorAfter(dialectName, names, headOf(names)) {
					t.Errorf("outcome = %+v; want %s applied and reported as the one contract", out, steps[1])
				}
				if n := countRows(t, db, "facts"); n != 0 {
					t.Errorf("facts holds %d row(s) after the contract; want none", n)
				}
			})
		}
	})
}

// TestApply_TheFloorKeepsAnOlderBuildOut: once facts at rest is applied, a
// build ending just before it is refused at start, with the reason, as its
// ApplyWith would refuse it (its own migration set, the window allowed). A
// build at the floor starts, and goes on starting when a newer build's expand
// migration is recorded above it, while the older build is still refused.
func TestApply_TheFloorKeepsAnOlderBuildOut(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		ctx := context.Background()
		db := freshDB(t, dialectName)
		src := migrationSources[dialectName]
		names := namesOf(t, dialectName)
		steps := factAtRest[dialectName]
		if err := Apply(ctx, dialectName, db); err != nil {
			t.Fatalf("Apply(): %v", err)
		}
		older := names[:slices.Index(names, steps[1])]
		refusedOlder := func() {
			t.Helper()
			_, err := applyPending(ctx, db, src, older, "", gateWithinWindow)
			if err == nil || !strings.Contains(err.Error(), `needs a build that knows at least "`+steps[1]+`"`) {
				t.Errorf("a build ending at %s started against the sealed schema: %v; want it refused by the floor", steps[0], err)
			}
		}
		refusedOlder()

		out, err := applyPending(ctx, db, src, names, "", gateWithinWindow)
		if err != nil || len(out.Applied) != 0 {
			t.Fatalf("this build started again = %+v, %v; want it served with nothing to apply", out, err)
		}

		next := nextName(t, headOf(names), "from_a_newer_build")
		floor := floorAfter(dialectName, names, headOf(names))
		exec(t, db, src.insertVersion, next, time.Now().UTC(), floor)
		out, err = applyPending(ctx, db, src, names, "", gateWithinWindow)
		if err != nil || !slices.Equal(out.Newer, []string{next}) || out.Floor != floor {
			t.Errorf("with a newer expand migration recorded = %+v, %v; want it served within the window at floor %s", out, err, floor)
		}
		refusedOlder()
	})
}
