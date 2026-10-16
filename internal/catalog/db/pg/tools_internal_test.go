// Tests that a tool run's password never reaches the command line.
package pg

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestToolRun_ThePasswordTravelsOnStdinOnly: the script the device runs
// (which its shell holds as an argument, visible in the process list)
// carries no password and no CA, clears the login's PG variables, makes
// its private directory and the trap that removes it, and only then reads
// the password file's line from standard input and writes it there with
// the shell's own printf. The tools find it through PGPASSFILE, as
// PostgreSQL recommends over PGPASSWORD, so no process's environment
// holds the password. A CA follows the line on standard input.
func TestToolRun_ThePasswordTravelsOnStdinOnly(t *testing.T) {
	const secret = "pw-sentinel-9QzT"
	run := toolRun{
		conn:     wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", Port: 6432, User: "app", Password: secret, CAPEM: "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n", ServerName: "db.internal"},
		database: "app", command: []string{"pg_dump", "--file=/tmp/x"}, timeout: time.Hour,
	}
	script, err := run.script()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, secret) || strings.Contains(script, "BEGIN CERTIFICATE") {
		t.Fatalf("the script carries the password or the CA:\n%s", script)
	}
	clear := strings.Index(script, "unset")
	trap := strings.Index(script, "trap ")
	read := strings.Index(script, "IFS= read -r line")
	write := strings.Index(script, `printf '%s\n' "$line" > "$d/pgpass"`)
	if clear < 0 || trap < 0 || read < 0 || write < 0 || !(clear < trap && trap < read && read < write) {
		t.Errorf("the PG variables are not cleared, then the directory's removal set, before the password is read and written:\n%s", script)
	}
	if strings.Contains(script, "PGPASSWORD") {
		t.Errorf("the script puts the password in an environment:\n%s", script)
	}
	for _, want := range []string{"PGHOST='db.internal'", "PGHOSTADDR='10.0.0.5'", "PGPORT=6432", "PGSSLMODE='verify-full'", `PGSSLROOTCERT="$d/ca.pem"`, `PGPASSFILE="$d/pgpass"`, "umask 077", "timeout -k 10s 3600s "} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if stdin := run.stdin(); !strings.HasPrefix(stdin, "*:*:*:app:"+secret+"\n-----BEGIN CERTIFICATE-----") {
		t.Errorf("stdin = %q", stdin)
	}
	run.conn.Host = "db.example"
	if _, err := run.script(); err == nil || !strings.Contains(err.Error(), "must be an address") {
		t.Errorf("server_name with a host name = %v", err)
	}
}

// TestToolRun_ATimeoutUnderASecondStillBounds: timeout(1) reads 0 as no
// limit at all, so a timeout under a second rounds up to one, never down
// to none.
func TestToolRun_ATimeoutUnderASecondStillBounds(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Millisecond:             "timeout -k 10s 1s ",
		999 * time.Millisecond:       "timeout -k 10s 1s ",
		time.Second:                  "timeout -k 10s 1s ",
		1500 * time.Millisecond:      "timeout -k 10s 2s ",
		time.Hour + time.Millisecond: "timeout -k 10s 3601s ",
	} {
		run := toolRun{conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app"}, database: "app", command: []string{"pg_dump"}, timeout: d}
		script, err := run.script()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(script, want) {
			t.Errorf("a %s timeout: the script lacks %q:\n%s", d, want, script)
		}
	}
}

// TestToolRun_RefusesAPasswordStdinCannotCarry: the password is read as
// the first line of standard input, so one holding a line break would be
// cut there and its rest written into the pinned CA's file; it is refused
// instead, naming the connection and never the password.
func TestToolRun_RefusesAPasswordStdinCannotCarry(t *testing.T) {
	for _, pw := range []string{"first\nsecond", "nul\x00byte"} {
		run := toolRun{conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app", Password: pw, CAPEM: "-----BEGIN CERTIFICATE-----\n"}, database: "app", command: []string{"pg_dump"}, timeout: time.Hour}
		_, err := run.script()
		if err == nil || !strings.Contains(err.Error(), "connection appdb") || strings.Contains(err.Error(), "second") || strings.Contains(err.Error(), "byte") {
			t.Errorf("password %q: %v", pw, err)
		}
	}
}

// TestConninfoDatabase_IsOnlyEverAName: pg_restore reads --dbname as a
// connection string when it holds "=" or begins with a URI prefix, so a
// name is always handed over quoted, as the dbname of a connection string
// naming nothing else; the host, port, user and TLS settings stay the
// ones exported beside it.
func TestConninfoDatabase_IsOnlyEverAName(t *testing.T) {
	for name, want := range map[string]string{
		"app":                               `dbname='app'`,
		"host=evil.example sslmode=disable": `dbname='host=evil.example sslmode=disable'`,
		"postgresql://evil.example/x":       `dbname='postgresql://evil.example/x'`,
		`o'b\x`:                             `dbname='o\'b\\x'`,
	} {
		if got := conninfoDatabase(name); got != want {
			t.Errorf("conninfoDatabase(%q) = %s, want %s", name, got, want)
		}
	}
}

// TestPgpassLine: the password file's line matches any host, port and
// database for the connection's own user, with ":" and "\" escaped as
// libpq reads them; no password leaves the file empty, which libpq reads
// as no match, so the tool asks the server for none.
func TestPgpassLine(t *testing.T) {
	for _, tc := range []struct{ user, password, want string }{
		{"app", "pw", `*:*:*:app:pw`},
		{"app", `a:b\c`, `*:*:*:app:a\:b\\c`},
		{`we:ird`, "pw", `*:*:*:we\:ird:pw`},
		{"app", "", ""},
	} {
		if got := pgpassLine(wire.DatabaseConnection{User: tc.user, Password: tc.password}); got != tc.want {
			t.Errorf("pgpassLine(%q, %q) = %s, want %s", tc.user, tc.password, got, tc.want)
		}
	}
}
