// Tests that a PostgreSQL role's own timeouts cannot end the one wait a
// migration's claim must never give up: a second starter waiting on the first
// one's uncommitted version row (FAILURE_PATTERNS.md #288).
//
// A managed PostgreSQL role often carries a statement or lock timeout, which
// is a sensible limit for application queries. The claim used to run under
// whatever the session carried, so on such a server a second controller gave
// up its wait, failed to start, and crash-looped until the first committed.
package migrate

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestApply_ARoleTimeoutDoesNotEndTheClaimWait gives one database the
// defaults such a role carries, a one second statement timeout and lock
// timeout, and has a second starter wait on a winner whose migration takes
// four seconds. The second starter must wait it out and find the work done,
// not fail on the role's timeout. The control proves the defaults are real:
// an ordinary statement on the same database is cut off by them.
func TestApply_ARoleTimeoutDoesNotEndTheClaimWait(t *testing.T) {
	if testing.Short() {
		t.Skip("starts postgres and waits on a four-second migration")
	}
	ctx := context.Background()
	dsn := freshDSN(t, "postgres")
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parsing the test database's DSN: %v", err)
	}
	// The name comes from freshDSN's own counter, never from input.
	name := strings.TrimPrefix(parsed.Path, "/")
	admin := openDSN(t, "postgres", requireSharedPostgres(t))
	for _, setting := range []string{"statement_timeout = '1s'", "lock_timeout = '1s'"} {
		if _, err := admin.ExecContext(ctx, "ALTER DATABASE "+name+" SET "+setting); err != nil {
			t.Fatalf("setting %s on the test database: %v", setting, err)
		}
	}

	// Control: a new session gets the defaults, and they do cut a statement
	// off. Without this, a server that ignored them would pass the test.
	probe := openDSN(t, "postgres", dsn)
	if _, err := probe.ExecContext(ctx, "SELECT pg_sleep(2)"); err == nil {
		t.Fatal("a two-second statement ran to the end under a one-second database statement_timeout; the defaults this test depends on are not in effect")
	}

	// The real PostgreSQL source, both preludes and all, reading the one
	// slow migration instead of the real set.
	src := migrationSources["postgres"]
	src.fsys, src.dir = slowMigrations, "testdata/slow"
	names, err := migrationNames(src)
	if err != nil {
		t.Fatalf("listing the slow migrations: %v", err)
	}
	db := openDSN(t, "postgres", dsn)
	if _, err := prepareHistory(ctx, db, src); err != nil {
		t.Fatalf("preparing the history table: %v", err)
	}

	winner := make(chan error, 1)
	winnerDB := openDSN(t, "postgres", dsn)
	go func() {
		_, err := applyPending(ctx, winnerDB, src, names, "", gateStrict)
		winner <- err
	}()
	awaitQuery(t, probe, "%pg_sleep%", winner)

	started := time.Now()
	out, err := applyPending(ctx, db, src, names, "", gateStrict)
	waited := time.Since(started)
	if err != nil {
		t.Fatalf("the waiting starter failed after %s: %v; a role's timeout ended the claim wait", waited, err)
	}
	if err := <-winner; err != nil {
		t.Fatalf("the winner failed: %v", err)
	}
	if len(out.Applied) != 0 || len(out.AppliedElsewhere) != 1 || out.AppliedElsewhere[0] != names[0] {
		t.Errorf("the waiting starter's outcome = %+v; want it to find %s applied by the winner", out, names[0])
	}
	if waited < time.Second {
		t.Errorf("the waiting starter returned after %s, inside the one-second role timeout, so it never waited on the claim at all", waited)
	}
	var marker int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_name = 'slow_migration_marker'").Scan(&marker); err != nil || marker != 1 {
		t.Errorf("the slow migration's table exists %d times (%v); want once", marker, err)
	}
}
