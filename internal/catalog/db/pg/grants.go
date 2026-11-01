// db.pg.grants: the privileges granted to a role, compared with a
// declared set.
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// grants' own parameters and returns.
const (
	paramGrantsRole = "role"
	paramExpect     = "expect"

	statExtra   = "extra"
	statMissing = "missing"
	// statExists says whether what a read names exists: db.pg.grants' role
	// and database, db.pg.rowcounts' database.
	statExists = "exists"
)

// grantKinds are the object kinds a declared set names, each with the
// privileges it has.
var grantKinds = map[string][]string{
	"database":  {"CONNECT", "CREATE", "TEMPORARY"},
	"schemas":   {"USAGE", "CREATE"},
	"tables":    {"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN"},
	"sequences": {"USAGE", "SELECT", "UPDATE"},
	"functions": {"EXECUTE"},
	"columns":   {"SELECT", "INSERT", "UPDATE", "REFERENCES"},
}

// singular names a kind in a message.
var singular = map[string]string{
	"database": "database", "schemas": "schema", "tables": "table", "sequences": "sequence", "functions": "function", "columns": "column",
}

// userSchema is the SQL condition for a schema that is not the system's.
const userSchema = `n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'`

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnGrants,
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"ssh"},
			RequiredCapabilities: []capability.Name{capability.NameSSHTransport},
			ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
			EngineVersion:        ">=0.2.0",
			Status:               collection.StatusImplemented,
			NoBecomeReason:       noBecomeForward,
			SupportsCheck:        true,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It reads the catalogs, and writes nothing.",
			},
			Doc: grantsDoc(),
		},
		Invoke: Grants,
		// It only reads, so a check of it is the task itself.
		Check: Grants,
	})
}

// privilege is one privilege on one object, and whether it is held (or
// expected) with the grant option.
type privilege struct {
	kind, object, name string
	grantable          bool
	// fromAll is a declared privilege that ALL stood for.
	fromAll bool
}

// key is the privilege without its grant option.
func (p privilege) key() string { return p.kind + "\x00" + p.object + "\x00" + p.name }

// String reads as a person would write it.
func (p privilege) String() string {
	s := fmt.Sprintf("%s on %s %s", p.name, singular[p.kind], p.object)
	if p.grantable {
		s += " WITH GRANT OPTION"
	}
	return s
}

// Grants implements db.pg.grants.
func Grants(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnGrants, err)
	}
	database, err := databaseName(params, paramDatabase)
	if err != nil {
		return fail(err)
	}
	role, err := sdk.RequiredStringParam(params, paramGrantsRole)
	if err != nil {
		return fail(err)
	}
	expect, err := parseExpect(params)
	if err != nil {
		return fail(err)
	}
	timeout, err := durationParam(params, paramTimeout, defaultQueryTimeout, maxQueryTimeout)
	if err != nil {
		return fail(err)
	}
	conn, err := connectionFor(rc, params, paramConnection)
	if err != nil {
		return fail(err)
	}
	ssh, err := sdk.Connect(ctx, rc, device, params, fqcnGrants)
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = ssh.Close() }()
	cat, err := grantsCatalog(ctx, ssh, conn, database, role, expect, timeout)
	if err != nil {
		return fail(err)
	}
	extra, missing := compareGrants(expect, cat)
	if cat.absent != "" {
		// The declared set names a role in a database, so one that does not
		// exist is a difference: answered as data, and still not a pass.
		missing = append([]string{cat.absent + " (it does not exist)"}, missing...)
	}
	toAny := func(in []string) []any {
		out := make([]any, len(in))
		for i, v := range in {
			out[i] = v
		}
		return out
	}
	if err := rc.SetStat(statExists, cat.absent == ""); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat(statExtra, toAny(extra)); err != nil {
		return collection.Result{}, err
	}
	if err := rc.SetStat(statMissing, toAny(missing)); err != nil {
		return collection.Result{}, err
	}
	if len(extra)+len(missing) > 0 {
		var lines []string
		for _, e := range extra {
			lines = append(lines, "extra: "+e)
		}
		for _, m := range missing {
			lines = append(lines, "missing: "+m)
		}
		return fail(fmt.Errorf("role %s in %s differs from the declared set in %d way(s)%s", role, database, len(lines), listed(lines)))
	}
	return collection.Result{Changed: false}, recordWarnings(rc, transportWarnings(conn))
}

