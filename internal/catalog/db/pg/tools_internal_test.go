// Tests of the script a tool run sends: the password never reaches the
// command line, and the trusted roots it names are ones the tool reads.
package pg

import (
	"strconv"
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
		run := toolRun{conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app"}, database: "app", command: []string{"pg_dump"}, major: 16, timeout: d}
		script, err := run.script()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(script, want) {
			t.Errorf("a %s timeout: the script lacks %q:\n%s", d, want, script)
		}
	}
}

// TestToolRun_SystemRootsOnlyWhereLibpqReadsThem: a verify-full connection
// with no pinned CA names the device's own trusted roots
// (PGSSLROOTCERT=system) only for a program of 16 or later, always beside
// PGSSLMODE=verify-full, which libpq needs to verify against them; for an
// older one, whose libpq reads "system" as a file name, the script is
// refused, naming the connection, the program and its version. A pinned
// CA is used at any version, and a connection without TLS names no roots.
// The tool runs from its private directory, after the directory and its
// removal are set up.
func TestToolRun_SystemRootsOnlyWhereLibpqReadsThem(t *testing.T) {
	for _, tc := range []struct {
		name   string
		conn   wire.DatabaseConnection
		major  int
		roots  string // the PGSSLROOTCERT the script exports, "" for none
		refuse bool
	}{
		{name: "the default sslmode at 16", conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app"}, major: 16, roots: "system"},
		{name: "verify-full at 18", conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app", SSLMode: wire.SSLVerifyFull}, major: 18, roots: "system"},
		{name: "the default sslmode at 15", conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app"}, major: 15, refuse: true},
		{name: "verify-full at 9", conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app", SSLMode: wire.SSLVerifyFull}, major: 9, refuse: true},
		{name: "a pinned CA at 15", conn: wire.DatabaseConnection{Name: "appdb", Host: "10.0.0.5", User: "app", CAPEM: "-----BEGIN CERTIFICATE-----\n"}, major: 15, roots: `"$d/ca.pem"`},
		{name: "no TLS at 15", conn: wire.DatabaseConnection{Name: "appdb", Host: "127.0.0.1", User: "app", SSLMode: wire.SSLDisable}, major: 15},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, err := toolRun{conn: tc.conn, database: "app", command: []string{"pg_restore"}, major: tc.major, timeout: time.Minute}.script()
			if tc.refuse {
				if err == nil || !strings.Contains(err.Error(), "connection appdb") || !strings.Contains(err.Error(), "pg_restore reads only from PostgreSQL 16, and the device's is "+strconv.Itoa(tc.major)) || !strings.Contains(err.Error(), "ca_pem") {
					t.Fatalf("script = %v, want a refusal naming the connection, pg_restore, its version and ca_pem:\n%s", err, script)
				}
				if err := systemRootsUsable(tc.conn, "pg_restore", tc.major); err == nil {
					t.Error("systemRootsUsable lets through what the script refuses")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			exported := strings.Contains(script, "export PGSSLROOTCERT=")
			if tc.roots == "" && exported || tc.roots != "" && !strings.Contains(script, "export PGSSLROOTCERT="+tc.roots+"\n") {
				t.Errorf("want PGSSLROOTCERT=%q:\n%s", tc.roots, script)
			}
			if exported && !strings.Contains(script, "PGSSLMODE='verify-full'") {
				t.Errorf("roots without PGSSLMODE=verify-full:\n%s", script)
			}
			cd, trap, run := strings.Index(script, "cd \"$d\"\n"), strings.Index(script, "trap "), strings.Index(script, "timeout -k")
			if cd < 0 || !(trap < cd && cd < run) {
				t.Errorf("the tool does not run from its private directory, set up first:\n%s", script)
			}
		})
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
