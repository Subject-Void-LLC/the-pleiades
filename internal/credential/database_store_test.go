// Tests for named database connections in the credential file.
package credential_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestDatabaseConnections_SaveListLookupRemove: a connection is stored
// with only its password encrypted (the file never holds it in the
// clear), listed without it, looked up with it, refused when no run could
// use it, and removed.
func TestDatabaseConnections_SaveListLookupRemove(t *testing.T) {
	dir := t.TempDir()
	key, err := credential.ResolveMasterKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	conn := wire.DatabaseConnection{Name: "appdb", Host: "db.internal", Port: 6432, User: "app", Password: "db-secret-value", ServerName: "db.example.com", Live: []string{"app", "billing"}}
	if err := credential.SaveDatabaseConnection(dir, key, conn); err != nil {
		t.Fatal(err)
	}
	if err := credential.SaveDatabaseConnection(dir, key, wire.DatabaseConnection{Name: "local", Host: "/var/run/postgresql", User: "postgres", SSLMode: wire.SSLDisable}); err != nil {
		t.Fatal(err)
	}
	var raw string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p) // #nosec G304 -- this test's own directory
			raw += string(b)
		}
		return nil
	})
	if strings.Contains(raw, "db-secret-value") {
		t.Fatal("the password is in the project's files in the clear")
	}

	list, err := credential.ListDatabaseConnections(dir)
	if err != nil || len(list) != 2 || list[0].Name != "appdb" || list[0].Password != "" || list[0].Port != 6432 || list[1].Name != "local" {
		t.Fatalf("List = %+v, %v", list, err)
	}
	got, ok, err := credential.NewLazyFileStore(dir).(credential.DatabaseStore).LookupDatabase(context.Background(), "appdb")
	if err != nil || !ok || got.Password != "db-secret-value" || got.ServerName != "db.example.com" || !got.IsLive("billing") {
		t.Fatalf("Lookup = %+v, %v, %v", got, ok, err)
	}

	for name, bad := range map[string]wire.DatabaseConnection{
		"plaintext across a network": {Name: "x", Host: "db.internal", User: "u", SSLMode: wire.SSLDisable},
		"TLS to a socket":            {Name: "x", Host: "/run/postgresql", User: "u"},
		"a bad name":                 {Name: "App DB", Host: "h", User: "u"},
		"no user":                    {Name: "x", Host: "h"},
		"a CA that is not one":       {Name: "x", Host: "h", User: "u", CAPEM: "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"},
		"an unknown mode":            {Name: "x", Host: "h", User: "u", SSLMode: "prefer"},
		"insecure allowing nothing":  {Name: "x", Host: "localhost", User: "u", SSLMode: wire.SSLDisable, InsecureTransport: true},
	} {
		if err := credential.SaveDatabaseConnection(dir, key, bad); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	if err := credential.SaveDatabaseConnection(dir, key, wire.DatabaseConnection{Name: "remote", Host: "10.0.0.5", User: "u", SSLMode: wire.SSLDisable, InsecureTransport: true}); err != nil {
		t.Errorf("plaintext asked for by name: %v", err)
	}

	if removed, err := credential.RemoveDatabaseConnection(dir, "appdb"); err != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, err)
	}
	if removed, err := credential.RemoveDatabaseConnection(dir, "appdb"); err != nil || removed {
		t.Errorf("a second Remove = %v, %v", removed, err)
	}
	if _, ok, _ := credential.NewLazyFileStore(dir).(credential.DatabaseStore).LookupDatabase(context.Background(), "appdb"); ok {
		t.Error("a removed connection was found")
	}
}
