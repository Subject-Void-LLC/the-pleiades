// Release gate for the db.pg methods: through the real binary, a device
// that is a real SSH server (in this process, its commands run by this
// machine's /bin/sh with its pg_dump and pg_restore) reaching a real
// PostgreSQL. A check of a runbook that dumps, restores, migrates and
// queries changes nothing; the same runbook run changes exactly that; the
// copy matches its source; a live database is refused; and the
// connection's password appears in no output.
package main_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestCLI_DatabaseCheckChangesNothingAndTheRunDoesTheWork is the release
// gate.
func TestCLI_DatabaseCheckChangesNothingAndTheRunDoesTheWork(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real PostgreSQL")
	}
	testsupport.UsePostgresClientTools(t)
	for _, name := range []string{"pg_dump", "pg_restore"} {
		_, err := exec.LookPath(name)
		testsupport.Require(t, name, err == nil, name+" is not on PATH; the device this gate stands in for runs it")
	}
	ctx := context.Background()
	const password = "gate-db-password-7Lm2"
	pgc, err := testpg.Run(ctx, testsupport.PostgresImage,
		testpg.WithDatabase("app"), testpg.WithUsername("admin"), testpg.WithPassword(password), testsupport.PostgresReady())
	testcontainers.CleanupContainer(t, pgc)
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	dbHost, err := pgc.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := pgc.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dbPort := int(mapped.Num())
	admin := func(database string) *pgclient.Session {
		s, err := pgclient.Open(ctx, pgclient.Target{Host: dbHost, Port: dbPort, User: "admin", Password: password, Database: database,
			Dial: func(ctx context.Context) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "tcp", net.JoinHostPort(dbHost, strconv.Itoa(dbPort)))
			}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	value := func(database, q string) string {
		s := admin(database)
		defer func() { _ = s.Close() }()
		var v string
		if err := s.DB().QueryRowContext(ctx, q).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	setup := admin("app")
	for _, stmt := range []string{"CREATE TABLE orders (id int PRIMARY KEY)", "INSERT INTO orders SELECT generate_series(1, 40)"} {
		if _, err := setup.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	_ = setup.Close()

	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "db1", "--type", "generic_ssh", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
		{"add-credential", "db1", "--username", srv.Username, "--password", srv.Password},
		{"onboard", "db1"},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if out, err := runPleiadesWithStdin(t, dir, password+"\n", "add-database-connection", "appdb", "--host", dbHost, "--port", strconv.Itoa(dbPort), "--user", "admin", "--sslmode", "disable", "--live", "app", "--password-stdin"); err != nil {
		t.Fatalf("add-database-connection: %v\n%s", err, out)
	}

	files := t.TempDir()
	existing := filepath.Join(files, "existing.dump")
	newDump := filepath.Join(files, "new.dump")
	migrations := filepath.Join(files, "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"1_notes.sql": "CREATE TABLE notes (id int);", "2_note.sql": "INSERT INTO notes VALUES (1);"} {
		if err := os.WriteFile(filepath.Join(migrations, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report := func(args ...string) (jsonReport, string) {
		t.Helper()
		out, _ := runPleiadesWithEnv(t, dir, env, append(args, "--json")...)
		var r jsonReport
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&r); err != nil {
			t.Fatalf("pleiades %v printed %q: %v", args, out, err)
		}
		return r, out
	}
	var outputs []string
	task := func(name, method, body string) string {
		return "  - name: " + name + "\n    " + method + ":\n      connection: appdb\n" + body
	}
	first := writeRunbook(t, dir, "dumpfirst", "id: dumpfirst\nhosts: db1\ntasks:\n"+task("dump", "db.pg.dump", "      database: app\n      dest: "+existing+"\n"))
	if r, out := report("run", first); r.Outcome.ExitCode != 0 {
		t.Fatalf("the first dump: %+v\n%s", r.Tasks, out)
	} else {
		outputs = append(outputs, out)
	}

	work := writeRunbook(t, dir, "dbwork", "id: dbwork\nhosts: db1\ntasks:\n"+
		task("dump", "db.pg.dump", "      database: app\n      dest: "+newDump+"\n")+
		task("restore", "db.pg.restore", "      database: app_copy\n      src: "+existing+"\n      create: true\n")+
		task("migrate", "db.pg.migrate", "      database: app\n      dir: "+migrations+"\n")+
		task("read", "db.pg.query", "      database: app\n      sql: SELECT count(*) AS orders FROM orders\n      allow_privileged_role: true\n"))
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", work); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}

	// The check: every task checked, nothing changed.
	checked, out := report("run", work, "--mode", "check")
	outputs = append(outputs, out)
	if checked.Outcome.ExitCode != 0 || checked.Outcome.Unchecked != 0 || len(checked.Tasks) != 4 {
		t.Fatalf("the check: %+v %+v\n%s", checked.Outcome, checked.Tasks, out)
	}
	for i, want := range []string{"would_change", "would_change", "would_change", "ok"} {
		if checked.Tasks[i].Status != want {
			t.Errorf("the check of %s is %s, want %s", checked.Tasks[i].Name, checked.Tasks[i].Status, want)
		}
	}
	if _, err := os.Stat(newDump); !os.IsNotExist(err) {
		t.Errorf("a check of the dump wrote %s: %v", newDump, err)
	}
	if got := value("postgres", "SELECT count(*) FROM pg_database WHERE datname = 'app_copy'"); got != "0" {
		t.Error("a check of the restore created app_copy")
	}
	if got := value("app", "SELECT to_regclass('public.pleiades_migrations') IS NULL AND to_regclass('public.notes') IS NULL"); got != "true" {
		t.Error("a check of the migration applied something")
	}

	// The run does exactly the work.
	ran, out := report("run", work)
	outputs = append(outputs, out)
	if ran.Outcome.ExitCode != 0 || len(ran.Tasks) != 4 {
		t.Fatalf("the run: %+v\n%s", ran.Tasks, out)
	}
	for i, want := range []string{"changed", "changed", "changed", "ok"} {
		if ran.Tasks[i].Status != want {
			t.Errorf("the run's %s is %s, want %s", ran.Tasks[i].Name, ran.Tasks[i].Status, want)
		}
	}
	if _, err := os.Stat(newDump); err != nil {
		t.Errorf("the dump wrote nothing: %v", err)
	}
	if got := value("app", "SELECT count(*) FROM notes"); got != "1" {
		t.Errorf("the migration left %s rows in notes", got)
	}
	if csv, _ := ran.Tasks[3].Stats["csv"].(string); csv != "orders\r\n40\r\n" {
		t.Errorf("the query returned %q", csv)
	}

	// The copy matches its source at the moment of the dump.
	verify := writeRunbook(t, dir, "dbverify", "id: dbverify\nhosts: db1\ntasks:\n"+task("compare", "db.pg.rowcounts", "      database: app_copy\n      compare:\n        database: app\n      exclude: [public.notes, public.pleiades_migrations]\n"))
	if r, out := report("run", verify); r.Outcome.ExitCode != 0 {
		t.Errorf("the copy differs: %+v\n%s", r.Tasks, out)
	} else {
		outputs = append(outputs, out)
	}

	// A live database is not restored over.
	live := writeRunbook(t, dir, "dblive", "id: dblive\nhosts: db1\ntasks:\n"+task("over", "db.pg.restore", "      database: app\n      src: "+existing+"\n      clean: true\n"))
	r, out := report("run", live)
	outputs = append(outputs, out)
	if r.Outcome.ExitCode == 0 || !strings.Contains(r.Tasks[0].Error, "connection appdb lists it as live") {
		t.Errorf("a restore over the live database: %+v", r.Tasks)
	}

	for _, out := range outputs {
		if strings.Contains(out, password) {
			t.Fatal("the connection's password appeared in a run's output")
		}
	}
	if doc, err := runPleiades(t, dir, "doc", "db.pg.restore"); err != nil || !strings.Contains(doc, "refusing a live one unless told why") {
		t.Errorf("pleiades doc db.pg.restore: %v\n%s", err, doc)
	}
}
