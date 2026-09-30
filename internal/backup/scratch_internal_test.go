// The containment a restore rests on, tested from the inside: what the
// scratch role cannot do, and the settings a file leaves being cleared
// before anything reads the database.
package backup

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// loadedScratch starts a server, migrates its database, backs it up with
// pg_dump, and loads that backup into a scratch database as the scratch
// role, without clearing its settings.
func loadedScratch(t *testing.T) *scratch {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a postgres container")
	}
	for _, name := range []string{"pg_dump", "pg_restore"} {
		_, err := exec.LookPath(name)
		testsupport.Require(t, name, err == nil, name+" is not on PATH")
	}
	ctx := context.Background()
	pg, err := testpg.Run(ctx, testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"), testpg.WithUsername("pleiades"), testpg.WithPassword("password"),
		testsupport.PostgresReady())
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()

	dir := t.TempDir()
	if out, err := exec.Command("pg_dump", "--format=custom", "--file="+filepath.Join(dir, "a.dump"), "--dbname="+dsn).CombinedOutput(); err != nil { // #nosec G204 -- a test's own paths
		t.Fatalf("pg_dump: %v\n%s", err, out)
	}
	admin, err := parseTarget(dsn)
	if err != nil {
		t.Fatal(err)
	}
	s, err := newScratch(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	archives, err := openStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archives.root.Close() })
	if err := s.create(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "a.dump")) // #nosec G304 -- a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := (tools{}).restore(ctx, s.login(s.db), f); err != nil {
		t.Fatalf("restoring as the scratch role: %v", err)
	}
	return s
}

// asRole runs stmt on the scratch database as the scratch role, the rights
// every statement in a restored file runs with.
func asRole(t *testing.T, s *scratch, stmt string) error {
	t.Helper()
	db, err := sql.Open("postgres", s.login(s.db).dsn())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(context.Background(), stmt)
	return err
}

// TestScratchRole_CannotReachPastItsOwnDatabase proves the containment, not
// the absence of an exploit: the role every restored statement runs as is
// refused the built-ins that run programs, read server files, and make
// roles or databases.
func TestScratchRole_CannotReachPastItsOwnDatabase(t *testing.T) {
	s := loadedScratch(t)
	for _, stmt := range []string{
		`COPY (SELECT 1) TO PROGRAM 'true'`,
		`SELECT pg_read_file('/etc/hostname')`,
		`SELECT lo_import('/etc/hostname')`,
		`CREATE ROLE planted SUPERUSER`,
		`CREATE DATABASE planted`,
		`ALTER ROLE pleiades PASSWORD 'changed'`,
		`CREATE EXTENSION dblink`,
	} {
		err := asRole(t, s, stmt)
		if err == nil {
			t.Errorf("the scratch role ran %q", stmt)
			continue
		}
		if !strings.Contains(err.Error(), "permission denied") && !strings.Contains(err.Error(), "must be superuser") &&
			!strings.Contains(err.Error(), "must have") && !strings.Contains(err.Error(), "not available") {
			t.Errorf("%q failed for a reason other than rights: %v", stmt, err)
		}
	}
}

// TestScratch_ClearSettingsRemovesWhatTheFileLeft plants, as the scratch
// role, the settings a restored file could leave, and proves the census and
// the schema comparison run once they are cleared. Without clearing, the
// statement_timeout alone stops both.
func TestScratch_ClearSettingsRemovesWhatTheFileLeft(t *testing.T) {
	s := loadedScratch(t)
	for _, stmt := range []string{
		`ALTER DATABASE ` + quoteIdent(s.db) + ` SET statement_timeout = '1ms'`,
		`ALTER ROLE CURRENT_USER SET search_path = 'planted, public'`,
		`ALTER ROLE CURRENT_USER IN DATABASE ` + quoteIdent(s.db) + ` SET lock_timeout = '1ms'`,
	} {
		if err := asRole(t, s, stmt); err != nil {
			t.Fatalf("planting %q as the role: %v", stmt, err)
		}
	}
	if _, _, err := s.count(keySet{}); err == nil {
		t.Fatal("the census ran under a 1ms statement timeout; the plant did not take, so this test proves nothing")
	}

	if err := s.clearSettings(); err != nil {
		t.Fatalf("clearSettings() error = %v", err)
	}
	var left int
	if err := s.maint.QueryRowContext(context.Background(),
		`SELECT count(*) FROM pg_db_role_setting WHERE setdatabase = (SELECT oid FROM pg_database WHERE datname = $1) OR setrole = (SELECT oid FROM pg_roles WHERE rolname = $2)`,
		s.db, s.role).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("%d settings survived clearSettings", left)
	}
	if _, _, err := s.count(keySet{}); err != nil {
		t.Fatalf("the census after clearing: %v", err)
	}
	if err := s.compare(); err != nil {
		t.Fatalf("the schema comparison after clearing: %v", err)
	}
}
