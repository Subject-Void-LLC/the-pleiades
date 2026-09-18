// The schema comparison a restored database must pass before anything with
// more rights than its own scratch role touches it.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
)

// userSchemas is the predicate, over a namespace alias n, for every schema a
// database's own objects can live in: all but PostgreSQL's catalogs and its
// per-session temporary and toast schemas.
const userSchemas = `n.nspname NOT IN ('pg_catalog', 'information_schema') AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'`

// shapeQueries each return one text column: one line per object of a kind,
// written so that two databases built by the same migrations produce the
// same lines, and anything else produces a line the other does not have.
//
// Why a comparison and not a list of things to refuse: a restored database
// holds whatever the file said to create, and the objects that can run code
// later are many and easy to miss. A trigger on jobs runs as whoever next
// updates a job; a column default runs as whoever next inserts a row, and a
// default can call a built-in that writes files on the server when that
// someone is a superuser, which the compose stack's controller is. Listing
// every such kind to refuse is a list that is complete until the day it is
// not. Requiring the restored schema to be exactly what this version's
// migrations make, and nothing else, needs no such list: whatever a crafted
// file added shows up as a line a fresh database does not have.
var shapeQueries = []string{
	// Every schema, so an object cannot hide in a second one.
	`SELECT 'schema ' || n.nspname FROM pg_namespace n WHERE ` + userSchemas,

	// Every relation: tables, indexes and sequences, and anything else,
	// with the settings that change how one is read.
	`SELECT 'relation ' || n.nspname || '.' || c.relname || ' kind=' || c.relkind::text || ' persistence=' || c.relpersistence::text ||
		' rls=' || c.relrowsecurity || '/' || c.relforcerowsecurity || ' options=' || coalesce(array_to_string(c.reloptions, ','), '')
	 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE ` + userSchemas,

	// Every column, with its default, which is code that runs on insert.
	`SELECT 'column ' || n.nspname || '.' || c.relname || '.' || a.attname || ' type=' || format_type(a.atttypid, a.atttypmod) ||
		' notnull=' || a.attnotnull || ' identity=' || a.attidentity::text || ' generated=' || a.attgenerated::text ||
		' default=' || coalesce(pg_get_expr(d.adbin, d.adrelid), '') || ' collation=' || coalesce(co.collname, '')
	 FROM pg_attribute a
	 JOIN pg_class c ON c.oid = a.attrelid
	 JOIN pg_namespace n ON n.oid = c.relnamespace
	 LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
	 LEFT JOIN pg_collation co ON co.oid = a.attcollation
	 WHERE a.attnum > 0 AND NOT a.attisdropped AND c.relkind IN ('r', 'p', 'v', 'm', 'f') AND ` + userSchemas,

	// Every constraint, with its definition: a check is code that runs on
	// every write.
	`SELECT 'constraint ' || coalesce(c.relname, 'type ' || t.typname, '') || ' ' || con.conname || ' ' || con.contype::text || ' ' || pg_get_constraintdef(con.oid)
	 FROM pg_constraint con
	 JOIN pg_namespace n ON n.oid = con.connamespace
	 LEFT JOIN pg_class c ON c.oid = con.conrelid
	 LEFT JOIN pg_type t ON t.oid = con.contypid
	 WHERE ` + userSchemas,

	// Every index, by its full definition, including any expression.
	`SELECT 'index ' || pg_get_indexdef(i.indexrelid)
	 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE ` + userSchemas,

	// Every sequence's settings. Its current value is data, not shape.
	`SELECT 'sequence ' || c.relname || ' type=' || format_type(s.seqtypid, NULL) || ' start=' || s.seqstart || ' increment=' || s.seqincrement ||
		' min=' || s.seqmin || ' max=' || s.seqmax || ' cache=' || s.seqcache || ' cycle=' || s.seqcycle
	 FROM pg_sequence s JOIN pg_class c ON c.oid = s.seqrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE ` + userSchemas,

	// Every function, procedure and aggregate outside the catalogs.
	`SELECT 'function ' || n.nspname || '.' || p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')'
	 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace WHERE ` + userSchemas,

	// Every trigger a person or a file made. Foreign keys make internal ones.
	`SELECT 'trigger ' || c.relname || '.' || t.tgname FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid WHERE NOT t.tgisinternal`,

	// Every rule, which rewrites a query into another.
	`SELECT 'rule ' || c.relname || '.' || r.rulename
	 FROM pg_rewrite r JOIN pg_class c ON c.oid = r.ev_class JOIN pg_namespace n ON n.oid = c.relnamespace WHERE ` + userSchemas,

	// Every row security policy.
	`SELECT 'policy ' || c.relname || '.' || p.polname FROM pg_policy p JOIN pg_class c ON c.oid = p.polrelid`,

	// Every type other than a table's own row type and that row type's
	// array: a domain carries a check, and an enum or range changes what a
	// column accepts.
	`SELECT 'type ' || n.nspname || '.' || t.typname || ' ' || t.typtype::text
	 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
	 WHERE t.typrelid = 0
	   AND NOT (t.typcategory = 'A' AND EXISTS (SELECT 1 FROM pg_type e WHERE e.oid = t.typelem AND e.typrelid <> 0))
	   AND ` + userSchemas,

	// Operators, operator classes and families, and collations, each of
	// which changes what a comparison in a query means.
	`SELECT 'operator ' || n.nspname || '.' || o.oprname FROM pg_operator o JOIN pg_namespace n ON n.oid = o.oprnamespace WHERE ` + userSchemas,
	`SELECT 'operator class ' || n.nspname || '.' || o.opcname FROM pg_opclass o JOIN pg_namespace n ON n.oid = o.opcnamespace WHERE ` + userSchemas,
	`SELECT 'operator family ' || n.nspname || '.' || o.opfname FROM pg_opfamily o JOIN pg_namespace n ON n.oid = o.opfnamespace WHERE ` + userSchemas,
	`SELECT 'collation ' || n.nspname || '.' || o.collname FROM pg_collation o JOIN pg_namespace n ON n.oid = o.collnamespace WHERE ` + userSchemas,
	`SELECT 'conversion ' || n.nspname || '.' || o.conname FROM pg_conversion o JOIN pg_namespace n ON n.oid = o.connamespace WHERE ` + userSchemas,
	`SELECT 'text search configuration ' || n.nspname || '.' || o.cfgname FROM pg_ts_config o JOIN pg_namespace n ON n.oid = o.cfgnamespace WHERE ` + userSchemas,

	// Casts anyone created: the built-in ones all have catalog OIDs below
	// FirstNormalObjectId, 16384.
	`SELECT 'cast ' || format_type(castsource, NULL) || ' to ' || format_type(casttarget, NULL) FROM pg_cast WHERE oid >= 16384`,

	// Extensions, table inheritance, publications, foreign data, event
	// triggers and large objects: none of which this schema uses.
	`SELECT 'extension ' || extname FROM pg_extension WHERE extname <> 'plpgsql'`,
	`SELECT 'inherits ' || c.relname || ' from ' || p.relname FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid JOIN pg_class p ON p.oid = i.inhparent`,
	`SELECT 'publication ' || pubname FROM pg_publication`,
	`SELECT 'foreign data wrapper ' || fdwname FROM pg_foreign_data_wrapper`,
	`SELECT 'foreign server ' || srvname FROM pg_foreign_server`,
	`SELECT 'event trigger ' || evtname FROM pg_event_trigger`,
	`SELECT 'large objects ' || count(*) FROM pg_largeobject_metadata HAVING count(*) > 0`,

	// Settings attached to this database, which apply to every session in
	// it, including the controller's.
	`SELECT 'setting ' || array_to_string(s.setconfig, ',')
	 FROM pg_db_role_setting s JOIN pg_database d ON d.oid = s.setdatabase WHERE d.datname = current_database()`,
}

