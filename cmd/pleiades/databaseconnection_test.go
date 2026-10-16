// Tests for the database connection commands through the real binary.
package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLI_DatabaseConnections stores a connection with its password from
// standard input, lists it without the password (in text and JSON),
// refuses one that would cross a network in the clear unless asked for by
// name, and removes it; no project file ever holds the password in the
// clear.
func TestCLI_DatabaseConnections(t *testing.T) {
	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	const secret = "db-password-under-test"
	if out, err := runPleiadesWithStdin(t, dir, secret+"\n", "add-database-connection", "appdb", "--host", "db.internal", "--port", "6432", "--user", "app", "--live", "app, billing", "--password-stdin"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	// Read while the connection holding the password is stored: after its
	// removal no file could hold it, encrypted or not.
	noFileHolds(t, dir, secret)
	if out, err := runPleiades(t, dir, "add-database-connection", "local", "--host", "/var/run/postgresql", "--user", "postgres", "--sslmode", "disable", "--no-password"); err != nil {
		t.Fatalf("add a socket connection: %v\n%s", err, out)
	}
	out, err := runPleiades(t, dir, "add-database-connection", "remote", "--host", "10.0.0.5", "--user", "u", "--sslmode", "disable", "--no-password")
	if err == nil || !strings.Contains(out, "in the clear; set insecure_transport") {
		t.Fatalf("plaintext across a network: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-database-connection", "remote", "--host", "10.0.0.5", "--user", "u", "--sslmode", "disable", "--no-password", "--insecure-transport"); err != nil || !strings.Contains(out, "in the clear") {
		t.Fatalf("plaintext asked for by name: %v\n%s", err, out)
	}

	text, err := runPleiades(t, dir, "list-database-connections")
	if err != nil || !strings.Contains(text, "appdb  app@db.internal:6432  verify-full; live: app, billing") || !strings.Contains(text, "INSECURE TRANSPORT") {
		t.Fatalf("list: %v\n%s", err, text)
	}
	raw, err := runPleiades(t, dir, "list-database-connections", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var views []map[string]any
	if err := json.NewDecoder(strings.NewReader(raw)).Decode(&views); err != nil || len(views) != 3 || views[0]["name"] != "appdb" {
		t.Fatalf("list --json: %v\n%s", err, raw)
	}
	for _, v := range views {
		if _, has := v["password"]; has {
			t.Errorf("list --json shows a password field: %v", v)
		}
	}

	if out, err := runPleiades(t, dir, "remove-database-connection", "appdb"); err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "remove-database-connection", "appdb"); err == nil || !strings.Contains(out, "no database connection named appdb") {
		t.Fatalf("a second remove: %v\n%s", err, out)
	}
	for _, s := range []string{raw, text} {
		if strings.Contains(s, secret) {
			t.Error("the password was printed")
		}
	}
}

// noFileHolds fails t if any file under dir holds secret in the clear, or
// if there was no file to read, which would make the search prove nothing.
func noFileHolds(t *testing.T, dir, secret string) {
	t.Helper()
	read := 0
	if err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) // #nosec G304 -- this test's own project
		if err != nil {
			return err
		}
		read++
		if strings.Contains(string(b), secret) {
			t.Errorf("%s holds the password in the clear", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if read == 0 {
		t.Fatal("the project holds no file to search")
	}
}
