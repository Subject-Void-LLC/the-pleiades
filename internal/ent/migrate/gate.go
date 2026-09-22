// This file holds the startup schema-version gate: the pure decision, made
// from nothing but this binary's own migration names and the rows a
// database's schema_migrations table holds, of whether this binary may use
// that database, and which migrations it still has to apply.
//
// It is kept free of any database access on purpose. The gate runs at every
// start, again after every migration attempt (the reload that makes
// concurrent starts safe, see apply.go), and again on every controller
// heartbeat, and a decision that only reads values can be fuzzed against a
// reference oracle, which is how the missing-prefix defect below was pinned.
package migrate

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
)

// versionPattern is the naming convention every migration file follows, and
// so the only shape a recorded version may take: a four-digit zero-padded
// number, an underscore, a lowercase descriptive suffix, and the .sql
// extension. Apply orders migrations by a plain lexicographic sort of these
// names, which is correct only because the number is zero padded to a fixed
// width.
//
// A recorded version that does not match is refused rather than compared,
// because the gate's error messages print recorded versions back to an
// operator, and a value that passed this pattern cannot carry a control
// character or a terminal escape.
var versionPattern = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// versionNumber returns the number a migration name starts with, or an error
// if the name does not follow versionPattern.
func versionNumber(name string) (int, error) {
	match := versionPattern.FindStringSubmatch(name)
	if match == nil {
		return 0, fmt.Errorf("migrate: %q is not a migration name (want NNNN_name.sql)", name)
	}
	// The pattern admits exactly four digits, so this cannot overflow or
	// fail; the error is checked anyway rather than assumed away.
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, fmt.Errorf("migrate: reading the number of %q: %w", name, err)
	}
	return n, nil
}

// appliedRow is one row of schema_migrations as read back from a database.
type appliedRow struct {
	// version is the migration's file name, exactly as recorded.
	version string

	// floor is the row's compatible_from value: the oldest migration whose
	// build can still serve the schema this migration left. It is NULL on
	// every row a build older than the floor column wrote, and on every row
	// read from a database that does not have the column yet.
	floor sql.NullString
}

// gateMode says how much of a database migrated beyond this binary the gate
// will accept.
type gateMode int

const (
	// gateStrict refuses any recorded migration this binary does not know.
	// It is the default, and it is what restore and every offline tool use:
	// they must never proceed against a schema they were not built for.
	gateStrict gateMode = iota

	// gateWithinWindow accepts recorded migrations this binary does not know
	// when every one of them was declared to leave a schema this binary can
	// still serve. It is what lets a controller from before an upgrade keep
	// serving, and be started again, inside the compatibility window.
	gateWithinWindow
)

// gateResult is what the gate decided about one database.
type gateResult struct {
	// pending is every migration this binary knows that the database has not
	// recorded, in the order they must be applied.
	pending []string

	// newer is every recorded migration this binary does not know, in
	// order. It is only ever non-empty in gateWithinWindow mode, and when it
	// is, pending is empty: a binary never applies anything to a database
	// that is ahead of it.
	newer []string

	// floor is the highest floor recorded on a newer row: the oldest
	// migration a build must know to serve this database. It is empty when
	// newer is.
	floor string
}

// checkGate decides whether a binary whose embedded migrations are known may
// use a database whose schema_migrations table holds applied.
//
// known must be sorted and well formed, which the embedded set is (the
// parity test enforces both). applied is taken exactly as read back,
// duplicates and all, because a duplicate is itself evidence of a tampered
// table and a set would hide it.
//
// Every refusal is fail closed: proceeding against a history this binary
// cannot explain would mean running against a schema shape it was never
// built or tested against.
func checkGate(known []string, applied []appliedRow, mode gateMode) (gateResult, error) {
	knownSet := make(map[string]bool, len(known))
	for _, name := range known {
		knownSet[name] = true
	}

	// Every recorded row must be a well-formed migration name, and no two
	// rows may claim the same number. Two rows with one number means two
	// different lineages were applied to one database (two branches that
	// each added their own 0033, say), and a string comparison would
	// happily accept either of them.
	byNumber := make(map[int]string, len(applied))
	appliedSet := make(map[string]bool, len(applied))
	var unknown []appliedRow
	for _, row := range applied {
		n, err := versionNumber(row.version)
		if err != nil {
			return gateResult{}, fmt.Errorf("migrate: database history holds a row that is not a migration name, refusing to start: %w", err)
		}
		if other, seen := byNumber[n]; seen {
			return gateResult{}, fmt.Errorf("migrate: database history records both %q and %q under one number; its history has diverged, refusing to start", other, row.version)
		}
		byNumber[n] = row.version
		appliedSet[row.version] = true
		if !knownSet[row.version] {
			unknown = append(unknown, row)
		}
	}

	// The migrations this binary knows that are recorded must be exactly a
	// prefix of its own ordered set: no migration may be recorded while an
	// earlier one is not. A hole means a migration was skipped or its record
	// was lost, and building forward from there would assume a schema that
	// was never created. This includes a missing FIRST migration, which an
	// earlier version of this check let through: it only started looking for
	// a gap after the first applied name it met, so {0002} alone passed and
	// 0001 would then have run on top of 0002 (FAILURE_PATTERNS.md #276).
	prefix := 0
	for prefix < len(known) && appliedSet[known[prefix]] {
		prefix++
	}
	for _, name := range known[prefix:] {
		if appliedSet[name] {
			missing := known[prefix]
			return gateResult{}, fmt.Errorf("migrate: migration %q is applied but the earlier %q is not; database history has a gap, refusing to start", name, missing)
		}
	}

	if len(unknown) == 0 {
		return gateResult{pending: known[prefix:]}, nil
	}
	return checkNewer(known, prefix, unknown, mode)
}

