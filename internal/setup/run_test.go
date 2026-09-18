// Tests for setup runs without a terminal, against real files and databases.
package setup_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/keyregistry"
	"github.com/Subject-Void-LLC/the-pleiades/internal/setup"
)

// These tests run setup.Run the way the controller's setup command does
// without a terminal, against real files and real SQLite databases seeded
// through the real encryption hooks. The terminal half is interact_test.go.

// runCompose runs the compose target in dir and returns what it printed.
func runCompose(t *testing.T, dir string, opts setup.Options) (setup.Result, string, error) {
	t.Helper()
	opts.Target, opts.Dir = setup.TargetCompose, dir
	var out bytes.Buffer
	result, err := setup.Run(context.Background(), opts, nil, &out)
	return result, out.String(), err
}

// readEnv parses the .env file in dir.
func readEnv(t *testing.T, dir string) (*setup.EnvFile, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, setup.ComposeFile))
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	f, err := setup.ParseEnvFile(data)
	if err != nil {
		t.Fatalf("parsing .env: %v", err)
	}
	return f, data
}

// seedCredential writes one credential into a migrated SQLite database at
// dsn, encrypted under key through the real credential hook.
func seedCredential(t *testing.T, dsn string, key []byte) {
	t.Helper()
	ctx := context.Background()
	svc, err := crypto.NewEnvelopeService(key, "v1", nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client, err := ent.OpenDatabase(ctx, ent.Config{DSN: dsn})
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	defer func() { _ = client.Close() }()
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct := client.CredentialType.Create().SetName("Custom").SetKind("cloud").SetNamespace("custom").SetOrganization(org).SaveX(ctx)
	client.Credential.Create().SetName("api").SetOrganization(org).SetCredentialType(ct).
		SetInputs(map[string]string{"token": "a-real-looking-token"}).SaveX(ctx)
}

// refusalOf returns err's message if it is a Refusal, and fails otherwise.
func refusalOf(t *testing.T, err error) string {
	t.Helper()
	var r *setup.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("error = %v (%T), want a *setup.Refusal", err, err)
	}
	return r.Message
}

// TestRun_FirstWriteWithNoDatabaseWritesAllThreeAndSaysNothingWasCounted is
// the first-install path with no database configured.
func TestRun_FirstWriteWithNoDatabaseWritesAllThreeAndSaysNothingWasCounted(t *testing.T) {
	dir := t.TempDir()
	result, out, err := runCompose(t, dir, setup.Options{})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	f, data := readEnv(t, dir)
	raw, ok := f.Get(setup.VarMasterKey)
	key, decodeErr := crypto.DecodeKey(raw, "test")
	if !ok || decodeErr != nil || !bytes.Equal(key, result.Key) {
		t.Fatalf("the written key does not match the one Run returned")
	}
	if jwt, ok := f.Get(setup.VarJWTSecret); !ok || len(jwt) < 32 {
		t.Fatalf("JWT_SECRET = %q, want a secret of at least 32 bytes", jwt)
	}
	if budget, _ := f.Get(setup.VarMaxOutage); budget != "30m" {
		t.Fatalf("PLEIADES_MAX_OUTAGE = %q, want the default 30m", budget)
	}
	if info, _ := os.Stat(filepath.Join(dir, setup.ComposeFile)); info.Mode().Perm() != 0o600 {
		t.Fatalf(".env was written at %v, want 0600", info.Mode().Perm())
	}
	if result.Possession != keyregistry.PossessionNotChecked {
		t.Fatalf("Possession = %q, want not_checked for a run without a terminal", result.Possession)
	}
	for _, want := range []string{"counted nothing", "no possession check ran", keyregistry.Short(crypto.Fingerprint(key))} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, raw) {
		t.Fatal("a run without a terminal printed the key")
	}
	if !strings.Contains(string(data), "Written by pleiades-controller setup") {
		t.Error("a new .env carries no note saying what wrote it")
	}
}

