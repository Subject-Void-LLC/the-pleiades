// db.pg.migrate: a directory of numbered SQL files applied in order, each
// recorded in a version table.
package pg

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
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
