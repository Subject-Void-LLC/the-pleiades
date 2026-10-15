// Tests that a Crawl run answers a method's request for a named database
// connection from the project's store, for that name and no other.
package engine

import (
	"context"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestCredentialRunbookContext_AnswersForDatabases: a run answers for the
// connection a method names, with its password, which joins the run's
// masking set as it is handed out and never reaches the device's secrets.
func TestCredentialRunbookContext_AnswersForDatabases(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveDatabaseConnection(dir, key, wire.DatabaseConnection{Name: "appdb", Host: "127.0.0.1", User: "app", Password: "db-secret", SSLMode: wire.SSLDisable, Live: []string{"app"}}); err != nil {
		t.Fatal(err)
	}
	rc, err := NewCredentialRunbookContext(credential.NewLazyFileStore(dir))(context.Background(), &inventorytest.Stub{StubName: "db-host"})
	if err != nil {
		t.Fatal(err)
	}
	dbs, ok := rc.(sdk.DatabaseConnections)
	if !ok {
		t.Fatal("the run cannot answer for database connections")
	}
	if _, ok, err := dbs.DatabaseConnection("other"); ok || err != nil {
		t.Errorf("an unknown connection = %v, %v", ok, err)
	}
	if got := rc.(*runbookContext).RegisteredSecrets(); len(got) != 0 {
		t.Errorf("secrets registered before any was handed out: %d", len(got))
	}
	conn, ok, err := dbs.DatabaseConnection("appdb")
	if err != nil || !ok || conn.Host != "127.0.0.1" || conn.User != "app" || conn.Password != "db-secret" || !conn.IsLive("app") {
		t.Fatalf("appdb = %+v, %v, %v", conn, ok, err)
	}
	if !slices.Contains(rc.(*runbookContext).RegisteredSecrets(), "db-secret") {
		t.Error("the password handed out is not in the run's masking set")
	}
	if rc.InjectSecrets()["password"] == "db-secret" {
		t.Error("the connection's password reached the device's secrets")
	}
}
