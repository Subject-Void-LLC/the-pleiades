//go:build linux

// The backup, restore and decommission commands at the binary level: the
// built controller, a real PostgreSQL server, PostgreSQL's own client
// programs, and real pseudo terminals.
package main_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/backup"
	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// backupGateEnv is the child environment for a command that reads DB_DSN.
func backupGateEnv(dsn string) []string {
	var env []string
	for _, kv := range setupEnv("") {
		if !strings.HasPrefix(kv, "DB_PATH=") {
			env = append(env, kv)
		}
	}
	return append(env, "DB_DSN="+dsn)
}

// backedUpServer starts PostgreSQL, stores one credential in it sealed under
// key, and backs it up with the package the command runs. It returns the
// server's DSN, the backup directory and the backup's file name.
func backedUpServer(t *testing.T, key []byte) (dsn, backups, file string) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a postgres container")
	}
	// The controller binary this gate runs finds pg_dump and pg_restore on
	// PATH, and the machine's default may be newer than the server: a newer
	// pg_restore sends 15 a setting it rejects (FAILURE_PATTERNS.md #292).
	// The backup image carries the server's own; here the host must match.
	testsupport.UsePostgresClientTools(t)
	for _, name := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not on PATH; the backup image carries it, and this test needs a copy here", name)
		}
	}
	ctx := context.Background()
	pg, err := testpg.Run(ctx, testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"), testpg.WithUsername("pleiades"), testpg.WithPassword("password"),
		testsupport.PostgresReady())
	if err != nil {
		_ = testcontainers.TerminateContainer(pg) // a failed start still returns its container
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	if dsn, err = pg.ConnectionString(ctx, "sslmode=disable"); err != nil {
		t.Fatal(err)
	}

	svc, err := crypto.NewEnvelopeService(key, crypto.DefaultKeyVersion, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct := client.CredentialType.Create().SetName("Custom API").SetKind("cloud").SetNamespace("custom_api").SetOrganization(org).SaveX(ctx)
	client.Credential.Create().SetName("api").SetOrganization(org).SetCredentialType(ct).
		SetInputs(map[string]string{"api_token": "a-stored-secret"}).SaveX(ctx)
	_ = client.Close()

	setupDir, backups := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(setupDir, ".env"), []byte("MASTER_ENCRYPTION_KEY="+crypto.EncodeKey(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := backup.Take(ctx, backup.Options{DSN: dsn, SetupDir: setupDir, BackupDir: backups}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Take() error = %v", err)
	}
	return dsn, backups, name.String()
}

// TestBackupReleaseGate_RestoreOnACleanMachineTakesTheKeyWithEchoOff runs
// the binary's restore on a pseudo terminal with no .env: it asks for the
// backup's key, naming it by fingerprint, reads it with echo off, restores,
// and writes the key to .env.
func TestBackupReleaseGate_RestoreOnACleanMachineTakesTheKeyWithEchoOff(t *testing.T) {
	key := []byte(strings.Repeat("g", 32))
	dsn, backups, file := backedUpServer(t, key)
	clean := t.TempDir()

	s := startPTYWith(t, backupGateEnv(dsn), "restore", "--dir", clean, "--backups", backups, "--file", file)
	s.waitFor(t, "Type or paste master encryption key "+keyregistry.Short(crypto.Fingerprint(key)))
	s.typeLine(t, crypto.EncodeKey(key), true)
	if code := s.wait(t); code != 0 {
		t.Fatalf("restore exited %d; the terminal showed:\n%s", code, s.shown())
	}
	if strings.Contains(s.shown(), crypto.EncodeKey(key)) {
		t.Fatal("the key typed at the prompt was shown on the terminal")
	}
	s.waitFor(t, "Restored the database pleiades from")
	env, err := os.ReadFile(filepath.Join(clean, ".env")) // #nosec G304 -- a path under t.TempDir()
	if err != nil || !strings.Contains(string(env), "MASTER_ENCRYPTION_KEY="+crypto.EncodeKey(key)+"\n") {
		t.Fatalf("the restore did not write the key to .env: %v", err)
	}

	// Without a terminal and without --key-stdin, it refuses and names the
	// flag, before loading anything.
	other := t.TempDir()
	cmd := exec.Command(binPath, "restore", "--dir", other, "--backups", backups, "--file", file) // #nosec G204 -- the test's own binary
	cmd.Env = backupGateEnv(dsn)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "--key-stdin") {
		t.Fatalf("a restore with no key and no terminal: %v\n%s", err, out)
	}

	// With --key-stdin it reads the key from standard input.
	cmd = exec.Command(binPath, "restore", "--dir", other, "--backups", backups, "--file", file, "--key-stdin") // #nosec G204 -- the test's own binary
	cmd.Env = backupGateEnv(dsn)
	cmd.Stdin = strings.NewReader(crypto.EncodeKey(key) + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restore --key-stdin: %v\n%s", err, out)
	}
	if strings.Contains(string(out), crypto.EncodeKey(key)) {
		t.Fatal("restore printed the key it read")
	}
}

// TestBackupReleaseGate_DecommissionTakesTheExactPhrase runs the binary's
// decommission on a pseudo terminal: it names what goes and what stays,
// including the key the backups need, and exits 0 only on the exact phrase.
func TestBackupReleaseGate_DecommissionTakesTheExactPhrase(t *testing.T) {
	key := []byte(strings.Repeat("h", 32))
	short := keyregistry.Short(crypto.Fingerprint(key))
	dir, backups := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MASTER_ENCRYPTION_KEY="+crypto.EncodeKey(key)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"decommission", "--dir", dir, "--backups", backups}

	s := startPTYWith(t, setupEnv(""), args...)
	s.waitFor(t, "THERE IS NO BACKUP")
	s.waitFor(t, `Type "decommission `+short+`"`)
	s.typeLine(t, "decommission", false)
	if code := s.wait(t); code != 1 || !strings.Contains(s.shown(), "Nothing was removed") {
		t.Fatalf("a near miss exited %d; the terminal showed:\n%s", code, s.shown())
	}

	name := "pleiades-20260918T141707Z-" + crypto.Fingerprint(key)[:8] + ".dump"
	if err := os.WriteFile(filepath.Join(backups, name), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s = startPTYWith(t, setupEnv(""), args...)
	s.waitFor(t, "1 of those backups needs key "+short)
	s.waitFor(t, `Type "decommission `+short+`"`)
	s.typeLine(t, "decommission "+short, false)
	if code := s.wait(t); code != 0 {
		t.Fatalf("the exact phrase exited %d; the terminal showed:\n%s", code, s.shown())
	}

	cmd := exec.Command(binPath, args...) // #nosec G204 -- the test's own binary
	cmd.Env = setupEnv("")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "--destroy-deployment") {
		t.Fatalf("decommission with no terminal and no flag: %v\n%s", err, out)
	}
	cmd = exec.Command(binPath, append(args, "--destroy-deployment")...) // #nosec G204 -- the test's own binary
	cmd.Env = setupEnv("")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("decommission --destroy-deployment: %v\n%s", err, out)
	}
}
