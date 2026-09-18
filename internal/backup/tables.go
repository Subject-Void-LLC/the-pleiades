// The tables a backup of this schema may hold.
package backup

import "github.com/Subject-Void-LLC/the-pleiades/internal/ent/migrate"

// historyTable is the migration runner's own table, which every database a
// controller has opened holds and which ent's generated schema does not list.
const historyTable = "schema_migrations"

// knownTables is every table this version's schema has: ent's generated list
// plus the migration history. No migration in any version drops a table, so
// a backup from an older version holds a subset of these.
func knownTables() map[string]bool {
	known := map[string]bool{historyTable: true}
	for _, t := range migrate.Tables {
		known[t.Name] = true
	}
	return known
}