// TestRun_ARerunRefusesAndNamesTheFileItWouldDestroy is the release gate's
// re-run property, at the package level.
func TestRun_ARerunRefusesAndNamesTheFileItWouldDestroy(t *testing.T) {
	dir := t.TempDir()
	first, _, err := runCompose(t, dir, setup.Options{})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	_, before := readEnv(t, dir)

	_, _, err = runCompose(t, dir, setup.Options{})
	msg := refusalOf(t, err)
	for _, want := range []string{filepath.Join(dir, ".env"), keyregistry.Short(crypto.Fingerprint(first.Key)), "irreversible", "wrote nothing"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the re-run refusal does not name %q:\n%s", want, msg)
		}
	}
	if _, after := readEnv(t, dir); !bytes.Equal(before, after) {
		t.Fatal("a refused re-run changed the file")
	}
}

// TestRun_ReversibleChangesTakeForceAndTouchOnlyTheNamedField covers the
// ordinary --force: it applies only to the field named with it.
func TestRun_ReversibleChangesTakeForceAndTouchOnlyTheNamedField(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCompose(t, dir, setup.Options{}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	f, _ := readEnv(t, dir)
	key0, _ := f.Get(setup.VarMasterKey)
	jwt0, _ := f.Get(setup.VarJWTSecret)

	if _, _, err := runCompose(t, dir, setup.Options{NewJWTSecret: true}); !strings.Contains(refusalOf(t, err), "--force") {
		t.Fatalf("--new-jwt-secret without --force was not refused for want of --force: %v", err)
	}
	if _, _, err := runCompose(t, dir, setup.Options{NewJWTSecret: true, Force: true}); err != nil {
		t.Fatalf("--new-jwt-secret --force error = %v", err)
	}
	f, _ = readEnv(t, dir)
	if key, _ := f.Get(setup.VarMasterKey); key != key0 {
		t.Fatal("replacing the JWT secret changed the master key")
	}
	if jwt, _ := f.Get(setup.VarJWTSecret); jwt == jwt0 {
		t.Fatal("--new-jwt-secret --force left the JWT secret as it was")
	}

	if _, out, err := runCompose(t, dir, setup.Options{MaxOutage: "10m", Force: true}); err != nil || !strings.Contains(out, "lowers PLEIADES_MAX_OUTAGE from 30m to 10m") {
		t.Fatalf("lowering the budget: err = %v, output does not state the lowering:\n%s", err, out)
	}
	f, _ = readEnv(t, dir)
	if budget, _ := f.Get(setup.VarMaxOutage); budget != "10m" {
		t.Fatalf("PLEIADES_MAX_OUTAGE = %q, want 10m", budget)
	}
}

// TestRun_KeepsAnOperatorsOwnLines proves setup adds to a .env an operator
// already keeps rather than replacing it.
func TestRun_KeepsAnOperatorsOwnLines(t *testing.T) {
	dir := t.TempDir()
	mine := "# my settings\nCOMPOSE_PROFILES=ops\nOTHER = spaced # note\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(mine), 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if _, _, err := runCompose(t, dir, setup.Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	_, data := readEnv(t, dir)
	if !strings.HasPrefix(string(data), mine) {
		t.Fatalf("the operator's lines did not survive at the top of the file:\n%s", data)
	}
}

// TestRun_FirstWriteOverAnEncryptedDatabaseIsRefused proves a new key is
// never written over a database that already holds encrypted data, which is
// the path a surviving compose volume takes.
func TestRun_FirstWriteOverAnEncryptedDatabaseIsRefused(t *testing.T) {
	cases := map[string]struct {
		key  []byte
		want string
	}{
		"under an unknown key":    {key: []byte(strings.Repeat("u", 32)), want: "put the key it was written under back"},
		"under the published key": {key: setup.PublishedComposeKey(), want: "That key is public"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dbDir, dir := t.TempDir(), t.TempDir()
			dsn := "sqlite://" + filepath.Join(dbDir, "controller.db")
			seedCredential(t, dsn, tc.key)

			_, _, err := runCompose(t, dir, setup.Options{DatabaseDSN: dsn})
			msg := refusalOf(t, err)
			for _, want := range []string{"1 credential", "permanently unreadable", tc.want, "docker compose down --volumes"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not say %q:\n%s", want, msg)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
				t.Fatal("a refused first write created .env")
			}
		})
	}
}