// schemaShape reads db's shape: every line every shape query returns,
// sorted.
func schemaShape(ctx context.Context, db *sql.DB) ([]string, error) {
	var lines []string
	for _, q := range shapeQueries {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("reading the schema: %w", err)
		}
		for rows.Next() {
			var line sql.NullString
			if err := rows.Scan(&line); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("reading the schema: %w", err)
			}
			if !line.Valid {
				// A NULL means some part of the line was NULL where the
				// query expected text, and a comparison that skipped it
				// could miss exactly the object that made it NULL.
				_ = rows.Close()
				return nil, errors.New("reading the schema: an object could not be described")
			}
			lines = append(lines, line.String)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the schema: %w", err)
		}
	}
	sort.Strings(lines)
	return lines, nil
}

// shapeDifference lists what got has that want does not, and what want has
// that got does not, each line sanitized for printing.
func shapeDifference(want, got []string) (extra, missing []string) {
	count := map[string]int{}
	for _, line := range want {
		count[line]++
	}
	for _, line := range got {
		if count[line] > 0 {
			count[line]--
			continue
		}
		extra = append(extra, printableUpTo(line, 200))
	}
	for line, n := range count {
		for ; n > 0; n-- {
			missing = append(missing, printableUpTo(line, 200))
		}
	}
	sort.Strings(missing)
	return extra, missing
}
