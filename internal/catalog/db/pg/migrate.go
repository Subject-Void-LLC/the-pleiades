// db.pg.migrate: a directory of numbered SQL files applied in order, each
// recorded in a version table.
package pg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sqllint"
)

// migrate's own parameters and returns.
const (
	paramDir             = "dir"
	paramTable           = "table"
	paramRole            = "role"
	paramAdditiveOnly    = "additive_only"
	paramAllowOutOfOrder = "allow_out_of_order"
	paramClaimWait       = "claim_wait"
	paramUpTo            = "up_to"

	statApplied     = "applied"
	statPending     = "pending"
	statDestructive = "destructive"
)

// migrate's defaults and bounds.
const (
	defaultMigrationTable = "public.pleiades_migrations"
	defaultFileTimeout    = 15 * time.Minute
	maxFileTimeout        = 24 * time.Hour
	defaultClaimWait      = 15 * time.Minute
	maxClaimWait          = 24 * time.Hour
	maxMigrationFiles     = 2000
	maxMigrationBytes     = sqllint.MaxSourceBytes
	maxMigrationsBytes    = 64 << 20
)

// migrationName is a migration file's name: a version, then a name.
var migrationName = regexp.MustCompile(`^([0-9]{1,18})_([A-Za-z0-9][A-Za-z0-9_.-]*)\.sql$`)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnMigrate,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "SQL has no inverse the method can derive; write the reverse as a rollback: list, or as a new forward migration.",
			},
			Doc: migrateDoc(),
		},
		Invoke: Migrate,
		Check:  CheckMigrate,
	})
}

// migration is one file: its version and name, its text and checksum,
// and what sqllint read in it.
type migration struct {
	version    int64
	name, file string
	text       string
	checksum   string
	directives sqllint.Directives
	statements []sqllint.Statement
}

// label names the file in messages.
func (m migration) label() string { return path.Base(m.file) }

// migrateRequest is a migrate task's parameters, read and checked.
type migrateRequest struct {
	database, dir, role    string
	schema, table          string
	additiveOnly, outOfOrd bool
	limits                 pgclient.Limits
	sql                    versionTableSQL
	claimWait              time.Duration
	target                 int64
}

// parseMigrate reads a migrate task's parameters.
func parseMigrate(params map[string]any) (migrateRequest, error) {
	var r migrateRequest
	var err error
	if r.database, err = databaseName(params, paramDatabase); err != nil {
		return r, err
	}
	if r.dir, err = sdk.RequiredStringParam(params, paramDir); err != nil {
		return r, err
	}
	if !path.IsAbs(r.dir) || strings.ContainsAny(r.dir, "\x00\r\n") {
		return r, fmt.Errorf("%s: %q is not an absolute path", paramDir, r.dir)
	}
	table := sdk.StringParam(params, paramTable)
	if table == "" {
		table = defaultMigrationTable
	}
	if r.schema, r.table, err = qualifiedName(table); err != nil {
		return r, fmt.Errorf("%s: %w", paramTable, err)
	}
	r.sql = newVersionTableSQL(r.schema, r.table)
	r.role = sdk.StringParam(params, paramRole)
	if len(r.role) > 63 || strings.ContainsAny(r.role, "\x00\r\n") {
		return r, fmt.Errorf("%s: %q is not a role name", paramRole, r.role)
	}
	if r.additiveOnly, err = sdk.BoolParamOr(params, paramAdditiveOnly, false); err != nil {
		return r, err
	}
	if r.outOfOrd, err = sdk.BoolParamOr(params, paramAllowOutOfOrder, false); err != nil {
		return r, err
	}
	if r.limits.Statement, err = durationParam(params, paramTimeout, defaultFileTimeout, maxFileTimeout); err != nil {
		return r, err
	}
	if r.limits.Lock, err = durationParam(params, paramLockTimeout, defaultLockTimeout, maxLockTimeout); err != nil {
		return r, err
	}
	if r.claimWait, err = durationParam(params, paramClaimWait, defaultClaimWait, maxClaimWait); err != nil {
		return r, err
	}
	if n, set, err := sdk.IntParam(params, paramUpTo); err != nil {
		return r, err
	} else if set {
		if n < 0 {
			return r, fmt.Errorf("%s is a version, from 0", paramUpTo)
		}
		r.target = int64(n)
	}
	return r, nil
}

// quotedTable is the version table as SQL, for messages and to_regclass.
func (r migrateRequest) quotedTable() string { return r.sql.table }

