// Tests that the Runner masks database passwords and hands connections to
// its own child.
package native

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestDatabaseConnections_ReachTheChildMasked: every bound connection's
// password is a value the Runner masks, and the child request for a
// built-in method carries the connections.
func TestDatabaseConnections_ReachTheChildMasked(t *testing.T) {
	dbs := []wire.DatabaseConnection{
		{Name: "appdb", Host: "db", User: "app", Password: "dbsentinel-4Kp9"},
		{Name: "trust", Host: "/run/postgresql", User: "postgres"},
	}
	if got := databaseSecretValues(dbs); !slices.Equal(got, []string{"dbsentinel-4Kp9"}) {
		t.Errorf("databaseSecretValues = %v", got)
	}
	req := childRequest(collection.Descriptor{Name: "db.pg.query"}, collection.ModeExecute, nil, wire.DispatchPayload{Databases: dbs})
	if len(req.Databases) != 2 || req.Databases[0].Password != "dbsentinel-4Kp9" {
		t.Errorf("the child request carries %+v", req.Databases)
	}
}
