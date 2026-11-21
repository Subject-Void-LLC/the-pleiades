// This file is the apply-time contract guard: a contract migration (compat.go)
// is applied only while no controller of a build older than its floor is
// running against the database, and the starter refuses rather than waits.
//
// # Why
//
// A contract leaves a schema the build before it cannot serve. The floor it
// records makes such a build refuse the database at its next start and stop at
// its next heartbeat (gate.go), but one that is already running keeps serving
// until that heartbeat, and in that time it writes what the contract exists to
// stop: for 0052_fact_at_rest.sql, a fact stored in plaintext. So the starter
// that would apply a contract first asks who is running. While an older build
// is, it applies nothing and exits with an error naming each one, and the
// operator stops them and starts it again. It never waits for them, since an
// older build has no reason to stop on its own.
//
// # Live and older
//
// A controller is running when its controller_instances row was written within
// ControllerLiveWithin, on the database's own clock: the rule the fleet's sync
// recovery and controller migrate --plan already use. One that shuts down
// cleanly removes its row at once; one that crashed stops counting
// ControllerLiveWithin after its last heartbeat, so its row delays an upgrade by
// at most that long and never blocks one for good. A running controller is
// older when the migration head its row reports is below the contract's floor,
// or is a name this build cannot place at or above it: another lineage's
// migration under a number this build uses, or no migration name at all. A head
// past this build's own is a newer build, which is not older.
//
// # The race
//
// The check that decides runs inside the contract's own transaction, after its
// claim (transaction.go), and from that check to the commit no heartbeat can be
// written. On SQLite the claim took the database's one write lock. On
// PostgreSQL the guard takes a SHARE lock on controller_instances before it
// reads: that waits for any heartbeat already being written, so the read sees
// it, and holds every later one until the transaction ends. Two starters of
// this build are kept apart by the claim itself: only the one holding it
// checks. Before applying anything at all, the same check is made once outside
// any transaction, so a refusal that can be foreseen leaves even the expand
// migrations ahead of the contract unapplied.
//
// # What it cannot see
//
// A controller is visible here only through its heartbeat, and a build that
// already shipped cannot be changed to announce itself sooner. Two cases stay
// open, and are said rather than hidden:
//
//   - An older controller starting at the moment of the upgrade. Its own gate
//     reads the history before the contract commits and finds nothing to
//     refuse; its first heartbeat then waits for the contract's transaction
//     (the locks above) and lands after it, so this check never saw it. It
//     serves until its first heartbeat tick, ControllerHeartbeatInterval later,
//     reads the new floor and stops it. The window is any older start whose
//     gate read falls inside the contract's transaction, or just before it.
//   - An older controller that looks gone and is not: cut off from the
//     database, paused, or failing its heartbeats for longer than
//     ControllerLiveWithin. The guard counts it gone, the contract applies, and
//     when it reaches the database again it serves until its next tick.
//
// What either writes in that window is at rest. For 0052, that is a plaintext
// fact, which this build's fact store then refuses for that device
// (internal/facts/entstore's errPlaintextRows). Closing these needs the
// database itself to refuse an older build's writes; no check made here can.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ControllerHeartbeatInterval is how often a running controller records its
// heartbeat in controller_instances (cmd/controller's fleet.go). It is defined
// here, beside the liveness rule and the guard that reads it, because the
// guard is right only while a running controller beats more often than that
// rule allows it to miss.
const ControllerHeartbeatInterval = 15 * time.Second

// ControllerLiveWithin is how recently a controller's heartbeat must have been
// written for it to count as running: three missed heartbeats, so one slow
// round trip does not make a live controller look gone.
const ControllerLiveWithin = 3 * ControllerHeartbeatInterval

// PostgresClockSQL and SQLiteClockSQL are the database server's clock in whole
// Unix seconds, as a SQL expression in each dialect. Every heartbeat is
// stamped with it and every liveness read compares against it (internal/ent's
// instances.go), so two controllers whose clocks disagree cannot make each
// other look gone. On PostgreSQL, now() is when the current transaction began,
// which inside a contract's transaction counts a row seen since then as live:
// the cautious reading.
const (
	PostgresClockSQL = `CAST(EXTRACT(EPOCH FROM now()) AS BIGINT)`
	SQLiteClockSQL   = `CAST(strftime('%s', 'now') AS INTEGER)`
)

// ErrOlderControllersRunning marks a contract the guard refused to apply
// because a controller of a build older than its floor is still running.
var ErrOlderControllersRunning = errors.New("migrate: a controller of an older build is still running against this database")

// maxNamed bounds how many controllers one refusal names.
const maxNamed = 10

// contractGuard is the check one contract migration is applied under.
type contractGuard struct {
	// migration is the contract's file name, and floor its compatibleFrom.
	migration, floor string

	// grandfathered is the declaration's flag: such a contract applies
	// before controller_instances exists, so there is nothing to check.
	grandfathered bool

	// known is this build's whole migration set, in order.
	known []string
}

// guardFor returns the guard the named migration is applied under, or nil when
// it is not a declared contract.
func guardFor(dialectName string, names []string, name string) *contractGuard {
	c, ok := contracts[dialectName][name]
	if !ok {
		return nil
	}
	return &contractGuard{migration: name, floor: c.compatibleFrom, grandfathered: c.grandfathered, known: names}
}