// versionTableSQL is every statement migrate sends that names the version
// table. An identifier cannot be a bind parameter, so this is the one place
// a task's table parameter becomes SQL text: its schema and name each
// passed qualifiedName (no quote, dot or control character, at most 63
// bytes) and are quoted here (quoteIdent), and every statement is built
// once, from them, rather than at each call.
type versionTableSQL struct {
	table, create, read, claim, setDuration, finish, unclaim, claimed string
}

// newVersionTableSQL builds the statements for schema.table.
func newVersionTableSQL(schema, table string) versionTableSQL {
	t := quoteIdent(schema) + "." + quoteIdent(table)
	return versionTableSQL{
		table: t,
		create: "CREATE TABLE IF NOT EXISTS " + t + ` (
  version bigint PRIMARY KEY,
  name text NOT NULL,
  checksum text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now(),
  applied_by text NOT NULL DEFAULT session_user,
  duration_ms bigint NOT NULL DEFAULT 0,
  finished boolean NOT NULL DEFAULT true
)`,
		read:        "SELECT version, name, checksum, finished FROM " + t,
		claim:       "INSERT INTO " + t + " (version, name, checksum, finished) VALUES ($1, $2, $3, $4)",
		setDuration: "UPDATE " + t + " SET duration_ms = $1 WHERE version = $2",
		finish:      "UPDATE " + t + " SET finished = true, duration_ms = $1 WHERE version = $2",
		unclaim:     "DELETE FROM " + t + " WHERE version = $1 AND NOT finished",
		claimed:     "SELECT checksum, finished FROM " + t + " WHERE version = $1",
	}
}

// Migrate implements db.pg.migrate.
func Migrate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return migrate(ctx, rc, device, params, collection.ModeExecute)
}

// CheckMigrate is db.pg.migrate's check: every file read and linted,
// every applied one's checksum verified, and the pending ones reported,
// with nothing applied.
func CheckMigrate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return migrate(ctx, rc, device, params, collection.ModeCheck)
}

// migrate is Migrate's and CheckMigrate's one body.
func migrate(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnMigrate, err)
	}
	req, err := parseMigrate(params)
	if err != nil {
		return fail(err)
	}
	conn, err := connectionFor(rc, params, paramConnection)
	if err != nil {
		return fail(err)
	}
	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnMigrate)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()

	files, err := readMigrations(ctx, ssh, req.dir)
	if err != nil {
		return fail(err)
	}
	problems, destructive := lintMigrations(files, req.additiveOnly)
	if len(problems) > 0 {
		return fail(fmt.Errorf("%d problem(s) in %s, so no file ran%s", len(problems), req.dir, listed(problems)))
	}

	s, err := open(ctx, ssh, conn, req.database)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = s.Close() }()
	c, err := s.DB().Conn(ctx)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = c.Close() }()
	if req.role != "" {
		var member bool
		if err := c.QueryRowContext(ctx, "SELECT pg_has_role(session_user, $1, 'MEMBER')", req.role).Scan(&member); err != nil {
			return fail(fmt.Errorf("%s %s: %w", paramRole, req.role, err))
		}
		if !member {
			return fail(fmt.Errorf("connection %s's login is not a member of %s, so it cannot run migrations as it", conn.Name, req.role))
		}
	}
	warnings := transportWarnings(conn)
	if err := rc.SetStat(statDestructive, destructive); err != nil {
		return collection.Result{}, err
	}

	applied, err := appliedVersions(ctx, c, req)
	if err != nil {
		return fail(err)
	}
	pending, warn, err := pendingMigrations(files, applied, req)
	if err != nil {
		return fail(err)
	}
	warnings = append(warnings, warn...)
	pendingEntries := make([]any, len(pending))
	for i, m := range pending {
		pendingEntries[i] = map[string]any{"version": m.version, "name": m.name}
	}
	if mode == collection.ModeCheck {
		if err := rc.SetStat(statPending, pendingEntries); err != nil {
			return collection.Result{}, err
		}
		return collection.Result{Changed: len(pending) > 0}, recordWarnings(rc, warnings)
	}

	if err := ensureVersionTable(ctx, c, req); err != nil {
		return fail(err)
	}
	var done []any
	for i, m := range pending {
		start := time.Now()
		err := applyMigration(ctx, c, req, m)
		if errors.Is(err, errClaimed) {
			// Another run claimed this version first: its record decides.
			if err = awaitClaim(ctx, c, req, m); err == nil {
				warnings = append(warnings, fmt.Sprintf("%s was applied by another run while this one waited", m.label()))
				continue
			}
		}
		if err != nil {
			so := "no file before it ran"
			if len(done) > 0 {
				so = fmt.Sprintf("the %d file(s) before it stay applied and recorded", len(done))
			}
			return fail(fmt.Errorf("%s failed, so it and the %d after it did not apply, and %s: %w", m.label(), len(pending)-i-1, so, err))
		}
		done = append(done, map[string]any{"version": m.version, "name": m.name, "duration_ms": time.Since(start).Milliseconds()})
	}
	if err := rc.SetStat(statApplied, done); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat(statPending, []any{}); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: len(done) > 0}, recordWarnings(rc, warnings)
}

