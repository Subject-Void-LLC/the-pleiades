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
// carries no password and no CA, clears the login's PG variables before
// reading the password, and reads it from standard input, whose first
// line it is; a CA follows it there.
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
	read := strings.Index(script, "read -r PGPASSWORD")
	if clear < 0 || read < 0 || clear > read {
		t.Errorf("the login's PG variables are not cleared before the password is read:\n%s", script)
	}
	for _, want := range []string{"PGHOST='db.internal'", "PGHOSTADDR='10.0.0.5'", "PGPORT=6432", "PGSSLMODE='verify-full'", `PGSSLROOTCERT="$d/ca.pem"`, "PGPASSFILE=/dev/null", "timeout -k 10s 3600s "} {
		if !strings.Contains(script, want) {
			t.Errorf("the script lacks %q:\n%s", want, script)
		}
	}
	if stdin := run.stdin(); !strings.HasPrefix(stdin, secret+"\n-----BEGIN CERTIFICATE-----") {
		t.Errorf("stdin = %q", stdin)
	}
	run.conn.Host = "db.example"
	if _, err := run.script(); err == nil || !strings.Contains(err.Error(), "must be an address") {
		t.Errorf("server_name with a host name = %v", err)
	}
}
