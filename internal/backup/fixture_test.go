// A real PostgreSQL server with a database this version's migrations built,
// holding a sealed value in every encrypted column, for the backup and
// restore tests.
package backup_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/crypto"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// secrets are the plaintexts seeded into each encrypted column, one per
// column so a value restored into the wrong one would show.
var secrets = map[string]string{
	"credential": "credential-secret-value",
	"device":     "device-secret-value",
	"survey":     "survey-secret-value",
	"mesh":       "SAMESHSEEDFORTESTINGONLYNOTAREALKEY",
}

// server is one PostgreSQL container, with the superuser login a compose
// stack's DB_DSN uses.
type server struct {
	dsn string
}

// requireTools skips unless pg_dump and pg_restore are on PATH.
//
// Production runs PostgreSQL 15.19's own programs from the backup image,
// and tests/e2e's backup gate runs that image. These tests run the programs
// this machine has at the server's major version: this package's TestMain
// puts them first on PATH (clienttools_internal_test.go), because a newer
// pg_restore sends a 15 server a setting it rejects.
func requireTools(t testing.TB) {
	t.Helper()
	for _, name := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not on PATH; the backup image carries it, and this test needs a copy here", name)
		}
	}
}

// startServer starts PostgreSQL at the pinned release with the database
// name, user and password a compose stack's DB_DSN names.
func startServer(t testing.TB) server {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a postgres container")
	}
	requireTools(t)
	ctx := context.Background()
	pg, err := testpg.Run(ctx, testsupport.PostgresImage,
		testpg.WithDatabase("pleiades"), testpg.WithUsername("pleiades"), testpg.WithPassword("password"),
		testsupport.PostgresReady())
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(pg) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("reading the postgres DSN: %v", err)
	}
	return server{dsn: dsn}
}

// installHooks wires every encrypted column's hook and interceptor, the set
// cmd/controller installs.
func installHooks(client *ent.Client, svc *crypto.EnvelopeService) {
	client.Device.Use(crypto.DeviceEnvelopePropertiesHook(svc))
	client.Device.Intercept(crypto.DeviceEnvelopePropertiesInterceptor(svc))
	client.SavedLaunchConfig.Use(crypto.SavedLaunchConfigAnswersHook(svc))
	client.SavedLaunchConfig.Intercept(crypto.SavedLaunchConfigAnswersInterceptor(svc))
	client.Credential.Use(crypto.CredentialInputsHook(svc))
	client.Credential.Intercept(crypto.CredentialInputsInterceptor(svc))
	client.MeshSigningKey.Use(crypto.MeshSigningKeySeedHook(svc))
	client.MeshSigningKey.Intercept(crypto.MeshSigningKeySeedInterceptor(svc))
}

// open opens the server's live database the way the controller does: the
// same migration runner, and every hook, under key with version.
func (s server) open(t testing.TB, key []byte, version string) *ent.Client {
	t.Helper()
	svc, err := crypto.NewEnvelopeService(key, version, nil, "")
	if err != nil {
		t.Fatalf("NewEnvelopeService() error = %v", err)
	}
	client, err := ent.OpenDatabase(context.Background(), ent.Config{DSN: s.dsn})
	if err != nil {
		t.Fatalf("OpenDatabase() error = %v", err)
	}
	installHooks(client, svc)
	return client
}