// strictestGuard returns the guard of the contract with the highest floor among
// pending, up to and including stopAfter, or nil when none is a contract. A
// controller that contract allows, every earlier one allows too, because the
// floor only rises (TestFloorAfter_OnlyRises).
func strictestGuard(dialectName string, names, pending []string, stopAfter string) *contractGuard {
	var strictest *contractGuard
	for _, name := range pending {
		if g := guardFor(dialectName, names, name); g != nil && (strictest == nil || g.floor > strictest.floor) {
			strictest = g
		}
		if name == stopAfter {
			break
		}
	}
	return strictest
}

// querier is what the guard reads through: the contract's own transaction, or
// the pool before anything is applied.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// check refuses, with ErrOlderControllersRunning, while a controller older than
// g's floor is running against the database q reads.
//
// inTransaction is true for the check that decides, made inside the contract's
// own transaction: it takes the lock described in this file's header, and it
// refuses a database that has no heartbeat table where one must exist. The
// check made before anything is applied passes such a database instead, since
// a pending migration may still create the table. notApplied is the sentence
// the refusal uses to say what was left unapplied.
func (g *contractGuard) check(ctx context.Context, q querier, src migrationSource, inTransaction bool, notApplied string) error {
	var tables int
	if err := q.QueryRowContext(ctx, src.controllerTableCount).Scan(&tables); err != nil {
		return fmt.Errorf("migrate: looking for controller_instances before applying %s: %w", g.migration, err)
	}
	if tables == 0 {
		// Only a grandfathered contract is applied before the table's own
		// migration, and no build that records a heartbeat can have served a
		// database that far back. For any other, a missing table means one
		// was removed, and nothing can say who is running.
		if !inTransaction || g.grandfathered {
			return nil
		}
		return fmt.Errorf("%w: %s is a contract, and this database has no controller_instances table to tell whether a controller older than %s is running; refusing to apply it", ErrOlderControllersRunning, g.migration, g.floor)
	}
	if inTransaction && src.lockControllers != "" {
		if _, err := q.ExecContext(ctx, src.lockControllers); err != nil {
			return fmt.Errorf("migrate: holding controller heartbeats while %s is applied: %w", g.migration, err)
		}
	}
	live, err := readLiveControllers(ctx, q, src)
	if err != nil {
		return fmt.Errorf("migrate: reading which controllers are running before applying %s: %w", g.migration, err)
	}
	var older []liveController
	for _, c := range live {
		if olderThan(g.known, g.floor, c.head) {
			older = append(older, c)
		}
	}
	if len(older) == 0 {
		return nil
	}
	return g.refusal(older, notApplied)
}

// liveController is one controller_instances row written within
// ControllerLiveWithin.
type liveController struct {
	instanceID, version, head, host string
	secondsSinceSeen                int64
}

// readLiveControllers lists every controller seen within ControllerLiveWithin,
// oldest start first.
func readLiveControllers(ctx context.Context, q querier, src migrationSource) ([]liveController, error) {
	rows, err := q.QueryContext(ctx, src.liveControllers, int64(ControllerLiveWithin/time.Second))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var live []liveController
	for rows.Next() {
		var c liveController
		if err := rows.Scan(&c.instanceID, &c.version, &c.head, &c.host, &c.secondsSinceSeen); err != nil {
			return nil, err
		}
		live = append(live, c)
	}
	return live, rows.Err()
}

// olderThan reports whether a controller whose build ends at head cannot serve
// a database whose floor is floor, as far as a build knowing known can tell.
func olderThan(known []string, floor, head string) bool {
	number, err := versionNumber(head)
	if err != nil {
		// Not a migration name at all: nothing shows it knows the floor.
		return true
	}
	floorNumber, err := versionNumber(floor)
	if err != nil || number < floorNumber {
		return true
	}
	ownNumber, err := versionNumber(headOf(known))
	if err == nil && number > ownNumber {
		// A newer build, which knows everything this one does.
		return false
	}
	// At or above the floor and within this build's own numbering: it must
	// be this build's own migration, or it is another lineage's.
	return !slices.Contains(known, head)
}

// refusal is the error naming every older controller, and what to do.
func (g *contractGuard) refusal(older []liveController, notApplied string) error {
	var named strings.Builder
	for i, c := range older {
		if i == maxNamed {
			fmt.Fprintf(&named, "; and %d more", len(older)-maxNamed)
			break
		}
		if i > 0 {
			named.WriteString("; ")
		}
		// Every value comes from a table any database user can write, so each
		// is quoted: %q escapes whatever a terminal would act on.
		fmt.Fprintf(&named, "instance %q (version %q, migrations through %q, on host %q, last seen %ds ago)",
			c.instanceID, c.version, c.head, c.host, max(c.secondsSinceSeen, 0))
	}
	return fmt.Errorf("%w: %s is a contract that no build before %s can serve, and %d controller(s) of such a build are running: %s. %s Stop every controller older than %s, then start this build again; a controller that stopped without shutting down cleanly stops counting %s after its last heartbeat",
		ErrOlderControllersRunning, g.migration, g.floor, len(older), named.String(), notApplied, g.floor, ControllerLiveWithin)
}
