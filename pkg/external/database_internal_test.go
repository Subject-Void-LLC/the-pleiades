// Tests that a child's run hands a method a database connection by name.
package external

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestRunbookContext_DatabaseConnectionsByName: the child's run answers
// for the connection a method names and no other, not at all when the
// request carried none (every request an external program receives), and
// drops them with its secrets when zeroed.
func TestRunbookContext_DatabaseConnectionsByName(t *testing.T) {
	rc := NewRunbookContext(nil)
	if _, ok, err := rc.DatabaseConnection("appdb"); ok || err != nil {
		t.Errorf("a run with no connections answered: %v, %v", ok, err)
	}
	rc.databases = []wire.DatabaseConnection{{Name: "appdb", Host: "db", User: "app", Password: "p"}}
	if c, ok, err := rc.DatabaseConnection("appdb"); !ok || err != nil || c.Password != "p" {
		t.Errorf("appdb = %+v, %v, %v", c, ok, err)
	}
	if _, ok, _ := rc.DatabaseConnection("other"); ok {
		t.Error("another name got the connection")
	}
	if len(rc.InjectSecrets()) != 0 {
		t.Error("a connection reached InjectSecrets")
	}
	rc.Zero()
	if _, ok, _ := rc.DatabaseConnection("appdb"); ok {
		t.Error("a zeroed run still answers")
	}
}
