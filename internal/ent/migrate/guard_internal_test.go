// The contract guard's own rules (guard.go): who counts as older, and on
// PostgreSQL the lock that keeps every heartbeat out while a contract's
// transaction is open. The guard on a real contract, through ApplyWith, is
// guard_fact_at_rest_internal_test.go's, and on every declared contract,
// compat_internal_test.go's TestEveryNewContractIsGuarded.
package migrate

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bindFor rewrites "?" placeholders as "$1", "$2", ... for PostgreSQL. Every
// query given to it is a constant of this package's tests.
func bindFor(dialectName, query string) string {
	if dialectName != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// insertController records a heartbeat the way a controller whose build ends
// at head does, last written secondsAgo seconds ago on the database's own
// clock. It returns its error, so a goroutine can call it.
func insertController(ctx context.Context, db *sql.DB, dialectName, id, head string, secondsAgo int64) error {
	clock := SQLiteClockSQL
	if dialectName == "postgres" {
		clock = PostgresClockSQL
	}
	query := bindFor(dialectName, `INSERT INTO controller_instances
		(created_at, updated_at, instance_id, version, migration_head, host, started_unix, last_seen_unix)
		VALUES (?, ?, ?, 'v-older', ?, 'host-older', `+clock+` - ?, `+clock+` - ?)`)
	now := time.Now().UTC()
	_, err := db.ExecContext(ctx, query, now, now, id, head, secondsAgo+60, secondsAgo)
	return err
}

// seedController is insertController, failing the test on error.
func seedController(t testing.TB, dialectName string, db *sql.DB, id, head string, secondsAgo int64) {
	t.Helper()
	if err := insertController(context.Background(), db, dialectName, id, head, secondsAgo); err != nil {
		t.Fatalf("recording controller %s's heartbeat: %v", id, err)
	}
}

// TestOlderThan is the rule on its own: below the floor, another lineage's
// migration within this build's numbering, and anything that is not a
// migration name are older; the floor itself, this build's later migrations
// and a newer build's are not.
func TestOlderThan(t *testing.T) {
	known := []string{"0001_a.sql", "0002_b.sql", "0003_c.sql"}
	for _, tc := range []struct {
		head  string
		older bool
	}{
		{"0001_a.sql", true},
		{"0002_b.sql", false},
		{"0003_c.sql", false},
		{"0003_other_lineage.sql", true},
		{"0004_from_a_newer_build.sql", false},
		{"", true},
		{"not a migration", true},
		{"0002_b.sql\x1b[31m", true},
	} {
		if got := olderThan(known, "0002_b.sql", tc.head); got != tc.older {
			t.Errorf("olderThan(floor 0002_b.sql, head %q) = %t, want %t", tc.head, got, tc.older)
		}
	}
}

// TestApplyOne_NoHeartbeatIsWrittenWhileAContractApplies is the race the
// lock closes on PostgreSQL. A contract whose script takes two seconds has
// passed its check, and an older controller records its first heartbeat
// while the script runs. The heartbeat must wait for the contract's commit,
// so that what the check saw is still the whole of who was running when the
// contract committed. SQLite needs no such test of this package: its claim
// already holds the one write lock, which no heartbeat gets past.
func TestApplyOne_NoHeartbeatIsWrittenWhileAContractApplies(t *testing.T) {
	if testing.Short() {
		t.Skip("starts postgres and holds a two-second migration")
	}
	ctx := context.Background()
	dsn := freshDSN(t, "postgres")
	db := openDSN(t, "postgres", dsn)
	if err := Apply(ctx, "postgres", db); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	src := migrationSources["postgres"]
	names := namesOf(t, "postgres")
	contract := nextName(t, headOf(names), "slow_contract")
	guard := &contractGuard{migration: contract, floor: contract, known: append(slices.Clone(names), contract)}

	applied := make(chan error, 1)
	go func() { applied <- applyOne(ctx, db, src, contract, "SELECT pg_sleep(2)", contract, guard) }()
	probe := openDSN(t, "postgres", dsn)
	awaitQuery(t, probe, "SELECT pg_sleep%", applied)

	beat := make(chan error, 1)
	other := openDSN(t, "postgres", dsn)
	go func() { beat <- insertController(ctx, other, "postgres", "older-late", headOf(names), 0) }()
	deadline := time.Now().Add(30 * time.Second)
	for waiting := 0; waiting == 0; {
		select {
		case err := <-beat:
			t.Fatalf("a heartbeat was written while the contract's transaction was open (%v); it must wait for the commit", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the heartbeat neither waited nor finished within 30s")
		}
		if err := probe.QueryRow(`SELECT count(*) FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE 'INSERT INTO controller_instances%'`).Scan(&waiting); err != nil {
			t.Fatalf("asking postgres who is waiting: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := <-applied; err != nil {
		t.Fatalf("the contract, checked before the heartbeat, = %v; want it applied", err)
	}
	if err := <-beat; err != nil {
		t.Fatalf("the heartbeat, once the contract committed: %v", err)
	}
	if !recorded(historyOf(t, db, "postgres"), contract) {
		t.Errorf("%s is not recorded", contract)
	}
}