// grantsCatalog reads role's catalog in database, fitting expect to the
// server first. A database that does not exist is answered as a catalog
// holding nothing, absent naming it, as readCatalog answers a role.
func grantsCatalog(ctx context.Context, ssh *remoteexec.Conn, conn wire.DatabaseConnection, database, role string, expect expectation, timeout time.Duration) (catalog, error) {
	s, err := open(ctx, ssh, conn, database)
	if pgclient.ErrorCode(err) == pgclient.InvalidCatalogName {
		return catalog{objects: map[string][]string{}, database: database, absent: "database " + database}, nil
	}
	if err != nil {
		return catalog{}, err
	}
	defer func() { _ = s.Close() }()
	_, major, err := s.ServerVersion(ctx)
	if err != nil {
		return catalog{}, err
	}
	if err := expect.fitServer(major); err != nil {
		return catalog{}, err
	}
	var cat catalog
	err = s.Snapshot(ctx, pgclient.Limits{Statement: timeout, Lock: defaultLockTimeout}, func(tx *sql.Tx) error {
		var err error
		cat, err = readCatalog(ctx, tx, role)
		return err
	})
	return cat, err
}

// compareGrants is every privilege, membership and owned object the role
// holds that the declared set does not, and every one it declares that the
// role lacks. A pattern schema.* expands to the objects of its kind that
// exist; an exact object that does not exist is missing; an object the
// role owns lacks nothing, since an owner holds every privilege on it.
func compareGrants(e expectation, cat catalog) (extra, missing []string) {
	expected := map[string]privilege{}
	for kind, byPattern := range e.privileges {
		for pattern, privs := range byPattern {
			objects := []string{pattern}
			if kind == "database" {
				objects = []string{cat.database}
			} else if strings.HasSuffix(pattern, ".*") {
				objects = nil
				schema := strings.TrimSuffix(pattern, "*")
				for _, o := range cat.objects[kind] {
					if strings.HasPrefix(o, schema) && !strings.Contains(strings.SplitN(strings.TrimPrefix(o, schema), "(", 2)[0], ".") {
						objects = append(objects, o)
					}
				}
			}
			for _, o := range objects {
				for _, p := range privs {
					p.object = o
					if prev, ok := expected[p.key()]; ok && prev.grantable {
						p.grantable = true
					}
					expected[p.key()] = p
				}
			}
		}
	}
	held := map[string]privilege{}
	for _, p := range cat.held {
		held[p.key()] = p
	}
	for k, h := range held {
		want, ok := expected[k]
		switch {
		case !ok:
			extra = append(extra, h.String())
		case h.grantable && !want.grantable:
			extra = append(extra, h.String()+" (the grant option is not declared)")
		}
	}
	owned := map[string]bool{}
	for _, o := range cat.owned {
		owned[o] = true
	}
	for k, want := range expected {
		// An owner holds every privilege on what it owns; whether it may
		// own it is compared below, as its own entry.
		if owned[want.object] || (want.kind == "database" && owned["database"]) {
			continue
		}
		h, ok := held[k]
		switch {
		case !ok:
			missing = append(missing, want.String())
		case want.grantable && !h.grantable:
			missing = append(missing, want.String())
		}
	}
	declaredMember := map[string]bool{}
	for _, m := range e.memberOf {
		declaredMember[m] = true
	}
	actualMember := map[string]bool{}
	for _, m := range cat.members {
		actualMember[m] = true
		if !declaredMember[m] {
			extra = append(extra, "member of "+m)
		}
	}
	for _, m := range e.memberOf {
		if !actualMember[m] {
			missing = append(missing, "member of "+m)
		}
	}
	for _, o := range cat.owned {
		if !ownershipDeclared(e.owns, o) {
			extra = append(extra, "owns "+o)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	return extra, missing
}

// ownershipDeclared reports whether owns allows owning object: by its
// exact name, or by schema.* for anything in the schema (not the schema
// itself).
func ownershipDeclared(owns []string, object string) bool {
	for _, pattern := range owns {
		if pattern == object {
			return true
		}
		if strings.HasSuffix(pattern, ".*") && strings.HasPrefix(object, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}
