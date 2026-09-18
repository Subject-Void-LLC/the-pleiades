// Backup and restore against a real PostgreSQL server, with PostgreSQL's
// own client programs.
package backup_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// takeBackup runs Take and returns the backup's file name and output.
func takeBackup(t *testing.T, srv server, d dirs) (string, string) {
	t.Helper()
	var out bytes.Buffer
	name, err := backup.Take(context.Background(), backup.Options{
		DSN: srv.dsn, SetupDir: d.setup, BackupDir: d.backups,
	}, &out)
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	return name.String(), out.String()
}

// restore runs Restore of file from d.backups.
func restore(srv server, d dirs, file string, keys backup.KeySource) (backup.Restored, string, error) {
	var out bytes.Buffer
	r, err := backup.Restore(context.Background(), backup.RestoreOptions{
		Options:    backup.Options{DSN: srv.dsn, SetupDir: d.setup, BackupDir: d.backups},
		ArchiveDir: d.backups, Archive: file,
	}, keys, &out)
	return r, out.String(), err
}

// TestTakeAndRestore_BringsBackEverySealedColumnReadable is the round trip,
// judged by the controller's own read path: every encrypted column is read
// back through the real hooks under the key in .env, not counted.
func TestTakeAndRestore_BringsBackEverySealedColumnReadable(t *testing.T) {
	// Registered before the container's own cleanup, so it runs after it:
	// the container's goroutines are the test's, until it is terminated.
	ignore := goleak.IgnoreCurrent()
	t.Cleanup(func() { goleak.VerifyNone(t, ignore) })
	srv := startServer(t)
	key := testKey('r')
	srv.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))

	file, out := takeBackup(t, srv, d)
	info, err := os.Stat(filepath.Join(d.backups, file))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the backup is %v, %v; want a file at mode 0600", info, err)
	}
	if !strings.Contains(file, crypto.Fingerprint(key)[:8]) {
		t.Fatalf("the backup's name %q does not carry the key's fingerprint", file)
	}
	for _, want := range []string{"1 credential", "DOES NOT HOLD THE KEY"} {
		if !strings.Contains(out, want) {
			t.Errorf("Take said nothing about %q:\n%s", want, out)
		}
	}
	data, _ := os.ReadFile(filepath.Join(d.backups, file)) // #nosec G304 -- a path under t.TempDir()
	if bytes.Contains(data, []byte(crypto.EncodeKey(key))) || bytes.Contains(data, key) {
		t.Fatal("the backup holds the key")
	}
	for _, s := range secrets {
		if bytes.Contains(data, []byte(s)) {
			t.Fatalf("the backup holds the plaintext %q", s)
		}
	}

	// A change after the backup, which the restore must undo.
	ctx := context.Background()
	after := srv.open(t, key, crypto.DefaultKeyVersion)
	after.Organization.Create().SetName("made-after-the-backup").SaveX(ctx)
	_ = after.Close()

	result, out, err := restore(srv, d, file, nil)
	if err != nil {
		t.Fatalf("Restore() error = %v\n%s", err, out)
	}
	if result.FailedJobs != 1 || result.EndedSessions != 1 {
		t.Errorf("Restore() = %+v; want the one running job failed and the one session ended", result)
	}
	if result.SetAside == "" {
		t.Fatal("the replaced database was not set aside")
	}
	if _, err := os.Stat(result.SetAside); err != nil {
		t.Fatalf("the set-aside backup %s: %v", result.SetAside, err)
	}

	for column, ok := range srv.readBack(t, key, crypto.DefaultKeyVersion) {
		if !ok {
			t.Errorf("%s did not read back as seeded", column)
		}
	}
	if got := srv.scalar(t, "pleiades", `SELECT count(*)::text FROM organizations WHERE name = 'made-after-the-backup'`); got != "0" {
		t.Error("a change made after the backup survived the restore")
	}
	if got := srv.scalar(t, "pleiades", `SELECT state || ': ' || coalesce(failure_reason, '') FROM jobs WHERE job_id = 'job-in-flight'`); !strings.HasPrefix(got, "failed: restored from a backup") {
		t.Errorf("the job the backup caught running is %q", got)
	}
	if got := srv.scalar(t, "pleiades", `SELECT state::text FROM jobs WHERE job_id = 'job-finished'`); got != "completed" {
		t.Errorf("a finished job became %q", got)
	}
	if got := srv.scalar(t, "pleiades", `SELECT count(*)::text FROM sessions`); got != "0" {
		t.Errorf("%s sessions survived the restore", got)
	}
	if got := srv.scalar(t, "pleiades", `SELECT actor || ' ' || action || ' ' || object_kind || ' ' || object_name FROM activity_entries ORDER BY id DESC LIMIT 1`); got != "controller-restore restored database pleiades from "+file {
		t.Errorf("the newest activity entry is %q", got)
	}
	if got := srv.scalar(t, "pleiades", `SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'pleiades'`); got != "pleiades" {
		t.Errorf("the restored database belongs to %q, not the DB_DSN login", got)
	}
	srv.assertNoLeftovers(t)

	// The set-aside backup is itself restorable: it holds the change made
	// after the first backup, so restoring it puts that back. It starts
	// within the same second as the first restore on purpose: its own
	// set-aside then finds its name taken and moves to the next second,
	// rather than overwriting the file being restored.
	if _, out, err := restore(srv, dirs{setup: d.setup, backups: d.backups}, filepath.Base(result.SetAside), nil); err != nil {
		t.Fatalf("restoring the set-aside backup: %v\n%s", err, out)
	}
	if got := srv.scalar(t, "pleiades", `SELECT count(*)::text FROM organizations WHERE name = 'made-after-the-backup'`); got != "1" {
		t.Error("restoring the set-aside backup did not bring back what the first restore replaced")
	}
	srv.assertNoLeftovers(t)
}

