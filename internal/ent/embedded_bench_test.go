package ent_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	_ "github.com/mattn/go-sqlite3"
)

// BenchmarkEmbeddedOpen measures cold-open latency: creating a brand new
// on-disk SQLite file, applying schema migration, and getting back a ready
// client. This is the path a Crawl-tier CLI runs on every process start, so
// its latency is directly user-visible (it happens before the first
// command can do anything).
//
// A credible existing reference for this specific operation (server
// process boot plus first-connection schema migration against Postgres)
// is not recorded anywhere in this repo, and fabricating one would violate
// this project's testing rules. That comparison is Walk-tier scope: it
// needs an actual Postgres instance (e.g. via testcontainers, the pattern
// internal/lock/nats_bench_test.go already uses for NATS) to measure
// honestly rather than guess. This benchmark logs only what was actually
// measured here.
func BenchmarkEmbeddedOpen(b *testing.B) {
	ctx := context.Background()

	b.Logf("[REFERENCE] no credible existing Postgres cold-open+migrate number is recorded in this repo; a real comparison needs a live Postgres instance and is Walk-tier scope, not fabricated here")

	for i := 0; i < b.N; i++ {
		// b.TempDir() returns a distinct, already-cleaned-up-on-test-end
		// directory on every call, so building the path inside the loop
		// guarantees each iteration is a genuine cold open against a file
		// ent has never seen, not a reopen of one already migrated.
		path := filepath.Join(b.TempDir(), "pleiades.db")

		client, err := ent.OpenEmbedded(ctx, path)
		if err != nil {
			b.Fatalf("OpenEmbedded failed: %v", err)
		}
		client.Close()
	}
}

// BenchmarkEmbeddedWrite measures single-row insert latency against an
// already-open embedded client, isolating steady-state write cost from the
// one-time cold-open cost BenchmarkEmbeddedOpen measures.
//
// Reference: this repo already records "Postgres raw INSERT latency is
// typically ~1-2ms" in internal/crypto/aes_bench_test.go, for the same
// shape of operation (a single row write), so that figure is reused here
// rather than inventing a new one.
func BenchmarkEmbeddedWrite(b *testing.B) {
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "embedded-write.db")

	client, err := ent.OpenEmbedded(ctx, path)
	if err != nil {
		b.Fatalf("OpenEmbedded failed: %v", err)
	}
	defer client.Close()

	b.Logf("[REFERENCE] Postgres raw INSERT latency is typically ~1-2ms")

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := client.Device.Create().
			SetName(fmt.Sprintf("bench-device-%d", i)).
			SetType("network_device").
			SetProperties(map[string]interface{}{"vendor": "arista", "role": "leaf"}).
			Save(ctx)
		if err != nil {
			b.Fatalf("failed to insert device: %v", err)
		}
	}
}