// readMigrations lists dir's .sql files and reads each, refusing a name
// that is not <version>_<name>.sql (a typo is not skipped), a down
// migration (it would otherwise run as an up), and a version two files
// share.
func readMigrations(ctx context.Context, ssh *remoteexec.Conn, dir string) ([]migration, error) {
	info, err := remotefile.Stat(ctx, ssh, dir)
	if err != nil {
		return nil, err
	}
	if info.Kind != remotefile.KindDirectory {
		return nil, fmt.Errorf("%s is not a directory on the device", dir)
	}
	res, err := ssh.RunWithStdinBounded(ctx, "find "+remoteexec.QuoteArg(dir)+" -mindepth 1 -maxdepth 1 -name '*.sql' -print0", nil, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("listing %s: %s", dir, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 256))
	}
	var names []string
	for _, p := range strings.Split(res.Stdout, "\x00") {
		if p != "" {
			names = append(names, path.Base(p))
		}
	}
	if len(names) > maxMigrationFiles {
		return nil, fmt.Errorf("%s holds %d .sql files, over the %d one task reads", dir, len(names), maxMigrationFiles)
	}
	sort.Strings(names)
	byVersion := map[int64]string{}
	var out []migration
	total := 0
	for _, name := range names {
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".down.sql") || strings.HasSuffix(lower, ".undo.sql") {
			return nil, fmt.Errorf("%s is a down migration; this applies forward only, so move it out of %s", name, dir)
		}
		m := migrationName.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("%s is not named <version>_<name>.sql, so it would never run; rename it or move it out of %s", name, dir)
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if other, dup := byVersion[version]; dup {
			return nil, fmt.Errorf("%s and %s are both version %d", other, name, version)
		}
		byVersion[version] = name
		file := path.Join(dir, name)
		data, _, err := remotefile.ReadText(ctx, ssh, file, maxMigrationBytes)
		if err != nil {
			return nil, err
		}
		if total += len(data); total > maxMigrationsBytes {
			return nil, fmt.Errorf("%s's migrations are over the %d bytes one task reads", dir, maxMigrationsBytes)
		}
		stmts, err := sqllint.Split(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		directives, err := sqllint.ParseDirectives(string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		out = append(out, migration{
			version: version, name: m[2], file: file, text: string(data),
			checksum: hex.EncodeToString(sum[:]), directives: directives, statements: stmts,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// lintMigrations finds every problem in files before any runs: an empty
// file, a no-transaction file of more than one statement, a transactional
// file that ends its own transaction or holds a statement that cannot run
// in one, and, with additiveOnly, a destructive statement in a file that
// does not say why. It also lists each file that says why.
func lintMigrations(files []migration, additiveOnly bool) ([]string, []any) {
	var problems []string
	destructive := []any{}
	for _, m := range files {
		at := func(s sqllint.Statement) string { return fmt.Sprintf("%s:%d", m.label(), s.Line) }
		if len(m.statements) == 0 {
			problems = append(problems, m.label()+": it holds no statement")
			continue
		}
		if m.directives.NoTransaction {
			if len(m.statements) != 1 {
				problems = append(problems, fmt.Sprintf("%s: a no-transaction file must hold exactly one statement, so a failure cannot leave part of it applied; it holds %d", m.label(), len(m.statements)))
			}
		} else {
			for _, s := range m.statements {
				if sqllint.EndsTransaction(s) {
					problems = append(problems, at(s)+": it ends the transaction the file runs in; each file is its own transaction")
				}
				if what := sqllint.OutsideTransaction(s); what != "" {
					problems = append(problems, fmt.Sprintf("%s: %s cannot run in a transaction; give it a file of its own starting -- pleiades: no-transaction", at(s), what))
				}
			}
		}
		if m.directives.Destructive != "" {
			destructive = append(destructive, map[string]any{"file": m.label(), "reason": m.directives.Destructive})
			continue
		}
		if additiveOnly {
			for _, s := range m.statements {
				if why := sqllint.Destructive(s); why != "" {
					problems = append(problems, fmt.Sprintf("%s: %s (additive_only); if that is meant, say why with -- pleiades: destructive: <why>", at(s), why))
				}
			}
		}
	}
	return problems, destructive
}

// appliedRow is one row of the version table. A row not finished is a
// no-transaction file's claim whose run has not (or never) finished.
type appliedRow struct {
	name, checksum string
	finished       bool
}

// appliedVersions reads the version table, empty when it does not exist.
func appliedVersions(ctx context.Context, c *sql.Conn, req migrateRequest) (map[int64]appliedRow, error) {
	var exists bool
	if err := c.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", req.quotedTable()).Scan(&exists); err != nil {
		return nil, err
	}
	out := map[int64]appliedRow{}
	if !exists {
		return out, nil
	}
	rows, err := c.QueryContext(ctx, req.sql.read)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", req.quotedTable(), err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var v int64
		var r appliedRow
		if err := rows.Scan(&v, &r.name, &r.checksum, &r.finished); err != nil {
			return nil, err
		}
		out[v] = r
	}
	return out, rows.Err()
}

// pendingMigrations is what is left to apply, refusing a changed applied
// file and, unless allowed, a pending version below the newest applied
// one; an applied version whose file is gone is a warning.
func pendingMigrations(files []migration, applied map[int64]appliedRow, req migrateRequest) ([]migration, []string, error) {
	var newest int64 = -1
	for v := range applied {
		newest = max(newest, v)
	}
	present := map[int64]bool{}
	var pending, early []migration
	var problems []string
	var warnings []string
	for _, m := range files {
		present[m.version] = true
		if row, ok := applied[m.version]; ok && !row.finished {
			// A claim not finished: a run applying it, or one that died.
			// Applying it again meets the claim and waits on it.
			warnings = append(warnings, fmt.Sprintf("%s is claimed by a run that has not finished", m.label()))
		} else if ok {
			if row.checksum != m.checksum {
				problems = append(problems, fmt.Sprintf("%s was applied with checksum %s and now has %s", m.label(), row.checksum, m.checksum))
			}
			continue
		}
		if req.target > 0 && m.version > req.target {
			continue
		}
		if m.version < newest {
			early = append(early, m)
		}
		pending = append(pending, m)
	}
	if len(problems) > 0 {
		return nil, nil, fmt.Errorf("applied migrations changed since they ran; restore them, or record the change with a new migration%s", listed(problems))
	}
	if len(early) > 0 && !req.outOfOrd {
		names := make([]string, len(early))
		for i, m := range early {
			names[i] = m.label()
		}
		return nil, nil, fmt.Errorf("pending migrations come before version %d, which is applied; set %s to apply them anyway%s", newest, paramAllowOutOfOrder, listed(names))
	}
	var gone []int64
	for v := range applied {
		if !present[v] {
			gone = append(gone, v)
		}
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i] < gone[j] })
	for _, v := range gone {
		warnings = append(warnings, fmt.Sprintf("applied migration %d (%s) has no file in %s", v, applied[v].name, req.dir))
	}
	return pending, warnings, nil
}

