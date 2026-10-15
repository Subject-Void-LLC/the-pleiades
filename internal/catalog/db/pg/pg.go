// Package pg implements the db.pg methods: PostgreSQL work a run does
// against a database it reaches from a device (docs/design/db-pg.md).
// query, grants, rowcounts and migrate speak the protocol in-process
// (pkg/pgclient) over a tunnel the device opens; dump and restore run the
// device's own pg_dump and pg_restore. A task names a connection, never a
// host, login or password (sdk.DatabaseConnections).
package pg

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The methods' names.
const (
	fqcnQuery     = "db.pg.query"
	fqcnDump      = "db.pg.dump"
	fqcnRestore   = "db.pg.restore"
	fqcnMigrate   = "db.pg.migrate"
	fqcnGrants    = "db.pg.grants"
	fqcnRowcounts = "db.pg.rowcounts"
)

// Parameters every method takes.
const (
	paramConnection  = "connection"
	paramDatabase    = "database"
	paramTimeout     = "timeout"
	paramLockTimeout = "lock_timeout"
)

// applicationName is what the server's pg_stat_activity shows.
const applicationName = "pleiades"

// maxListed bounds how many problems a failed task's error names; the rest
// are counted.
const maxListed = 50

// connectionFor is the connection the task's key parameter names, from the
// run (sdk.DatabaseConnections), held to the rules a stored one is.
func connectionFor(rc sdk.RunbookContext, params map[string]any, key string) (wire.DatabaseConnection, error) {
	name, err := sdk.RequiredStringParam(params, key)
	if err != nil {
		return wire.DatabaseConnection{}, err
	}
	if !wire.ValidDatabaseConnectionName(name) {
		return wire.DatabaseConnection{}, fmt.Errorf("%s: %q is not a connection name", key, name)
	}
	dbs, ok := rc.(sdk.DatabaseConnections)
	if !ok {
		return wire.DatabaseConnection{}, fmt.Errorf("this run cannot hand a method a database connection")
	}
	conn, found, err := dbs.DatabaseConnection(name)
	if err != nil {
		return wire.DatabaseConnection{}, fmt.Errorf("reading database connection %s: %w", name, err)
	}
	if !found {
		return wire.DatabaseConnection{}, fmt.Errorf("no database connection named %s: add one with `pleiades add-database-connection %s`, or bind a PostgreSQL Connection credential named %s to the template", name, name, name)
	}
	if err := conn.Validate(); err != nil {
		return wire.DatabaseConnection{}, err
	}
	return conn, nil
}

// transportWarnings is what a connection's transport allows that a run
// must say every time it is used.
func transportWarnings(conn wire.DatabaseConnection) []string {
	if conn.Plaintext() {
		return []string{fmt.Sprintf("connection %s sends its password and every row to %s in the clear (insecure_transport)", conn.Name, conn.Host)}
	}
	return nil
}

// databaseName reads a database name parameter: required, at most 63
// bytes, no control characters.
func databaseName(params map[string]any, key string) (string, error) {
	name, err := sdk.RequiredStringParam(params, key)
	if err != nil {
		return "", err
	}
	if len(name) > 63 || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("%s: %q is not a database name", key, name)
	}
	return name, nil
}

// durationParam reads a duration written as Go writes one ("30s", "15m"),
// def when absent, refused past max or below a millisecond.
func durationParam(params map[string]any, key string, def, max time.Duration) (time.Duration, error) {
	raw, present := params[key]
	if !present || raw == nil {
		return def, nil
	}
	text, ok := raw.(string)
	if !ok {
		return 0, fmt.Errorf("%s is a duration such as \"30s\" or \"15m\"", key)
	}
	d, err := time.ParseDuration(text)
	if err != nil || d < time.Millisecond || d > max {
		return 0, fmt.Errorf("%s: %q is not a duration from 1ms to %s", key, text, max)
	}
	return d, nil
}

