// The named database connections a method may ask its run for.
package sdk

import "github.com/Subject-Void-LLC/the-pleiades/pkg/wire"

// DatabaseConnections is implemented by a RunbookContext that can give a
// method a named database connection. A task names the connection, and a
// method asks for that name and gets nothing else, so the host, login and
// password are never in a runbook. On the Crawl tier the answer comes from
// the project's credential store (`pleiades add-database-connection`); on
// the Walk tier from the template's pleiades_postgres credentials. A
// method that is handed a connection's password adds it to the run's
// masking set, which the run's own implementation does as it answers.
type DatabaseConnections interface {
	// DatabaseConnection returns the connection named name, false when
	// there is none, and an error when the store holding it cannot be
	// read (which is not the same as there being none).
	DatabaseConnection(name string) (wire.DatabaseConnection, bool, error)
}