// ensureVersionTable creates the version table when it is missing, as the
// task's role, so that role owns it.
func ensureVersionTable(ctx context.Context, c *sql.Conn, req migrateRequest) error {
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := asRole(ctx, tx, req.role); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, req.sql.create); err != nil {
		// CREATE TABLE IF NOT EXISTS is not safe for two sessions at once:
		// the second can meet the first's catalog row as a duplicate key
		// (or a duplicate table). Either means another run made the table.
		if code := pgclient.ErrorCode(err); code == pgclient.UniqueViolation || code == duplicateTable {
			_ = tx.Rollback()
			var exists bool
			if qerr := c.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", req.quotedTable()).Scan(&exists); qerr == nil && exists {
				return nil
			}
		}
		return fmt.Errorf("creating %s: %w", req.quotedTable(), err)
	}
	return tx.Commit()
}

// duplicateTable is the SQLSTATE of a table that already exists.
const duplicateTable = "42P07"

// errClaimed is applyMigration's answer when another run holds or has
// recorded the file's version.
var errClaimed = errors.New("another run claimed this version")

// applyMigration applies one file and records it, claiming its version
// first: the version is the table's primary key, so a second run's claim
// waits on the first's (for up to claim_wait) and then meets a duplicate
// key, which is errClaimed. A transactional file's claim, body and record
// are one transaction. A no-transaction file's claim is committed first,
// unfinished, then its one statement runs, and the claim is marked
// finished, or removed when the statement fails.
func applyMigration(ctx context.Context, c *sql.Conn, req migrateRequest, m migration) error {
	start := time.Now()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := asRole(ctx, tx, req.role); err != nil {
		return err
	}
	if err := pgclient.SetLimits(ctx, tx, pgclient.Limits{Lock: req.claimWait}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, req.sql.claim,
		m.version, m.name, m.checksum, !m.directives.NoTransaction); err != nil {
		if pgclient.ErrorCode(err) == pgclient.UniqueViolation {
			return errClaimed
		}
		return fmt.Errorf("claiming version %d: %w", m.version, err)
	}
	if m.directives.NoTransaction {
		if err := tx.Commit(); err != nil {
			return err
		}
		return applyOutsideTransaction(ctx, c, req, m, start)
	}
	if err := pgclient.SetLimits(ctx, tx, req.limits); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, m.text); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, req.sql.setDuration, time.Since(start).Milliseconds(), m.version); err != nil {
		return err
	}
	return tx.Commit()
}

