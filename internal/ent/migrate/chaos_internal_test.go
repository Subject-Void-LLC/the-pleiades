// The Toxiproxy gate for the one holder the apply loop cannot outwait on its
// own: a starter that claimed a migration and then vanished from the network
// without closing its connection.
//
// A starter that crashes releases its claim at once, because the database
// sees its connection close. One cut off by a partition does not: no FIN ever
// arrives, the server keeps its transaction open, and every other starter
// waits on its claim until TCP keepalive notices, which can take hours. That
// is FAILURE_PATTERNS.md #134's "one bad holder blocks everybody", and the
// prelude's idle_in_transaction_session_timeout is what ends it. This test
// makes the partition real and checks the other starter goes on to apply the
// migration itself.
package migrate

import (
	"context"
	"database/sql"
	"embed"
	"testing"
	"time"

	toxiproxyclient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	tctoxiproxy "github.com/testcontainers/testcontainers-go/modules/toxiproxy"
	"github.com/testcontainers/testcontainers-go/network"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// slowMigrations holds one migration that sleeps before it creates a table,
// so a connection can be cut while its transaction is open.
//
//go:embed testdata/slow
var slowMigrations embed.FS

// TestApply_APartitionedWinnerReleasesItsClaim cuts a winner's link in the
// middle of its migration and requires the waiting starter to take the
// migration over within the prelude's idle timeout.
func TestApply_APartitionedWinnerReleasesItsClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("starts postgres and toxiproxy containers and waits out a sixty-second timeout")
	}
	ctx := context.Background()

	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("creating the network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	pg, err := testpg.Run(ctx,
		testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("pleiades"),
		testpg.BasicWaitStrategies(),
		network.WithNetwork([]string{"postgres"}, nw),
	)
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	directDSN, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("reading the direct DSN: %v", err)
	}

	toxi, err := tctoxiproxy.Run(ctx,
		testsupport.ToxiproxyImage,
		tctoxiproxy.WithProxy("postgres", "postgres:5432"),
		network.WithNetwork([]string{"toxiproxy"}, nw),
	)
	if err != nil {
		t.Fatalf("starting toxiproxy: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(toxi) })
	host, port, err := toxi.ProxiedEndpoint(8666)
	if err != nil {
		t.Fatalf("reading the proxied endpoint: %v", err)
	}
	uri, err := toxi.URI(ctx)
	if err != nil {
		t.Fatalf("reading the toxiproxy control URI: %v", err)
	}
	proxies, err := toxiproxyclient.NewClient(uri).Proxies()
	if err != nil {
		t.Fatalf("listing proxies: %v", err)
	}
	proxy := proxies["postgres"]
	proxiedDSN := "postgres://pleiades:pleiades@" + host + ":" + port + "/pleiades?sslmode=disable"

	// The real PostgreSQL source, prelude and all, reading its migrations
	// from the slow set instead.
	src := migrationSources["postgres"]
	src.fsys, src.dir = slowMigrations, "testdata/slow"
	names, err := migrationNames(src)
	if err != nil {
		t.Fatalf("listing the slow migrations: %v", err)
	}
	direct := openDSN(t, "postgres", directDSN)
	if _, err := prepareHistory(ctx, direct, src); err != nil {
		t.Fatalf("preparing the history table: %v", err)
	}

	// The winner migrates through the proxy. Its context outlives the
	// test's own patience; it is the server that has to end it.
	winnerCtx, cancelWinner := context.WithCancel(ctx)
	t.Cleanup(cancelWinner)
	winnerDB := openDSN(t, "postgres", proxiedDSN)
	winner := make(chan error, 1)
	go func() {
		_, err := applyPending(winnerCtx, winnerDB, src, names, "", gateStrict)
		winner <- err
	}()

	// Cut the link while the winner is inside its migration: data in both
	// directions is dropped and the connection is left open, which is what
	// a partition looks like to the server.
	awaitQuery(t, direct, "%pg_sleep%", winner)
	for _, stream := range []string{"upstream", "downstream"} {
		if _, err := proxy.AddToxic("blackhole-"+stream, "timeout", stream, 1, toxiproxyclient.Attributes{"timeout": 0}); err != nil {
			t.Fatalf("partitioning the %s link: %v", stream, err)
		}
	}
	t.Cleanup(func() {
		_ = proxy.RemoveToxic("blackhole-upstream")
		_ = proxy.RemoveToxic("blackhole-downstream")
	})

	// The loser connects directly and waits on the winner's claim until the
	// server gives up on the silent winner.
	started := time.Now()
	loserCtx, cancelLoser := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelLoser()
	out, err := applyPending(loserCtx, direct, src, names, "", gateStrict)
	waited := time.Since(started)
	if err != nil {
		t.Fatalf("the waiting starter failed after %s: %v", waited, err)
	}
	if len(out.Applied) != 1 || out.Applied[0] != names[0] {
		t.Fatalf("the waiting starter's outcome = %+v; want it to apply %s itself, the partitioned winner's work having been rolled back", out, names[0])
	}
	if waited > 2*time.Minute {
		t.Errorf("the waiting starter waited %s for a partitioned winner; the idle timeout should have ended it in about a minute", waited)
	}
	t.Logf("a partitioned winner held its claim for %s before the server ended it", waited)
}

// awaitQuery waits until some session on the server is running a query that
// matches pattern (a LIKE pattern). winner carries the result of the session
// expected to run it: one that ends first never got there, and its error,
// not a timeout, is what the failure has to say (FAILURE_PATTERNS.md #293).
func awaitQuery(t *testing.T, db *sql.DB, pattern string, winner <-chan error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-winner:
			t.Fatalf("the winner ended before any session ran a query like %q (its error: %v)", pattern, err)
		default:
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND query LIKE $1 AND pid <> pg_backend_pid()`, pattern).Scan(&n); err != nil {
			t.Fatalf("reading pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no session ran a query like %q within 30s, and the winner is still running", pattern)
}