// liveFingerprint is a cheap proof the live database was not touched: the
// same rows, and the same database, since a rename would change its oid.
func liveFingerprint(t *testing.T, srv server) string {
	t.Helper()
	return srv.scalar(t, "pleiades", `SELECT (SELECT oid FROM pg_database WHERE datname = 'pleiades')::text || '/' ||
		(SELECT count(*) FROM organizations)::text || '/' || (SELECT count(*) FROM sessions)::text`)
}

// TestRestore_RefusalsChangeNothing covers each refusal a real backup can
// meet, and checks after each that the live database is the same one, with
// the same rows, and that nothing was left behind.
func TestRestore_RefusalsChangeNothing(t *testing.T) {
	srv := startServer(t)
	key := testKey('k')
	srv.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))
	file, _ := takeBackup(t, srv, d)
	before := liveFingerprint(t, srv)

	cases := []struct {
		name string
		env  []string
		want string
	}{
		{"another key in .env", []string{envKey(testKey('x'))}, "under no key in .env"},
		{"the right key under the wrong tag", []string{envKey(key), "MASTER_ENCRYPTION_KEY_VERSION=v2"}, "the version tag v1, while .env gives that key the tag v2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			other := newDirs(t, c.env...)
			other.backups = d.backups
			_, out, err := restore(srv, other, file, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "nothing was changed") {
				t.Fatalf("Restore() error = %v, want a refusal saying %q\n%s", err, c.want, out)
			}
			if liveFingerprint(t, srv) != before {
				t.Fatal("a refused restore changed the live database")
			}
			srv.assertNoLeftovers(t)
		})
	}

	t.Run("a session still connected", func(t *testing.T) {
		conn, err := sql.Open("postgres", srv.dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if err := conn.PingContext(context.Background()); err != nil {
			t.Fatal(err)
		}
		_, _, err = restore(srv, d, file, nil)
		if err == nil || !strings.Contains(err.Error(), "still connected") {
			t.Fatalf("Restore() error = %v, want a refusal while the database is in use", err)
		}
	})
	if liveFingerprint(t, srv) != before {
		t.Fatal("a refused restore changed the live database")
	}

	t.Run("no key anywhere", func(t *testing.T) {
		empty := newDirs(t)
		empty.backups = d.backups
		if _, _, err := restore(srv, empty, file, nil); !errors.Is(err, backup.ErrNoKeySource) {
			t.Fatalf("Restore() error = %v, want ErrNoKeySource", err)
		}
	})

	t.Run("a key the backup's name does not give", func(t *testing.T) {
		empty := newDirs(t)
		empty.backups = d.backups
		_, _, err := restore(srv, empty, file, func(string) ([]byte, error) { return testKey('w'), nil })
		if err == nil || !strings.Contains(err.Error(), "the backup's name says it was taken under") {
			t.Fatalf("Restore() error = %v, want the early refusal by name", err)
		}
		if _, statErr := os.Stat(filepath.Join(empty.setup, ".env")); statErr == nil {
			t.Fatal("a refused restore wrote .env")
		}
	})
	if liveFingerprint(t, srv) != before {
		t.Fatal("a refused restore changed the live database")
	}
	srv.assertNoLeftovers(t)
}

