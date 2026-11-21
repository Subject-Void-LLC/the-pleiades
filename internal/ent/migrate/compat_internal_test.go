// These tests keep the contract table honest as data: every declaration names
// a real migration, both dialects declare the same changes, the floor rises
// the way the gate assumes it does, and every new contract is refused by the
// guard while an older controller runs. Whether a migration's SHAPE agrees
// with its declaration is compat_shape_internal_test.go's question.
package migrate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestContracts_NameRealMigrations proves every declaration refers to a
// migration that exists in its own dialect, with a floor no later than
// itself and a reason written down.
func TestContracts_NameRealMigrations(t *testing.T) {
	for dialectName, declared := range contracts {
		src, ok := migrationSources[dialectName]
		if !ok {
			t.Errorf("contracts are declared for %q, which has no migration source", dialectName)
			continue
		}
		names, err := migrationNames(src)
		if err != nil {
			t.Fatal(err)
		}
		exists := map[string]bool{}
		for _, n := range names {
			exists[n] = true
		}
		for name, c := range declared {
			if !exists[name] {
				t.Errorf("%s: contract %q names no migration", dialectName, name)
			}
			if !exists[c.compatibleFrom] {
				t.Errorf("%s: contract %q has floor %q, which names no migration", dialectName, name, c.compatibleFrom)
			}
			if c.compatibleFrom > name {
				t.Errorf("%s: contract %q has floor %q, later than itself", dialectName, name, c.compatibleFrom)
			}
			if strings.TrimSpace(c.reason) == "" {
				t.Errorf("%s: contract %q gives no reason", dialectName, name)
			}
		}
	}
}

// TestContracts_AgreeAcrossDialects proves both dialects declare the same
// change as a contract. The files are numbered differently (SQLite's history
// is three longer), so they are paired by the name after the number; a
// migration that exists in only one dialect (SQLite's pre-PostgreSQL
// history) has nothing to agree with.
func TestContracts_AgreeAcrossDialects(t *testing.T) {
	suffixes := map[string]map[string]bool{}
	for dialectName, src := range migrationSources {
		names, err := migrationNames(src)
		if err != nil {
			t.Fatal(err)
		}
		suffixes[dialectName] = map[string]bool{}
		for _, n := range names {
			suffixes[dialectName][suffix(n)] = true
		}
	}
	for dialectName, declared := range contracts {
		for name := range declared {
			for other := range migrationSources {
				if other == dialectName || !suffixes[other][suffix(name)] {
					continue
				}
				if !declaresSuffix(contracts[other], suffix(name)) {
					t.Errorf("%s declares %s a contract, but %s's %s migration is not declared", dialectName, name, other, suffix(name))
				}
			}
		}
	}
}

// TestFloorAfter_OnlyRises proves the property the gate depends on when it
// takes the highest floor it can see: across every dialect's real migration
// set, the floor after each migration is never lower than the one before.
func TestFloorAfter_OnlyRises(t *testing.T) {
	for dialectName, src := range migrationSources {
		names, err := migrationNames(src)
		if err != nil {
			t.Fatal(err)
		}
		previous := ""
		for _, n := range names {
			floor := floorAfter(dialectName, names, n)
			if floor < previous {
				t.Errorf("%s: the floor after %s is %s, lower than the %s before it", dialectName, n, floor, previous)
			}
			if floor > n {
				t.Errorf("%s: the floor after %s is %s, a migration that has not happened yet", dialectName, n, floor)
			}
			previous = floor
		}
	}
	// And concretely, for the history as it stands: the first migration
	// before any contract, each contract's own floor after it.
	names := namesOf(t, "postgres")
	if got := floorAfter("postgres", names, "0027_add_job_external_checks.sql"); got != "0001_initial.sql" {
		t.Errorf("postgres floor before any contract = %s; want 0001_initial.sql", got)
	}
	if got := floorAfter("postgres", names, "0029_add_launchables.sql"); got != "0029_add_launchables.sql" {
		t.Errorf("postgres floor after the launchables contract = %s; want itself", got)
	}
	// The facts at rest contract: no build from before sealing serves after it.
	for dialectName, steps := range factAtRest {
		all := namesOf(t, dialectName)
		if got := floorAfter(dialectName, all, steps[0]); got >= steps[1] {
			t.Errorf("%s floor before facts at rest = %s; want one an unsealed build reaches", dialectName, got)
		}
		if got := floorAfter(dialectName, all, steps[1]); got != steps[1] {
			t.Errorf("%s floor after facts at rest = %s; want itself", dialectName, got)
		}
	}
}

