// pg_dump and pg_restore, run on the device with the connection's
// password on standard input.
package pg

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// maxToolOutput bounds what a tool run may print; pg_restore --list of a
// large archive is the most a run prints.
const maxToolOutput = 8 << 20

// toolVersionPattern reads a tool's major version from its --version.
var toolVersionPattern = regexp.MustCompile(`\(PostgreSQL\) (\d+)`)

// toolRun is one run of pg_dump or pg_restore against a connection.
type toolRun struct {
	conn     wire.DatabaseConnection
	database string
	// command is the program and its arguments, each quoted here.
	command []string
	timeout time.Duration
	// after runs once command has succeeded, in the same shell.
	after string
	// cleanup runs however the script ends.
	cleanup string
}

// script is the shell the device runs. Every PG* variable the login
// carries is cleared first, so its own PGSERVICE or PGHOST cannot redirect
// the run; the password is read from the first line of standard input into
// PGPASSWORD, exported to the tool alone and never on a command line; a
// pinned CA, the rest of standard input, is written into a private
// directory removed however the run ends; and the tool runs under
// timeout -k so a hung one is killed on the device.
func (t toolRun) script() (string, error) {
	q := remoteexec.QuoteArg
	if err := toolsCanReach(t.conn); err != nil {
		return "", err
	}
	host, hostaddr := t.conn.Host, ""
	if t.conn.ServerName != "" {
		host, hostaddr = t.conn.ServerName, t.conn.Host
	}
	lines := []string{
		"set -e",
		`for v in $(env | sed -n 's/^\(PG[A-Za-z0-9_]*\)=.*/\1/p'); do unset "$v"; done`,
		`IFS= read -r PGPASSWORD || true`,
		"export PGPASSWORD",
		"umask 077",
		`d=$(mktemp -d)`,
		`trap 'rm -rf "$d"` + cleanupClause(t.cleanup) + `' EXIT`,
		"export PGHOST=" + q(host) + " PGPORT=" + strconv.Itoa(t.conn.EffectivePort()) + " PGUSER=" + q(t.conn.User) +
			" PGDATABASE=" + q(t.database) + " PGSSLMODE=" + q(t.conn.EffectiveSSLMode()) +
			" PGAPPNAME=" + applicationName + " PGPASSFILE=/dev/null PGCONNECT_TIMEOUT=30",
	}
	if hostaddr != "" {
		lines = append(lines, "export PGHOSTADDR="+q(hostaddr))
	}
	switch {
	case t.conn.CAPEM != "":
		lines = append(lines, `cat > "$d/ca.pem"`, `export PGSSLROOTCERT="$d/ca.pem"`)
	case t.conn.EffectiveSSLMode() == wire.SSLVerifyFull:
		lines = append(lines, "export PGSSLROOTCERT=system")
	}
	run := "timeout -k 10s " + strconv.FormatInt(int64(t.timeout/time.Second), 10) + "s " + remoteexec.QuoteCommand(t.command)
	if t.after != "" {
		run += " && " + t.after
	}
	lines = append(lines, run)
	return strings.Join(lines, "\n"), nil
}

// toolsCanReach refuses a connection pg_dump and pg_restore cannot verify
// as written: they check a certificate against the host they dial, so a
// server_name works only when the host is an address (dialed as
// hostaddr, checked as host).
func toolsCanReach(conn wire.DatabaseConnection) error {
	if conn.ServerName != "" && net.ParseIP(conn.Host) == nil {
		return fmt.Errorf("connection %s: pg_dump and pg_restore check a certificate against the host they dial, so with server_name the host must be an address, not %s", conn.Name, conn.Host)
	}
	return nil
}

// cleanupClause is the trap's extra command, when there is one.
func cleanupClause(cleanup string) string {
	if cleanup == "" {
		return ""
	}
	return "; " + strings.ReplaceAll(cleanup, "'", `'\''`)
}

// stdin is what the script reads: the password's line, then any pinned CA.
func (t toolRun) stdin() string { return t.conn.Password + "\n" + t.conn.CAPEM }

// run runs the tool, naming a timeout and the tool's own last words on
// failure.
func (t toolRun) run(ctx context.Context, ssh *remoteexec.Conn) (remoteexec.Result, error) {
	script, err := t.script()
	if err != nil {
		return remoteexec.Result{}, err
	}
	res, err := ssh.RunWithStdinBounded(ctx, script, strings.NewReader(t.stdin()), maxToolOutput)
	if err != nil {
		return res, fmt.Errorf("%s: %w", t.command[0], err)
	}
	switch res.ExitCode {
	case 0:
		return res, nil
	case 124, 137:
		return res, fmt.Errorf("%s timed out after %s and was stopped", t.command[0], t.timeout)
	}
	return res, fmt.Errorf("%s exited %d: %s", t.command[0], res.ExitCode, remoteexec.Excerpt(strings.TrimSpace(res.Stderr), 4096))
}

// toolMajor is the device's program's major version, refusing a device
// that has no such program.
func toolMajor(ctx context.Context, ssh *remoteexec.Conn, program string) (int, string, error) {
	res, err := ssh.RunWithStdinBounded(ctx, remoteexec.QuoteCommand([]string{program, "--version"}), nil, 4096)
	if err != nil {
		return 0, "", err
	}
	if res.ExitCode != 0 {
		return 0, "", fmt.Errorf("the device has no %s on PATH (exited %d); install the PostgreSQL client programs there", program, res.ExitCode)
	}
	version := strings.TrimSpace(res.Stdout)
	m := toolVersionPattern.FindStringSubmatch(version)
	if m == nil {
		return 0, "", fmt.Errorf("%s --version answered %q, which names no PostgreSQL version", program, remoteexec.Excerpt(version, 128))
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", err
	}
	return major, version, nil
}

// requireTimeout refuses a device whose timeout command does not take -k
// and a duration, which a tool run depends on to be stopped on the device.
func requireTimeout(ctx context.Context, ssh *remoteexec.Conn) error {
	res, err := ssh.Run(ctx, "timeout -k 10s 5s true")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("the device's timeout command does not take -k and a duration (coreutils, or BusyBox 1.30 or later), which stops a hung pg_dump or pg_restore on the device")
	}
	return nil
}