// TestRestore_OnACleanMachineWritesTheKeyItWasGiven covers the restore a
// lost machine needs: no .env, the key typed in, and the version tag read
// off the values it opens rather than assumed.
func TestRestore_OnACleanMachineWritesTheKeyItWasGiven(t *testing.T) {
	srv := startServer(t)
	key := testKey('c')
	srv.seed(t, key, "v5")
	d := newDirs(t, envKey(key), "MASTER_ENCRYPTION_KEY_VERSION=v5")
	file, _ := takeBackup(t, srv, d)

	clean := newDirs(t)
	clean.backups = d.backups
	var asked string
	result, out, err := restore(srv, clean, file, func(label string) ([]byte, error) {
		asked = label
		return key, nil
	})
	if err != nil {
		t.Fatalf("Restore() error = %v\n%s", err, out)
	}
	if asked == "" || !strings.Contains(file, strings.ReplaceAll(asked, "-", "")) {
		t.Errorf("the key was asked for with the label %q; the backup's name %q gives it", asked, file)
	}
	if result.KeyFile == "" {
		t.Fatal("the restore did not report writing .env")
	}
	env, err := os.ReadFile(filepath.Join(clean.setup, ".env")) // #nosec G304 -- a path under t.TempDir()
	if err != nil {
		t.Fatalf("reading the written .env: %v", err)
	}
	for _, want := range []string{envKey(key), "MASTER_ENCRYPTION_KEY_VERSION=v5"} {
		if !strings.Contains(string(env), want+"\n") {
			t.Errorf(".env lacks %q", strings.SplitN(want, "=", 2)[0])
		}
	}
	for column, ok := range srv.readBack(t, key, "v5") {
		if !ok {
			t.Errorf("%s did not read back under the key and tag the restore wrote", column)
		}
	}
	srv.assertNoLeftovers(t)
}

// TestRestore_RefusesABackupFromANewerVersion covers a backup whose history
// holds a migration this version does not have.
func TestRestore_RefusesABackupFromANewerVersion(t *testing.T) {
	srv := startServer(t)
	key := testKey('n')
	srv.seed(t, key, crypto.DefaultKeyVersion)
	srv.exec(t, "pleiades", `INSERT INTO schema_migrations (version, applied_at) VALUES ('9999_from_the_future.sql', now())`)
	d := newDirs(t, envKey(key))
	file, _ := takeBackup(t, srv, d)
	srv.exec(t, "pleiades", `DELETE FROM schema_migrations WHERE version = '9999_from_the_future.sql'`)
	before := liveFingerprint(t, srv)

	_, _, err := restore(srv, d, file, nil)
	if err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("Restore() error = %v, want the newer-version refusal", err)
	}
	if liveFingerprint(t, srv) != before {
		t.Fatal("a refused restore changed the live database")
	}
	srv.assertNoLeftovers(t)
}

// TestTake_RefusesADatabaseNoControllerHasOpened covers backing up before
// the first start: there is nothing to back up, and no file is written.
func TestTake_RefusesADatabaseNoControllerHasOpened(t *testing.T) {
	srv := startServer(t)
	d := newDirs(t, envKey(testKey('e')))
	_, err := backup.Take(context.Background(), backup.Options{DSN: srv.dsn, SetupDir: d.setup, BackupDir: d.backups}, &bytes.Buffer{})
	if !errors.Is(err, backup.ErrNothingToBackUp) {
		t.Fatalf("Take() error = %v, want ErrNothingToBackUp", err)
	}
	if entries, _ := os.ReadDir(d.backups); len(entries) != 0 {
		t.Fatalf("the backup directory holds %d entries after a refused backup", len(entries))
	}
}

// TestTake_TwoBackupsInOneSecondKeepBoth proves a second backup started in
// the same second as the first takes the next second's name rather than
// failing or replacing the first. The clock is fixed, so the collision is
// certain rather than a matter of timing.
func TestTake_TwoBackupsInOneSecondKeepBoth(t *testing.T) {
	srv := startServer(t)
	key := testKey('s')
	srv.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))
	fixed := time.Now().UTC()
	opts := backup.Options{DSN: srv.dsn, SetupDir: d.setup, BackupDir: d.backups, Now: func() time.Time { return fixed }}

	first, err := backup.Take(context.Background(), opts, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("first Take() error = %v", err)
	}
	second, err := backup.Take(context.Background(), opts, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("second Take() error = %v", err)
	}
	if first.String() == second.String() || !second.Taken.Equal(first.Taken.Add(time.Second)) {
		t.Fatalf("names %s and %s; want the second one second later", first, second)
	}
	if entries, _ := os.ReadDir(d.backups); len(entries) != 2 {
		t.Fatalf("the directory holds %d entries, want both backups and nothing else", len(entries))
	}
}
