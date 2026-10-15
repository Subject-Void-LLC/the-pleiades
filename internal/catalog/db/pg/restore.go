// db.pg.restore: the device's pg_restore from an archive on the device
// into a database the task names, in one transaction.
package pg

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// restore's own parameters and returns.
const (
	paramSrc                 = "src"
	paramCreate              = "create"
	paramClean               = "clean"
	paramAllowLive           = "allow_live"
	paramLiveReason          = "live_reason"
	paramNoOwner             = "no_owner"
	paramNoPrivileges        = "no_privileges"
	paramMaintenanceDatabase = "maintenance_database"

	statDatabase   = "database"
	statCreated    = "created"
	statTOCEntries = "toc_entries"
	statLive       = "live_override"
)

// liveSetting is the database setting that marks a database live.
const liveSetting = "pleiades.live"

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnRestore,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameCommandExec},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "It replaces a database's contents, which no recorded value could put back; dump the target first (db.pg.dump) to keep a way back.",
			},
			Doc: restoreDoc(),
		},
		Invoke: Restore,
		Check:  CheckRestore,
	})
}

// restoreRequest is a restore task's parameters, read and checked.
type restoreRequest struct {
	database, src, maintenance string
	create, clean              bool
	allowLive                  bool
	liveReason                 string
	noOwner, noPrivileges      bool
}

// parseRestore reads a restore task's parameters.
func parseRestore(params map[string]any) (restoreRequest, error) {
	var r restoreRequest
	var err error
	if r.database, err = databaseName(params, paramDatabase); err != nil {
		return r, err
	}
	if r.src, err = sdk.RequiredStringParam(params, paramSrc); err != nil {
		return r, err
	}
	if !path.IsAbs(r.src) || strings.ContainsAny(r.src, "\x00\r\n") {
		return r, fmt.Errorf("%s: %q is not an absolute path", paramSrc, r.src)
	}
	r.maintenance = "postgres"
	if _, set := params[paramMaintenanceDatabase]; set {
		if r.maintenance, err = databaseName(params, paramMaintenanceDatabase); err != nil {
			return r, err
		}
	}
	if r.maintenance == r.database {
		return r, fmt.Errorf("%s is the database being restored; name another to connect to while it is checked or created", paramMaintenanceDatabase)
	}
	bools := []struct {
		key string
		def bool
		out *bool
	}{{paramCreate, false, &r.create}, {paramClean, false, &r.clean}, {paramAllowLive, false, &r.allowLive}, {paramNoOwner, true, &r.noOwner}, {paramNoPrivileges, true, &r.noPrivileges}}
	for _, b := range bools {
		if *b.out, err = sdk.BoolParamOr(params, b.key, b.def); err != nil {
			return r, err
		}
	}
	r.liveReason = strings.TrimSpace(sdk.StringParam(params, paramLiveReason))
	if r.allowLive && r.liveReason == "" {
		return r, fmt.Errorf("%s needs %s: say why a live database is being overwritten, which the run records", paramAllowLive, paramLiveReason)
	}
	if !r.allowLive && r.liveReason != "" {
		return r, fmt.Errorf("%s means nothing without %s", paramLiveReason, paramAllowLive)
	}
	return r, nil
}

// Restore implements db.pg.restore.
func Restore(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return restore(ctx, rc, device, params, collection.ModeExecute)
}

// CheckRestore is db.pg.restore's check: the archive listed, and every
// refusal a run makes, with nothing written.
func CheckRestore(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return restore(ctx, rc, device, params, collection.ModeCheck)
}