// intParam reads a whole-number parameter, def when absent, from 1 to max.
func intParam(params map[string]any, key string, def, max int) (int, error) {
	n, set, err := sdk.IntParam(params, key)
	if err != nil {
		return 0, err
	}
	if !set {
		return def, nil
	}
	if n < 1 || n > max {
		return 0, fmt.Errorf("%s is %d; it must be from 1 to %d", key, n, max)
	}
	return n, nil
}

// open connects to database on conn's server through the device: a
// direct-tcpip channel to its host and port, or a direct-streamlocal one
// to its socket, and TLS negotiated here (pkg/pgclient) when the
// connection asks for it.
func open(ctx context.Context, ssh *remoteexec.Conn, conn wire.DatabaseConnection, database string) (*pgclient.Session, error) {
	port := conn.EffectivePort()
	dial := func(ctx context.Context) (net.Conn, error) {
		if conn.Socket() {
			return ssh.DialUnix(ctx, path.Join(conn.Host, ".s.PGSQL."+strconv.Itoa(port)))
		}
		return ssh.DialTCP(ctx, net.JoinHostPort(conn.Host, strconv.Itoa(port)))
	}
	var cfg *tls.Config
	if conn.EffectiveSSLMode() == wire.SSLVerifyFull {
		var err error
		if cfg, err = tlsConfig(conn); err != nil {
			return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
		}
	}
	s, err := pgclient.Open(ctx, pgclient.Target{
		Host: conn.Host, Port: port, User: conn.User, Password: conn.Password, Database: database,
		TLS: cfg, Dial: dial, ApplicationName: applicationName,
	})
	if err != nil {
		return nil, fmt.Errorf("connection %s: %w", conn.Name, err)
	}
	return s, nil
}

// tlsConfig is a connection's TLS, built by pkg/devicetls as a device's
// is: a TLS 1.2 floor, the pinned authority or the system's roots, and
// always verified, against server_name or else the host.
func tlsConfig(conn wire.DatabaseConnection) (*tls.Config, error) {
	props := map[string]inventory.PropertyValue{}
	if conn.CAPEM != "" {
		props[devicetls.CAPEMProperty] = conn.CAPEM
	}
	if conn.ServerName != "" {
		props[devicetls.ServerNameProperty] = conn.ServerName
	}
	settings, err := devicetls.Parse(inventory.NewProperties(props))
	if err != nil {
		return nil, err
	}
	cfg, err := settings.Config(nil)
	if err != nil {
		return nil, err
	}
	if cfg.ServerName == "" {
		cfg.ServerName = conn.Host
	}
	return cfg, nil
}

// quoteIdent quotes an identifier for SQL, doubling any quote in it.
func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// qualifiedName splits "schema.name" (or "name", in public), refusing
// anything else. The parts are as written, never case-folded, and are
// quoted wherever they reach SQL.
func qualifiedName(text string) (schema, name string, err error) {
	schema, name, found := strings.Cut(text, ".")
	if !found {
		schema, name = "public", text
	}
	for _, part := range []string{schema, name} {
		if part == "" || len(part) > 63 || strings.ContainsAny(part, ".\"\x00") || strings.ContainsFunc(part, func(r rune) bool { return r < 0x20 }) {
			return "", "", fmt.Errorf("%q is not schema.name", text)
		}
	}
	return schema, name, nil
}

// listed is lines for a failed task's error: at most maxListed of them,
// the rest counted.
func listed(lines []string) string {
	if len(lines) > maxListed {
		lines = append(lines[:maxListed:maxListed], fmt.Sprintf("... and %d more", len(lines)-maxListed))
	}
	return "\n  " + strings.Join(lines, "\n  ")
}

// recordWarnings records a run's warnings, when it has any.
func recordWarnings(rc sdk.RunbookContext, warnings []string) error {
	if len(warnings) == 0 {
		return nil
	}
	return sdk.RecordWarnings(rc, warnings)
}
