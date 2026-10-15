// db.pg.grants: the privileges granted to a role, compared with a
// declared set.
package pg

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// grants' own parameters and returns.
const (
	paramGrantsRole = "role"
	paramExpect     = "expect"

	statExtra   = "extra"
	statMissing = "missing"
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

// expectation is a declared set: privileges by kind and object pattern,
// the roles the role is a member of, and what it may own.
type expectation struct {
	privileges map[string]map[string][]privilege // kind, pattern
	memberOf   []string
	owns       []string
}

// parseExpect reads the expect parameter, refusing a kind, a privilege or
// a pattern it does not know.
func parseExpect(params map[string]any) (expectation, error) {
	raw, ok := params[paramExpect].(map[string]any)
	if !ok {
		return expectation{}, fmt.Errorf("%s is a mapping of database, schemas, tables, sequences, functions, columns, member_of and owns", paramExpect)
	}
	e := expectation{privileges: map[string]map[string][]privilege{}}
	for key, value := range raw {
		switch key {
		case "member_of", "owns":
			list, err := stringList(value)
			if err != nil {
				return expectation{}, fmt.Errorf("%s.%s: %w", paramExpect, key, err)
			}
			if key == "member_of" {
				e.memberOf = list
			} else {
				e.owns = list
			}
		case "database":
			list, err := stringList(value)
			if err != nil {
				return expectation{}, fmt.Errorf("%s.database: %w", paramExpect, err)
			}
			privs, err := parsePrivileges(key, "", list)
			if err != nil {
				return expectation{}, err
			}
			e.privileges[key] = map[string][]privilege{"": privs}
		default:
			if _, known := grantKinds[key]; !known {
				return expectation{}, fmt.Errorf("%s.%s is not a kind this compares (database, schemas, tables, sequences, functions, columns, member_of, owns)", paramExpect, key)
			}
			byPattern, ok := value.(map[string]any)
			if !ok {
				return expectation{}, fmt.Errorf("%s.%s is a mapping of object (or schema.*) to privileges", paramExpect, key)
			}
			e.privileges[key] = map[string][]privilege{}
			for pattern, v := range byPattern {
				list, err := stringList(v)
				if err != nil {
					return expectation{}, fmt.Errorf("%s.%s[%s]: %w", paramExpect, key, pattern, err)
				}
				if err := checkPattern(key, pattern); err != nil {
					return expectation{}, err
				}
				privs, err := parsePrivileges(key, pattern, list)
				if err != nil {
					return expectation{}, err
				}
				e.privileges[key][pattern] = privs
			}
		}
	}
	return e, nil
}

// checkPattern refuses an object pattern the kind cannot have.
func checkPattern(kind, pattern string) error {
	parts := strings.Count(strings.SplitN(pattern, "(", 2)[0], ".")
	want := map[string]int{"schemas": 0, "tables": 1, "sequences": 1, "functions": 1, "columns": 2}[kind]
	if pattern == "" || parts != want || (kind == "schemas" && strings.Contains(pattern, "*")) || (kind == "columns" && strings.Contains(pattern, "*")) {
		return fmt.Errorf("%s.%s: %q is not a %s (or schema.*)", paramExpect, kind, pattern, singular[kind])
	}
	if strings.Contains(pattern, "*") && !strings.HasSuffix(pattern, ".*") {
		return fmt.Errorf("%s.%s: %q: only schema.* may hold *", paramExpect, kind, pattern)
	}
	return nil
}

// parsePrivileges reads privilege names, upper-cased, with ALL standing
// for every privilege the kind has and WITH GRANT OPTION kept.
func parsePrivileges(kind, object string, names []string) ([]privilege, error) {
	var out []privilege
	for _, raw := range names {
		name := strings.ToUpper(strings.Join(strings.Fields(raw), " "))
		grantable := strings.HasSuffix(name, " WITH GRANT OPTION")
		name = strings.TrimSuffix(name, " WITH GRANT OPTION")
		if name == "TEMP" {
			name = "TEMPORARY"
		}
		all := grantKinds[kind]
		if name == "ALL" || name == "ALL PRIVILEGES" {
			for _, p := range all {
				out = append(out, privilege{kind: kind, object: object, name: p, grantable: grantable})
			}
			continue
		}
		known := false
		for _, p := range all {
			known = known || p == name
		}
		if !known {
			return nil, fmt.Errorf("%s.%s: %q is not a privilege a %s has (%s)", paramExpect, kind, raw, singular[kind], strings.Join(all, ", "))
		}
		out = append(out, privilege{kind: kind, object: object, name: name, grantable: grantable})
	}
	return out, nil
}

// stringList reads a list of strings.
func stringList(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("it is a list of names")
	}
	out := make([]string, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, fmt.Errorf("entry %d is not a name", i)
		}
		out[i] = s
	}
	return out, nil
}