// heartbeatTable names, by dialect, the migration that creates
// controller_instances: the table the contract guard reads.
var heartbeatTable = map[string]string{
	"sqlite3":  "0033_add_controller_instances.sql",
	"postgres": "0030_add_controller_instances.sql",
}

// TestEveryNewContractIsGuarded holds every declared contract to the guard
// (guard.go), against a real database of each dialect: with a controller
// whose build ends just below the contract's floor still running, applying
// through the contract is refused and leaves it unrecorded.
//
// It replaces a tripwire that failed on the first contract declared without
// the guard. That purpose stays: a contract the guard does not cover fails
// here. And grandfathered is held to what it means, a contract applied before
// the heartbeat table existed, so it cannot be used to excuse a new one.
func TestEveryNewContractIsGuarded(t *testing.T) {
	forEachDialect(t, func(t *testing.T, dialectName string) {
		names := namesOf(t, dialectName)
		table := heartbeatTable[dialectName]
		if !slices.Contains(names, table) {
			t.Fatalf("%s has no migration %s; the heartbeat table moved and this test needs its new name", dialectName, table)
		}
		checked := 0
		for name, c := range contracts[dialectName] {
			if c.grandfathered {
				if name >= table {
					t.Errorf("%s: %s is marked grandfathered but applies after %s, so the guard can see who is running when it applies; only a contract from before the heartbeat table may be grandfathered", dialectName, name, table)
				}
				continue
			}
			if name <= table {
				t.Errorf("%s: %s is a new contract that applies before %s, where no guard can see who is running", dialectName, name, table)
				continue
			}
			checked++
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				db := freshDB(t, dialectName)
				src := migrationSources[dialectName]
				at := slices.Index(names, name)
				if _, err := applyPending(ctx, db, src, names, names[at-1], gateStrict); err != nil {
					t.Fatalf("applying through %s: %v", names[at-1], err)
				}
				// The newest build the floor leaves out.
				justBelow := names[slices.Index(names, c.compatibleFrom)-1]
				seedController(t, dialectName, db, "guard-probe", justBelow, 5)

				_, err := applyPending(ctx, db, src, names, name, gateStrict)
				if !errors.Is(err, ErrOlderControllersRunning) {
					t.Fatalf("applying %s with a controller at %s running = %v; want the guard's refusal", name, justBelow, err)
				}
				if recorded(historyOf(t, db, dialectName), name) {
					t.Errorf("%s is recorded although the guard refused it", name)
				}
			})
		}
		if checked == 0 {
			t.Logf("%s declares no contract newer than the heartbeat table; nothing for the guard to refuse", dialectName)
		}
	})
}

// TestEverySourceNamesItsOwnDialect proves each migration source carries the
// dialect it is registered under, since that name is how its contract
// declarations are found.
func TestEverySourceNamesItsOwnDialect(t *testing.T) {
	for key, src := range migrationSources {
		if src.dialect != key {
			t.Errorf("the source registered as %q says it is %q", key, src.dialect)
		}
	}
}

// suffix returns a migration name without its number.
func suffix(name string) string {
	if i := strings.IndexByte(name, '_'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// declaresSuffix reports whether declared holds a contract whose name has the
// given suffix.
func declaresSuffix(declared map[string]contract, s string) bool {
	for name := range declared {
		if suffix(name) == s {
			return true
		}
	}
	return false
}