// restore is Restore's and CheckRestore's one body.
func restore(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnRestore, err)
	}
	req, err := parseRestore(params)
	if err != nil {
		return fail(err)
	}
	timeout, err := durationParam(params, paramTimeout, defaultToolTimeout, maxToolTimeout)
	if err != nil {
		return fail(err)
	}
	conn, err := connectionFor(rc, params, paramConnection)
	if err == nil {
		err = toolsCanReach(conn)
	}
	if err != nil {
		return fail(err)
	}
	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnRestore)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()

	if err := requireTimeout(ctx, ssh); err != nil {
		return fail(err)
	}
	entries, err := listArchive(ctx, ssh, req.src)
	if err != nil {
		return fail(err)
	}
	maint, err := open(ctx, ssh, conn, req.maintenance)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = maint.Close() }()
	exists, markedLive, err := targetState(ctx, maint, req.database)
	if err != nil {
		return fail(err)
	}
	warnings := transportWarnings(conn)
	if live := conn.IsLive(req.database) || markedLive; live {
		why := "connection " + conn.Name + " lists it as live"
		if markedLive {
			why = "it is marked live (" + liveSetting + " = on)"
		}
		if !req.allowLive {
			return fail(fmt.Errorf("%s is a live database (%s); restoring over it needs %s and a %s", req.database, why, paramAllowLive, paramLiveReason))
		}
		warnings = append(warnings, fmt.Sprintf("restoring over live database %s (%s): %s", req.database, why, req.liveReason))
		if err := rc.SetStat(statLive, req.liveReason); err != nil {
			return collection.Result{}, err
		}
	}
	switch {
	case !exists && !req.create:
		return fail(fmt.Errorf("database %s does not exist on connection %s; set %s to create it", req.database, conn.Name, paramCreate))
	case exists:
		target, err := open(ctx, ssh, conn, req.database)
		if err != nil {
			return fail(err)
		}
		relations, err := userRelations(ctx, target)
		_ = target.Close()
		if err != nil {
			return fail(err)
		}
		if relations > 0 && !req.clean {
			return fail(fmt.Errorf("database %s already holds %d table(s), view(s) or sequence(s); set %s to drop what the archive restores first", req.database, relations, paramClean))
		}
	}
	toolMaj, toolVersion, err := toolMajor(ctx, ssh, "pg_restore")
	if err != nil {
		return fail(err)
	}
	_, serverMajor, err := maint.ServerVersion(ctx)
	if err != nil {
		return fail(err)
	}
	if toolMaj != serverMajor {
		warnings = append(warnings, fmt.Sprintf("the device's pg_restore is %d and the server is %d; a newer pg_restore can send settings an older server does not have", toolMaj, serverMajor))
	}
	for key, value := range map[string]any{statDatabase: req.database, statCreated: !exists, statTOCEntries: entries, statServerVersion: serverMajor, statToolVersion: toolVersion} {
		if err := rc.SetStat(key, value); err != nil {
			return collection.Result{}, err
		}
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordWarnings(rc, warnings)
	}

	if !exists {
		if _, err := maint.DB().ExecContext(ctx, "CREATE DATABASE "+quoteIdent(req.database)); err != nil {
			return fail(fmt.Errorf("creating database %s: %w", req.database, err))
		}
	}
	command := []string{"pg_restore", "--single-transaction", "--exit-on-error", "--no-password", "--dbname=" + req.database}
	if req.clean {
		command = append(command, "--clean", "--if-exists")
	}
	if req.noOwner {
		command = append(command, "--no-owner")
	}
	if req.noPrivileges {
		command = append(command, "--no-privileges")
	}
	command = append(command, "--", req.src)
	if _, err := (toolRun{conn: conn, database: req.database, command: command, timeout: timeout}).run(ctx, ssh); err != nil {
		if !exists {
			// The run created it, and one transaction left it empty: the
			// server is left as it was found.
			if _, dropErr := maint.DB().ExecContext(ctx, "DROP DATABASE "+quoteIdent(req.database)); dropErr != nil {
				return fail(fmt.Errorf("%w; the database this run created, %s, is empty and was not dropped: %v", err, req.database, dropErr))
			}
			return fail(fmt.Errorf("%w; nothing was restored, and the database this run created was dropped", err))
		}
		return fail(fmt.Errorf("%w; nothing was restored: it ran in one transaction", err))
	}
	return collection.Result{Changed: true}, recordWarnings(rc, warnings)
}

// listArchive lists the archive at src (pg_restore --list), which reads it
// whole and so refuses one that is not an archive, and returns how many
// entries it holds.
func listArchive(ctx context.Context, ssh *remoteexec.Conn, src string) (int, error) {
	info, err := remotefile.Stat(ctx, ssh, src)
	if err != nil {
		return 0, err
	}
	if info.Kind != remotefile.KindFile {
		return 0, fmt.Errorf("%s is not a file on the device", src)
	}
	res, err := ssh.RunWithStdinBounded(ctx, remoteexec.QuoteCommand([]string{"pg_restore", "--list", "--", src}), nil, maxToolOutput)
	if err != nil {
		return 0, fmt.Errorf("listing %s: %w", src, err)
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("%s is not an archive pg_restore reads: %s", src, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 1024))
	}
	n := 0
	for _, line := range strings.Split(res.Stdout, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, ";") {
			n++
		}
	}
	return n, nil
}

// targetState reports whether database exists on the server, and whether
// it marks itself live.
func targetState(ctx context.Context, s *pgclient.Session, database string) (exists, live bool, err error) {
	rows, err := s.DB().QueryContext(ctx, `
SELECT true, coalesce((SELECT array_to_string(st.setconfig, E'\n') FROM pg_db_role_setting st
                       WHERE st.setdatabase = d.oid AND st.setrole = 0), '')
FROM pg_database d WHERE d.datname = $1`, database)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var settings string
		if err := rows.Scan(&exists, &settings); err != nil {
			return false, false, err
		}
		for _, line := range strings.Split(settings, "\n") {
			name, value, _ := strings.Cut(line, "=")
			if strings.EqualFold(name, liveSetting) {
				switch strings.ToLower(strings.TrimSpace(value)) {
				case "on", "true", "yes", "1":
					live = true
				}
			}
		}
	}
	return exists, live, rows.Err()
}

// userRelations counts the tables, views, materialized views, sequences
// and foreign tables outside the system schemas.
func userRelations(ctx context.Context, s *pgclient.Session) (int, error) {
	var n int
	err := s.DB().QueryRowContext(ctx, `
SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'`).Scan(&n)
	return n, err
}
