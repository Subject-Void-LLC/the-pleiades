// Restoring a file somebody tampered with: each way a backup can carry code
// that runs later, refused before the live database is touched.
package backup_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
)

// TestRestore_RefusesACodeCarryingDefaultTheListingCannotSee is the case the
// schema comparison exists for. A column default is part of its table's
// definition, so a backup carrying one lists nothing but a TABLE and passes
// every check on the listing. The default then runs as whoever next inserts
// a row, and pg_read_file is one of the built-ins a superuser may call; the
// compose stack's controller is a superuser.
func TestRestore_RefusesACodeCarryingDefaultTheListingCannotSee(t *testing.T) {
	srv := startServer(t)
	key := testKey('d')
	srv.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))

	srv.exec(t, "pleiades", `ALTER TABLE users ALTER COLUMN email SET DEFAULT pg_read_file('/etc/hostname')`)
	file, _ := takeBackup(t, srv, d)
	srv.exec(t, "pleiades", `ALTER TABLE users ALTER COLUMN email DROP DEFAULT`)
	before := liveFingerprint(t, srv)

	_, out, err := restore(srv, d, file, nil)
	if err == nil || !strings.Contains(err.Error(), "schema is not the one this version makes") || !strings.Contains(err.Error(), "pg_read_file") {
		t.Fatalf("Restore() error = %v, want the schema refusal naming the default\n%s", err, out)
	}
	if liveFingerprint(t, srv) != before {
		t.Fatal("a refused restore changed the live database")
	}
	if entries, _ := os.ReadDir(d.backups); len(entries) != 1 {
		t.Fatalf("the backup directory holds %d entries; a refusal before the swap sets nothing aside", len(entries))
	}
	srv.assertNoLeftovers(t)
}

// TestTakeAndRestore_RefuseATriggerAndItsFunction covers the kinds the
// listing does show. Backing up a database someone added a trigger to is
// refused, since the file would be one no restore accepts, and a file pg_dump
// wrote of such a database is refused on restore before anything is loaded.
func TestTakeAndRestore_RefuseATriggerAndItsFunction(t *testing.T) {
	srv := startServer(t)
	key := testKey('t')
	srv.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))
	srv.exec(t, "pleiades", `CREATE FUNCTION planted() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$`)
	srv.exec(t, "pleiades", `CREATE TRIGGER planted BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION planted()`)

	_, err := backup.Take(context.Background(), backup.Options{DSN: srv.dsn, SetupDir: d.setup, BackupDir: d.backups}, &strings.Builder{})
	if !errors.Is(err, backup.ErrNotABackup) || !strings.Contains(err.Error(), "FUNCTION") {
		t.Fatalf("Take() error = %v, want the refusal naming the function", err)
	}
	if entries, _ := os.ReadDir(d.backups); len(entries) != 0 {
		t.Fatalf("a refused backup left %d files", len(entries))
	}

	// The same database, dumped by pg_dump directly, as a file handed to a
	// restore would be.
	crafted := "pleiades-20260101T000000Z-" + crypto.Fingerprint(key)[:8] + ".dump"
	dump := exec.Command("pg_dump", "--format=custom", "--file="+filepath.Join(d.backups, crafted), "--dbname="+srv.dsn) // #nosec G204 -- a test's own paths
	if out, err := dump.CombinedOutput(); err != nil {
		t.Fatalf("pg_dump: %v\n%s", err, out)
	}
	srv.exec(t, "pleiades", `DROP TRIGGER planted ON jobs`)
	srv.exec(t, "pleiades", `DROP FUNCTION planted()`)
	before := liveFingerprint(t, srv)

	_, _, err = restore(srv, d, crafted, nil)
	if !errors.Is(err, backup.ErrNotABackup) || !strings.Contains(err.Error(), "TRIGGER") {
		t.Fatalf("Restore() error = %v, want ErrNotABackup naming the trigger", err)
	}
	if liveFingerprint(t, srv) != before {
		t.Fatal("a refused restore changed the live database")
	}
	srv.assertNoLeftovers(t)
}

// TestRestore_RefusesWhatIsNotAnArchive covers the file itself: a symbolic
// link, a text file, and a file that does not exist.
func TestRestore_RefusesWhatIsNotAnArchive(t *testing.T) {
	srv := startServer(t)
	key := testKey('f')
	srv.seed(t, key, crypto.DefaultKeyVersion)
	d := newDirs(t, envKey(key))
	file, _ := takeBackup(t, srv, d)

	if err := os.Symlink(filepath.Join(d.backups, file), filepath.Join(d.backups, "link.dump")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.backups, "plain.dump"), []byte("-- PostgreSQL database dump\nDROP DATABASE pleiades;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"link.dump": "not a regular file", "plain.dump": "not a PostgreSQL custom-format archive",
		"missing.dump": "there is no", "../escape.dump": "",
	} {
		_, _, err := restore(srv, d, name, nil)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Restore(%q) error = %v, want %q", name, err, want)
		}
	}
	srv.assertNoLeftovers(t)
}