// TestRun_ReplacingAKeyThatProtectsDataIsRefusedWhateverTheFlag proves the
// destructive flag does not override the census.
func TestRun_ReplacingAKeyThatProtectsDataIsRefusedWhateverTheFlag(t *testing.T) {
	dbDir, dir := t.TempDir(), t.TempDir()
	dsn := "sqlite://" + filepath.Join(dbDir, "controller.db")
	first, _, err := runCompose(t, dir, setup.Options{DatabaseDSN: dsn})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	seedCredential(t, dsn, first.Key)
	_, before := readEnv(t, dir)

	_, _, err = runCompose(t, dir, setup.Options{DatabaseDSN: dsn, DestroyKey: true})
	msg := refusalOf(t, err)
	for _, want := range []string{"1 credential", "does not override this", "ROTATE_ENCRYPTION_KEYS=true"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not say %q:\n%s", want, msg)
		}
	}
	if _, after := readEnv(t, dir); !bytes.Equal(before, after) {
		t.Fatal("a refused replacement changed the file")
	}

	if _, _, err := runCompose(t, dir, setup.Options{DestroyKey: true}); !strings.Contains(refusalOf(t, err), "does not replace a key without counting first") {
		t.Fatalf("a replacement with no database configured was not refused for want of a count: %v", err)
	}
}

// TestRun_ReplacingAKeyThatProtectsNothingGoesAheadOnTheNamedFlag covers
// the one replacement the census permits, taken without a terminal.
func TestRun_ReplacingAKeyThatProtectsNothingGoesAheadOnTheNamedFlag(t *testing.T) {
	dbDir, dir := t.TempDir(), t.TempDir()
	dsn := "sqlite://" + filepath.Join(dbDir, "controller.db")
	first, _, err := runCompose(t, dir, setup.Options{DatabaseDSN: dsn})
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	second, _, err := runCompose(t, dir, setup.Options{DatabaseDSN: dsn, DestroyKey: true})
	if err != nil {
		t.Fatalf("replacing an unused key error = %v", err)
	}
	if bytes.Equal(first.Key, second.Key) || second.Key == nil {
		t.Fatal("the key was not replaced")
	}
	if _, err := os.Stat(strings.TrimPrefix(dsn, "sqlite://")); !os.IsNotExist(err) {
		t.Fatal("counting a database that did not exist created it")
	}
}

// TestRun_AnUnreachableDatabaseIsARefusalNotAnEmptyCount proves setup never
// treats "could not read" as "holds nothing".
func TestRun_AnUnreachableDatabaseIsARefusalNotAnEmptyCount(t *testing.T) {
	dir := t.TempDir()
	_, _, err := runCompose(t, dir, setup.Options{DatabaseDSN: "postgres://pleiades:pw@127.0.0.1:1/pleiades?sslmode=disable"})
	if !strings.Contains(refusalOf(t, err), "cannot read the configured database") {
		t.Fatalf("refusal = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); !os.IsNotExist(err) {
		t.Fatal("setup wrote a key without being able to count")
	}
}

// TestRun_CheckReportsCompleteIncompleteAndUnreadable pins --check's three
// answers, which is what make up branches on.
func TestRun_CheckReportsCompleteIncompleteAndUnreadable(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runCompose(t, dir, setup.Options{Check: true}); !errors.Is(err, setup.ErrIncomplete) {
		t.Fatalf("--check with no file error = %v, want ErrIncomplete", err)
	}
	if _, _, err := runCompose(t, dir, setup.Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, _, err := runCompose(t, dir, setup.Options{Check: true}); err != nil {
		t.Fatalf("--check on a complete file error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MASTER_ENCRYPTION_KEY=\"quoted\"\n"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if _, _, err := runCompose(t, dir, setup.Options{Check: true}); err == nil || errors.Is(err, setup.ErrIncomplete) {
		t.Fatalf("--check on an unreadable file error = %v, want a refusal that is not ErrIncomplete", err)
	}
}
