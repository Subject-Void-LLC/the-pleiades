// These tests keep the contract table honest as data: every declaration names
// a real migration, both dialects declare the same changes, the floor rises
// the way the gate assumes it does, and no new contract can be declared
// before the guard it needs exists. Whether a migration's SHAPE agrees with
// its declaration is compat_shape_internal_test.go's question.
package migrate

import (
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
}

// TestANewContractNeedsTheGuard is a tripwire, not a test of today's
// behavior. A contract declared from now on must be refused while an older
// controller that cannot serve it is still running, and the guard that would
// do that (reading controller_instances inside the contract's own
// transaction) is deliberately unbuilt: it cannot be tested against a real
// contract until one exists. So declaring the first new contract fails here,
// and the message says what to build alongside it.
func TestANewContractNeedsTheGuard(t *testing.T) {
	for dialectName, declared := range contracts {
		for name, c := range declared {
			if !c.grandfathered {
				t.Errorf("%s: %s is a new contract. Build the apply-time contract guard with it (refuse, never wait, while a live controller_instances row reports a head below %s), and test it against this real migration, before removing this check", dialectName, name, c.compatibleFrom)
			}
		}
	}
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
