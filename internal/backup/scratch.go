// The scratch database a backup is loaded into: creating it, loading the
// file as a role that can reach nothing else, and counting what it holds.
// golive.go takes it from there to the live database.
package backup

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
)

// scratch is one restore's scratch database, the role that owns it, and the
// superuser connection that creates and removes both.
//
// The role is the containment. It can log in, and it owns the scratch
// database and nothing else: no superuser, no CREATEDB, no CREATEROLE, and
// none of the built-in roles that read or write server files or run
// programs. pg_restore runs every statement in the file as this role, so a
// crafted file can do to the scratch database whatever it likes and to
// nothing else. Nothing with more rights touches the scratch database until
// its schema has been compared with a fresh one (see shape.go), because the
// objects a file can leave behind run as whoever next uses them.
type scratch struct {
	ctx   context.Context
	admin target  // the DB_DSN login, on the live database
	maint *sql.DB // that login on the maintenance database

	db, role, reference, replaced string
	password                      string
}

// newScratch connects to the maintenance database and removes whatever an
// interrupted restore left behind.
func newScratch(ctx context.Context, admin target) (*scratch, error) {
	secret, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	s := &scratch{
		ctx: ctx, admin: admin,
		db: admin.database + "_restore", role: admin.database + "_restore",
		reference: admin.database + "_restore_reference", replaced: admin.database + "_replaced",
		password: hex.EncodeToString(secret),
	}
	// internal/ent registers the postgres driver, and this package imports
	// internal/ent.
	s.maint, err = sql.Open("postgres", admin.on("postgres", admin.user, admin.password).dsn())
	if err == nil {
		err = s.maint.PingContext(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("backup: cannot reach the database server: %w", err)
	}
	if err := s.removeLeftovers(); err != nil {
		_ = s.maint.Close()
		return nil, err
	}
	return s, nil
}

// removeLeftovers drops the scratch and reference databases, the role, and
// a replaced database a crash after the swap left behind. The replaced one
// is safe to drop: the swap happens only after it was backed up.
func (s *scratch) removeLeftovers() error {
	for _, db := range []string{s.db, s.reference, s.replaced} {
		if _, err := s.maint.ExecContext(s.ctx, "DROP DATABASE IF EXISTS "+quoteIdent(db)+" WITH (FORCE)"); err != nil {
			return fmt.Errorf("backup: removing %s: %w", db, err)
		}
	}
	if _, err := s.maint.ExecContext(s.ctx, "DROP ROLE IF EXISTS "+quoteIdent(s.role)); err != nil {
		return fmt.Errorf("backup: removing the role %s: %w", s.role, err)
	}
	return nil
}

// close removes everything the restore made that is not live. After a
// swap the scratch database has the live name, so only the reference
// database and the replaced one are left to drop.
func (s *scratch) close() {
	_ = s.removeLeftovers()
	_ = s.maint.Close()
}

// refuseIfInUse refuses while anything but this command is connected to the
// live database: a controller still running would keep writing to the
// database being replaced, and those writes would be lost.
func (s *scratch) refuseIfInUse() error {
	var n int
	var who sql.NullString
	err := s.maint.QueryRowContext(s.ctx, `SELECT count(*), string_agg(DISTINCT coalesce(nullif(application_name, ''), 'a client with no name'), ', ')
		FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, s.admin.database).Scan(&n, &who)
	if err != nil {
		return fmt.Errorf("backup: checking what is connected to the database: %w", err)
	}
	if n > 0 {
		return fmt.Errorf("backup: nothing was changed: %d %s still connected to the database (%s). Stop the controller and the runner first; make restore does this for you",
			n, pick(n == 1, "session is", "sessions are"), printable(who.String))
	}
	return nil
}

// login is the scratch role on db, with extra settings for its session.
func (s *scratch) login(db string, params ...string) target {
	t := s.admin.on(db, s.role, s.password)
	t.params = cloneValues(t.params)
	for i := 0; i+1 < len(params); i += 2 {
		t.params.Set(params[i], params[i+1])
	}
	return t
}

// load creates the role and the scratch database it owns, restores the
// archive into it as that role, and clears the settings the file could have
// left.
func (s *scratch) load(archives *store, name string) error {
	if err := s.create(); err != nil {
		return err
	}
	f, err := archives.root.Open(name)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := (tools{}).restore(s.ctx, s.login(s.db), f); err != nil {
		return fmt.Errorf("backup: nothing was changed: the backup could not be loaded: %w", err)
	}
	return s.clearSettings()
}

// create makes the role and the scratch database it owns.
func (s *scratch) create() error {
	for _, stmt := range []string{
		// INHERIT, the default, and deliberately not NOINHERIT: the role is a
		// member of nothing but the implicit pg_database_owner of its own
		// database, and in PostgreSQL 15 that membership is what grants
		// CREATE on that database's public schema. NOINHERIT withholds it,
		// and the restore fails with "permission denied for schema public".
		"CREATE ROLE " + quoteIdent(s.role) + " LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE INHERIT NOREPLICATION NOBYPASSRLS PASSWORD '" + s.password + "'",
		"CREATE DATABASE " + quoteIdent(s.db) + " OWNER " + quoteIdent(s.role),
	} {
		if _, err := s.maint.ExecContext(s.ctx, stmt); err != nil {
			return fmt.Errorf("backup: preparing the scratch database: %w", err)
		}
	}
	return nil
}

// clearSettings removes, as the superuser, every setting a file restored as
// the role could have attached to the scratch database or to the role. Such
// a setting applies to every later session there, including the ones that
// check the database: a statement_timeout stops them, and a search_path
// could make them resolve a function the file created instead of the
// catalog's. Clearing a setting edits a catalog row and runs no code.
//
// The role can write settings in exactly three places, one statement each:
// the database's own, the role's own, and the role's inside the database.
// ALTER ROLE ALL IN DATABASE, which reads as though it covered the last,
// clears only the entry for all roles. So after clearing, what is left is
// counted, and anything left is a refusal rather than a hope.
func (s *scratch) clearSettings() error {
	for _, stmt := range []string{
		"ALTER DATABASE " + quoteIdent(s.db) + " RESET ALL",
		"ALTER ROLE " + quoteIdent(s.role) + " RESET ALL",
		"ALTER ROLE " + quoteIdent(s.role) + " IN DATABASE " + quoteIdent(s.db) + " RESET ALL",
	} {
		if _, err := s.maint.ExecContext(s.ctx, stmt); err != nil {
			return fmt.Errorf("backup: clearing the scratch database's settings: %w", err)
		}
	}
	var left int
	err := s.maint.QueryRowContext(s.ctx, `SELECT count(*) FROM pg_db_role_setting
		WHERE setdatabase = (SELECT oid FROM pg_database WHERE datname = $1)
		   OR setrole = (SELECT oid FROM pg_roles WHERE rolname = $2)`, s.db, s.role).Scan(&left)
	if err != nil {
		return fmt.Errorf("backup: checking the scratch database's settings: %w", err)
	}
	if left > 0 {
		return fmt.Errorf("backup: nothing was changed: %d settings the backup attached to the scratch database could not be cleared", left)
	}
	return nil
}

// count takes the census of the scratch database under keys, as the role,
// and reads the short fingerprints its key registry lists.
func (s *scratch) count(keys keySet) (crypto.Census, []string, error) {
	db, err := ent.OpenExisting(s.ctx, s.login(s.db, "search_path", "public").dsn())
	if err != nil {
		return crypto.Census{}, nil, fmt.Errorf("backup: opening the scratch database: %w", err)
	}
	defer func() { _ = db.Close() }()
	census, err := crypto.TakeCensus(s.ctx, db, keys.candidates())
	if err != nil {
		return crypto.Census{}, nil, fmt.Errorf("backup: nothing was changed: counting what the backup holds failed: %w", err)
	}
	rows, err := db.StoredValues(s.ctx, "encryption_keys", "fingerprint")
	if err != nil {
		return crypto.Census{}, nil, fmt.Errorf("backup: reading the backup's key registry: %w", err)
	}
	var shorts []string
	for _, r := range rows {
		shorts = append(shorts, keyregistry.Short(printable(r.Value)))
	}
	return census, shorts, nil
}
