// What a backup costs over pg_dump alone.
package backup_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// BenchmarkTake measures a whole backup against the thing it wraps: a plain
// `pg_dump --format=custom` of the same database, which is also what the AWX
// operator's backup runs. The difference is the census, the read-back
// through pg_restore --list, and the checks on it, and that difference is
// what the Take/pg_dump ratio logged at the end reports.
func BenchmarkTake(b *testing.B) {
	srv := startServer(b)
	key := testKey('b')
	srv.seed(b, key, crypto.DefaultKeyVersion)
	setupDir, backups := b.TempDir(), b.TempDir()
	if err := os.WriteFile(filepath.Join(setupDir, ".env"), []byte(envKey(key)+"\n"), 0o600); err != nil {
		b.Fatal(err)
	}

	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var takeTotal time.Duration
	b.Run("Take", func(b *testing.B) {
		started := time.Now()
		for i := 0; i < b.N; i++ {
			clock = clock.Add(time.Second)
			now := clock
			if _, err := backup.Take(context.Background(), backup.Options{
				DSN: srv.dsn, SetupDir: setupDir, BackupDir: backups, Now: func() time.Time { return now },
			}, &bytes.Buffer{}); err != nil {
				b.Fatalf("Take() error = %v", err)
			}
		}
		takeTotal = time.Since(started) / time.Duration(b.N)
	})

	var dumpTotal time.Duration
	b.Run("pg_dump", func(b *testing.B) {
		started := time.Now()
		for i := 0; i < b.N; i++ {
			out := filepath.Join(b.TempDir(), "raw.dump")
			if err := exec.Command("pg_dump", "--format=custom", "--file="+out, "--dbname="+srv.dsn).Run(); err != nil { // #nosec G204 -- a benchmark's own paths
				b.Fatalf("pg_dump: %v", err)
			}
		}
		dumpTotal = time.Since(started) / time.Duration(b.N)
	})
	if dumpTotal > 0 {
		b.Logf("Take %s per backup, pg_dump alone %s: %.2fx", takeTotal.Round(time.Millisecond), dumpTotal.Round(time.Millisecond),
			float64(takeTotal)/float64(dumpTotal))
	}
}