// checkNewer decides the case where the database records migrations this
// binary does not know: some other, newer build migrated it further.
//
// In gateStrict mode that is always a refusal. In gateWithinWindow mode it is
// accepted only when all of the following hold, each of which rules out a
// way a newer-looking history could still be one this binary cannot serve:
//
//   - This binary has nothing pending. A database that is missing one of this
//     binary's migrations while also holding migrations this binary does not
//     know was built from a different lineage.
//   - The unknown migrations run contiguously from this binary's newest one.
//     A gap there is a hole in the history of a build this binary cannot see
//     into, so it cannot tell what shape the gap left.
//   - Every unknown row carries a floor. A row without one was written by a
//     build that predates the floor column, which never promised anything.
//   - Every floor numbered at or below this binary's newest migration is one
//     this binary knows. A floor there that it does not know names another
//     lineage's migration under a number this binary uses for its own.
//   - The highest of those floors is a migration this binary knows.
//     Membership rather than a comparison of numbers is deliberate: it also
//     proves the floor names this binary's lineage and not a different
//     migration that happens to share a number. (The fuzzer found why the
//     rule above is needed as well: two floors sharing the highest number,
//     one of each lineage, left the answer depending on which row was read
//     first.)
func checkNewer(known []string, prefix int, unknown []appliedRow, mode gateMode) (gateResult, error) {
	sort.Slice(unknown, func(i, j int) bool { return unknown[i].version < unknown[j].version })
	first := unknown[0].version

	if mode == gateStrict {
		return gateResult{}, fmt.Errorf("migrate: database has migration %q applied, which this binary's embedded migrations do not recognize; refusing to start", first)
	}

	if prefix != len(known) {
		return gateResult{}, fmt.Errorf("migrate: database has migration %q applied, which this binary does not recognize, while this binary's own %q is not applied; the two histories have diverged, refusing to start", first, known[prefix])
	}

	// The numbers of the unknown rows must continue straight on from this
	// binary's newest migration.
	head := 0
	if len(known) > 0 {
		n, err := versionNumber(known[len(known)-1])
		if err != nil {
			return gateResult{}, err
		}
		head = n
	}
	next := head + 1
	for _, row := range unknown {
		// Every row was validated by checkGate, so this cannot fail.
		n, err := versionNumber(row.version)
		if err != nil {
			return gateResult{}, err
		}
		if n != next {
			return gateResult{}, fmt.Errorf("migrate: database has migration %q applied, but this binary's history ends before %04d; the gap is not one this binary can reason about, refusing to start", row.version, next)
		}
		next++
	}

	// The highest floor wins: a later migration can raise the floor but
	// never lower it, and reading the maximum rather than only the newest
	// row's value is correct whichever way a newer build chose to write it.
	knownSet := make(map[string]bool, len(known))
	for _, name := range known {
		knownSet[name] = true
	}
	floor, floorNumber := "", -1
	for _, row := range unknown {
		if !row.floor.Valid {
			return gateResult{}, fmt.Errorf("migrate: database has migration %q applied by a build that recorded no compatibility floor; refusing to start against a schema nothing says this binary can serve", row.version)
		}
		n, err := versionNumber(row.floor.String)
		if err != nil {
			return gateResult{}, fmt.Errorf("migrate: migration %q records a compatibility floor that is not a migration name, refusing to start: %w", row.version, err)
		}
		if n <= head && !knownSet[row.floor.String] {
			return gateResult{}, fmt.Errorf("migrate: migration %q records the compatibility floor %q, which this binary does not know although it numbers its own migrations that far; the histories have diverged, refusing to start", row.version, row.floor.String)
		}
		if n > floorNumber {
			floor, floorNumber = row.floor.String, n
		}
	}
	if !knownSet[floor] {
		return gateResult{}, fmt.Errorf("migrate: database was migrated to %q, which needs a build that knows at least %q; this binary ends at %q, refusing to start. Run a newer build, or restore the backup taken before the upgrade", unknown[len(unknown)-1].version, floor, headOf(known))
	}

	newer := make([]string, len(unknown))
	for i, row := range unknown {
		newer[i] = row.version
	}
	return gateResult{newer: newer, floor: floor}, nil
}

// headOf names the newest migration in known, for an error message, or says
// there is none. Only a malformed test input can have none, since every
// dialect embeds at least one migration.
func headOf(known []string) string {
	if len(known) == 0 {
		return "no migration"
	}
	return known[len(known)-1]
}
