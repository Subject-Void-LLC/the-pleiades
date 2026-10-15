// db.pg.grants' reading of the catalogs: what a role was granted, is a
// member of and owns, in one snapshot.
package pg

import (
	"context"
	"database/sql"
	"fmt"
)

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