// applyOutsideTransaction runs a claimed no-transaction file's one
// statement with the limits and role set for the session, resets them,
// and finishes or removes the claim.
func applyOutsideTransaction(ctx context.Context, c *sql.Conn, req migrateRequest, m migration, start time.Time) error {
	set := map[string]string{
		"statement_timeout": strconv.FormatInt(req.limits.Statement.Milliseconds(), 10),
		"lock_timeout":      strconv.FormatInt(req.limits.Lock.Milliseconds(), 10),
	}
	if req.role != "" {
		set["role"] = req.role
	}
	var runErr error
	for name, value := range set {
		if _, err := c.ExecContext(ctx, "SELECT set_config($1, $2, false)", name, value); err != nil {
			runErr = fmt.Errorf("setting %s: %w", name, err)
			break
		}
	}
	if runErr == nil {
		_, runErr = c.ExecContext(ctx, m.text)
	}
	_, _ = c.ExecContext(context.WithoutCancel(ctx), "RESET ROLE; RESET statement_timeout; RESET lock_timeout")
	if runErr != nil {
		if _, err := c.ExecContext(context.WithoutCancel(ctx), req.sql.unclaim, m.version); err != nil {
			return fmt.Errorf("%w; and its claim could not be removed, so version %d stays claimed until its row is deleted: %v", runErr, m.version, err)
		}
		return runErr
	}
	if _, err := c.ExecContext(ctx, req.sql.finish, time.Since(start).Milliseconds(), m.version); err != nil {
		return fmt.Errorf("its statement ran, but its claim could not be marked finished, so version %d reads as unfinished: %w", m.version, err)
	}
	return nil
}

// awaitClaim waits, up to claim_wait, for another run's claim on m's
// version to finish, and holds what it recorded to m's checksum.
func awaitClaim(ctx context.Context, c *sql.Conn, req migrateRequest, m migration) error {
	deadline := time.Now().Add(req.claimWait)
	for {
		var checksum string
		var finished bool
		err := c.QueryRowContext(ctx, req.sql.claimed, m.version).Scan(&checksum, &finished)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// Its claim was removed: the other run's statement failed.
			return fmt.Errorf("another run claimed version %d and its statement failed", m.version)
		case err != nil:
			return err
		case finished && checksum != m.checksum:
			return fmt.Errorf("another run applied version %d with checksum %s, and this file has %s", m.version, checksum, m.checksum)
		case finished:
			return nil
		case time.Now().After(deadline):
			return fmt.Errorf("version %d is claimed by a run that has not finished after %s; if no run is applying it, delete its row from %s", m.version, req.claimWait, req.quotedTable())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// asRole sets the transaction's role, when the task names one.
func asRole(ctx context.Context, tx *sql.Tx, role string) error {
	if role == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "SELECT set_config('role', $1, true)", role); err != nil {
		return fmt.Errorf("becoming %s: %w", role, err)
	}
	return nil
}