// catalog is what the database holds that a declared set is compared
// with: the role's direct grants, memberships and owned objects, and every
// object a pattern can expand to.
type catalog struct {
	held     []privilege
	members  []string
	owned    []string
	objects  map[string][]string // kind to objects
	database string
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
	s, err := open(ctx, ssh, conn, database)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = s.Close() }()

	var cat catalog
	if err := s.Snapshot(ctx, pgclient.Limits{Statement: timeout, Lock: defaultLockTimeout}, func(tx *sql.Tx) error {
		var err error
		cat, err = readCatalog(ctx, tx, role)
		return err
	}); err != nil {
		return fail(err)
	}
	extra, missing := compareGrants(expect, cat)
	toAny := func(in []string) []any {
		out := make([]any, len(in))
		for i, v := range in {
			out[i] = v
		}
		return out
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

// readCatalog reads what compareGrants compares, refusing a role that
// does not exist.
func readCatalog(ctx context.Context, tx *sql.Tx, role string) (catalog, error) {
	cat := catalog{objects: map[string][]string{}}
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1), current_database()", role).Scan(&exists, &cat.database); err != nil {
		return cat, err
	}
	if !exists {
		return cat, fmt.Errorf("role %s does not exist", role)
	}
	rows, err := tx.QueryContext(ctx, `
WITH r AS (SELECT oid FROM pg_roles WHERE rolname = $1)
SELECT 'database', d.datname, a.privilege_type, a.is_grantable
  FROM pg_database d, aclexplode(d.datacl) a, r
 WHERE d.datname = current_database() AND a.grantee = r.oid AND d.datdba <> r.oid
UNION ALL
SELECT 'schemas', n.nspname, a.privilege_type, a.is_grantable
  FROM pg_namespace n, aclexplode(n.nspacl) a, r
 WHERE a.grantee = r.oid AND n.nspowner <> r.oid AND `+userSchema+`
UNION ALL
SELECT CASE WHEN c.relkind = 'S' THEN 'sequences' ELSE 'tables' END, n.nspname || '.' || c.relname, a.privilege_type, a.is_grantable
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(c.relacl) a, r
 WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S') AND a.grantee = r.oid AND c.relowner <> r.oid AND `+userSchema+`
UNION ALL
SELECT 'functions', n.nspname || '.' || p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')', a.privilege_type, a.is_grantable
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace, aclexplode(p.proacl) a, r
 WHERE a.grantee = r.oid AND p.proowner <> r.oid AND `+userSchema+`
UNION ALL
SELECT 'columns', n.nspname || '.' || c.relname || '.' || att.attname, a.privilege_type, a.is_grantable
  FROM pg_attribute att JOIN pg_class c ON c.oid = att.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace, aclexplode(att.attacl) a, r
 WHERE att.attnum > 0 AND NOT att.attisdropped AND a.grantee = r.oid AND c.relowner <> r.oid AND `+userSchema, role)
	if err != nil {
		return cat, err
	}
	for rows.Next() {
		var p privilege
		if err := rows.Scan(&p.kind, &p.object, &p.name, &p.grantable); err != nil {
			_ = rows.Close()
			return cat, err
		}
		cat.held = append(cat.held, p)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return cat, err
	}
	if cat.members, err = column(ctx, tx, `
SELECT b.rolname FROM pg_auth_members m JOIN pg_roles b ON b.oid = m.roleid JOIN pg_roles r ON r.oid = m.member
 WHERE r.rolname = $1 ORDER BY 1`, role); err != nil {
		return cat, err
	}
	if cat.owned, err = column(ctx, tx, `
WITH r AS (SELECT oid FROM pg_roles WHERE rolname = $1)
SELECT 'database' FROM pg_database d, r WHERE d.datname = current_database() AND d.datdba = r.oid
UNION ALL SELECT n.nspname FROM pg_namespace n, r WHERE n.nspowner = r.oid AND `+userSchema+`
UNION ALL SELECT n.nspname || '.' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace, r
 WHERE c.relowner = r.oid AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S') AND `+userSchema+`
UNION ALL SELECT n.nspname || '.' || p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')'
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace, r WHERE p.proowner = r.oid AND `+userSchema, role); err != nil {
		return cat, err
	}
	for kind, q := range map[string]string{
		"schemas":   `SELECT n.nspname FROM pg_namespace n WHERE ` + userSchema,
		"tables":    `SELECT n.nspname || '.' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r', 'p', 'v', 'm', 'f') AND ` + userSchema,
		"sequences": `SELECT n.nspname || '.' || c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'S' AND ` + userSchema,
		"functions": `SELECT n.nspname || '.' || p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')' FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE ` + userSchema,
	} {
		if cat.objects[kind], err = column(ctx, tx, q); err != nil {
			return cat, err
		}
	}
	return cat, nil
}

// column reads one text column.
func column(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
