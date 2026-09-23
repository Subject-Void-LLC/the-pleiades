package lock_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	_ "github.com/lib/pq"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// BenchmarkPostgresAdvisoryLock measures the latency of acquiring and
// releasing a Postgres session-level advisory lock (pg_try_advisory_lock /
// pg_advisory_unlock): the real, industry-standard "raw SQL database as a
// distributed lock" alternative this package's NATS JetStream KV approach
// is implicitly compared against. Measured locally against a real Postgres
// container, on the same host as BenchmarkLockAcquisition
// (nats_bench_test.go), not an unverifiable published figure for a
// different system: PATTERNS.md's own precedent for this distinction
// (Phase 2's event bus benchmarks compare against a bare core-NATS publish
// measured on the identical broker rather than a cited Redis figure).
func BenchmarkPostgresAdvisoryLock(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark in short mode")
	}
	ctx := context.Background()

	pgContainer, err := testpg.Run(ctx,
		testsupport.PostgresImage,
		testpg.WithDatabase("pleiades_bench"),
		testpg.WithUsername("pleiades"),
		testpg.WithPassword("password"),
		testsupport.PostgresReady(),
	)
	if err != nil {
		b.Fatalf("failed to start postgres container: %v", err)
	}
	b.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		b.Fatalf("failed to get pg connection string: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		b.Fatalf("failed to open postgres connection: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	// Advisory locks are session-scoped: pin one connection for the whole
	// benchmark so every acquire/release pair genuinely happens against
	// the same session, matching how a real caller would use this
	// primitive, rather than letting database/sql's pool hand out a
	// different underlying connection per query.
	conn, err := db.Conn(ctx)
	if err != nil {
		b.Fatalf("failed to pin a postgres connection: %v", err)
	}
	b.Cleanup(func() { _ = conn.Close() })

	const lockKey = int64(918273645)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var acquired bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&acquired); err != nil {
			b.Fatalf("failed to acquire advisory lock: %v", err)
		}
		if !acquired {
			b.Fatalf("expected to acquire the advisory lock; it should never already be held in this single-connection benchmark")
		}

		var released bool
		if err := conn.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", lockKey).Scan(&released); err != nil {
			b.Fatalf("failed to release advisory lock: %v", err)
		}
		if !released {
			b.Fatalf("expected to release the advisory lock that was just acquired")
		}
	}
}
