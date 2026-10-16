// The database connection store's refusals and failures: each is
// reported, never a silent absent connection or an empty password.
package credential

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestSaveDatabaseConnection_Refusals: a key that is not a key and a
// credential file that does not parse are refused, and the refused save
// writes nothing.
func TestSaveDatabaseConnection_Refusals(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	conn := wire.DatabaseConnection{Name: "appdb", Host: "db.internal", User: "app", Password: "db-secret"}
	dir := t.TempDir()
	if err := SaveDatabaseConnection(dir, []byte("short"), conn); err == nil {
		t.Error("a key that is not a key was used")
	}
	if _, err := os.Stat(credentialsPath(dir)); !os.IsNotExist(err) {
		t.Errorf("a refused save wrote the credential file: %v", err)
	}

	corrupt := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(credentialsPath(corrupt)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialsPath(corrupt), []byte("databases: [not, a, map"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveDatabaseConnection(corrupt, key, conn); err == nil {
		t.Error("a save over a credential file that does not parse succeeded")
	}
	if _, err := ListDatabaseConnections(corrupt); err == nil {
		t.Error("listing a credential file that does not parse succeeded")
	}
}

// TestLookupDatabase_Failures: a password that does not decrypt, a
// credential file that does not parse, and a master key that cannot be
// resolved are errors, not an absent connection, and none names the
// password.
func TestLookupDatabase_Failures(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	dir := t.TempDir()
	if err := SaveDatabaseConnection(dir, key, wire.DatabaseConnection{Name: "appdb", Host: "db.internal", User: "app", Password: "db-secret"}); err != nil {
		t.Fatal(err)
	}

	other, err := NewFileStore(dir, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	_, ok, err := other.(DatabaseStore).LookupDatabase(context.Background(), "appdb")
	if err == nil || ok || !strings.Contains(err.Error(), "decrypt the password of connection appdb") || strings.Contains(err.Error(), "db-secret") {
		t.Errorf("a password under another key: ok %v, %v; want a decrypt failure naming the connection", ok, err)
	}

	if err := os.WriteFile(credentialsPath(dir), []byte("databases: [not, a, map"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(dir, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.(DatabaseStore).LookupDatabase(context.Background(), "appdb"); err == nil || ok {
		t.Errorf("a credential file that does not parse: ok %v, %v", ok, err)
	}

	t.Setenv(masterKeyEnvVar, "not a base64 key!")
	lazy := NewLazyFileStore(t.TempDir())
	if _, ok, err := lazy.(DatabaseStore).LookupDatabase(context.Background(), "appdb"); err == nil || ok {
		t.Errorf("an unresolvable master key: ok %v, %v", ok, err)
	}
}