// seed writes one sealed value into every encrypted column under key and
// version, one job still running, and one live session.
func (s server) seed(t testing.TB, key []byte, version string) {
	t.Helper()
	ctx := context.Background()
	client := s.open(t, key, version)
	defer func() { _ = client.Close() }()

	org := client.Organization.Create().SetName("acme").SaveX(ctx)
	ct := client.CredentialType.Create().SetName("Custom API").SetKind("cloud").SetNamespace("custom_api").SetOrganization(org).SaveX(ctx)
	inv := client.Inventory.Create().SetName("all").SetOrganization(org).SaveX(ctx)
	tmpl := client.Template.Create().SetName("deploy").SetKind("runbook").SetDefinition("some-runbook").SetOrganization(org).SetInventory(inv).SaveX(ctx)
	client.Credential.Create().SetName("api").SetOrganization(org).SetCredentialType(ct).
		SetInputs(map[string]string{"api_token": secrets["credential"]}).SaveX(ctx)
	client.Device.Create().SetName("router-1").SetType("cisco_router").
		SetProperties(map[string]any{"enable_secret": secrets["device"]}).SaveX(ctx)
	client.SavedLaunchConfig.Create().SetName("nightly").SetTemplate(tmpl).
		SetAnswers(map[string]any{"survey_password": secrets["survey"]}).SaveX(ctx)
	client.MeshSigningKey.Create().SetKeyID("key-1").SetAccountSubject("ACCOUNT").SetPublicKey("APUBLICKEY").
		SetSeed(secrets["mesh"]).SaveX(ctx)

	client.Job.Create().SetJobID("job-in-flight").SetRunbookID("some-runbook").SetGroupName("all").
		SetActor("ada@example.com").SetState("running").SaveX(ctx)
	client.Job.Create().SetJobID("job-finished").SetRunbookID("some-runbook").SetGroupName("all").
		SetActor("ada@example.com").SetState("completed").SaveX(ctx)
	client.Session.Create().SetTokenHash([]byte("a-token-hash")).SetSubject("ada@example.com").SetRole("admin").
		SetCsrfKey([]byte("a-csrf-key")).SetIdleExpiresAt(time.Now().Add(time.Hour)).
		SetAbsoluteExpiresAt(time.Now().Add(time.Hour)).SaveX(ctx)
}

// readBack reads every seeded value through the controller's own read path
// under key and version, and reports which ones decrypted to what was
// seeded.
func (s server) readBack(t *testing.T, key []byte, version string) map[string]bool {
	t.Helper()
	ctx := context.Background()
	client := s.open(t, key, version)
	defer func() { _ = client.Close() }()
	ok := map[string]bool{}
	if c, err := client.Credential.Query().Only(ctx); err == nil {
		ok["credential"] = c.Inputs["api_token"] == secrets["credential"]
	}
	if d, err := client.Device.Query().Only(ctx); err == nil {
		ok["device"] = d.Properties["enable_secret"] == secrets["device"]
	}
	if c, err := client.SavedLaunchConfig.Query().Only(ctx); err == nil {
		ok["survey"] = c.Answers["survey_password"] == secrets["survey"]
	}
	if k, err := client.MeshSigningKey.Query().Only(ctx); err == nil {
		ok["mesh"] = k.Seed == secrets["mesh"]
	}
	return ok
}

// scalar runs a one-value query on the database named db, as the superuser.
func (s server) scalar(t *testing.T, db, query string, args ...any) string {
	t.Helper()
	dsn := strings.Replace(s.dsn, "/pleiades?", "/"+db+"?", 1)
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", db, err)
	}
	defer func() { _ = conn.Close() }()
	var out sql.NullString
	if err := conn.QueryRowContext(context.Background(), query, args...).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out.String
}

// exec runs a statement that returns no rows on the database named db, as
// the superuser.
func (s server) exec(t *testing.T, db, stmt string) {
	t.Helper()
	dsn := strings.Replace(s.dsn, "/pleiades?", "/"+db+"?", 1)
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("opening %s: %v", db, err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(context.Background(), stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// assertNoLeftovers fails if a restore left its scratch or reference
// database, the database it replaced, or its role behind.
func (s server) assertNoLeftovers(t *testing.T) {
	t.Helper()
	if got := s.scalar(t, "postgres", `SELECT string_agg(datname, ',') FROM pg_database WHERE datname LIKE 'pleiades\_%'`); got != "" {
		t.Errorf("databases left behind: %s", got)
	}
	if got := s.scalar(t, "postgres", `SELECT string_agg(rolname, ',') FROM pg_roles WHERE rolname LIKE 'pleiades\_%'`); got != "" {
		t.Errorf("roles left behind: %s", got)
	}
}

// dirs is one run's .env directory and backup directory.
type dirs struct {
	setup, backups string
}

// newDirs makes a setup directory holding a .env with the lines given, or
// none when lines is nil, and an empty backup directory.
func newDirs(t *testing.T, lines ...string) dirs {
	t.Helper()
	d := dirs{setup: t.TempDir(), backups: t.TempDir()}
	if lines != nil {
		if err := os.WriteFile(filepath.Join(d.setup, ".env"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("writing .env: %v", err)
		}
	}
	return d
}

// envKey is the .env line for key.
func envKey(key []byte) string { return "MASTER_ENCRYPTION_KEY=" + crypto.EncodeKey(key) }

// testKey is a key the tests seal under.
func testKey(b byte) []byte { return []byte(strings.Repeat(string(b), 32)) }
