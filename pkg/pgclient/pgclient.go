// Package pgclient is the PostgreSQL client a Collection connects with:
// one connection, over a byte stream the caller opens (a tunnel through a
// device), authenticated with exactly the credentials it is given.
//
// It is the one package under pkg/ that imports the driver
// (github.com/lib/pq, which internal/ent already uses), so the rules
// below hold for every method that talks to a database, which
// internal/archtest enforces.
//
// # Nothing of the machine running pleiades is read
//
// The driver, left to itself, falls back on the machine it runs on: an
// empty password reads ~/.pgpass, TLS reads ~/.postgresql/root.crt and a
// client certificate from ~/.postgresql/postgresql.crt, and the
// connection-string form reads every PG* environment variable. Each of
// those would hand one run's database the credentials of whoever runs
// pleiades. So the driver is configured from a struct (never the
// environment), its password file is the empty device file, and it never
// negotiates TLS at all: this package does, in the dialer, before the
// driver sees the connection, with a configuration the caller built
// (pkg/devicetls: a TLS 1.2 floor, a pinned authority or the system's,
// and always verified). The driver then speaks the protocol over a stream
// that is already encrypted.
//
// The driver reads one variable this package cannot turn off: PQGO_DEBUG,
// once, as the program starts. Set to 1, it writes each connection's
// settings, password included, and every query and row to standard
// error. So Open refuses to connect while it is set to anything, or was
// when the program started.
package pgclient

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/lib/pq"
)

// Target is one database and how to reach it.
type Target struct {
	// Host is the server's name as the dialing side sees it. It is the
	// name a certificate is checked against unless TLS sets ServerName,
	// and otherwise only names the server in messages.
	Host string
	// Port is the server's port, for messages and the driver.
	Port int
	// User and Password authenticate. An empty password sends none: the
	// server must allow the login without one (peer or trust).
	User     string
	Password string
	// Database is the database to connect to.
	Database string
	// TLS, when set, is negotiated before anything else is sent and must
	// succeed; when nil the connection is not encrypted.
	TLS *tls.Config
	// Dial opens the stream to the server.
	Dial func(ctx context.Context) (net.Conn, error)
	// ApplicationName is shown in the server's pg_stat_activity.
	ApplicationName string
}

// Session is one open connection.
type Session struct {
	db     *sql.DB
	target Target
}

// debugAtStart is PQGO_DEBUG as it was when the driver read it: this
// package's variables are set after the driver's, as the program starts.
var debugAtStart = os.Getenv(debugVariable)

// debugVariable turns the driver's protocol trace on.
const debugVariable = "PQGO_DEBUG"

// Open connects to t and confirms the server answers.
func Open(ctx context.Context, t Target) (*Session, error) {
	// The driver decided at start whether to trace, so unsetting the
	// variable since does not help; the current value is checked as well,
	// so no value that could ever turn the trace on is let through.
	if debugAtStart != "" || os.Getenv(debugVariable) != "" {
		return nil, fmt.Errorf("pgclient: %s is set, and the driver then writes every connection's password, query and row to standard error; unset it and start again", debugVariable)
	}
	if t.Dial == nil {
		return nil, errors.New("pgclient: a target needs a dialer")
	}
	if t.Port < 1 || t.Port > 65535 {
		return nil, fmt.Errorf("pgclient: port %d is not a port", t.Port)
	}
	connector, err := pq.NewConnectorConfig(pq.Config{
		Host:            t.Host,
		Port:            uint16(t.Port),
		User:            t.User,
		Password:        t.Password,
		Database:        t.Database,
		SSLMode:         pq.SSLModeDisable,
		Passfile:        os.DevNull,
		ApplicationName: t.ApplicationName,
		// The driver decodes text as UTF-8 and times in ISO form, and sets
		// both only when it reads a connection string, never from a Config,
		// so they are asked for here. pkg/sqllint lexes strings as standard
		// conforming, where a backslash is an ordinary character. Sent in
		// the startup packet, all three win over the database's and the
		// role's own settings.
		ClientEncoding: "UTF8",
		Datestyle:      "ISO, MDY",
		Runtime:        map[string]string{"standard_conforming_strings": "on"},
	})
	if err != nil {
		return nil, fmt.Errorf("pgclient: %w", err)
	}
	connector.Dialer(dialer{t: t})
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to %s: %w", t.name(), err)
	}
	return &Session{db: db, target: t}, nil
}

// DB is the session's connection pool, which holds one connection.
func (s *Session) DB() *sql.DB { return s.db }

// Close closes the session.
func (s *Session) Close() error { return s.db.Close() }

// ServerVersion is the server's version number (server_version_num, so
// 150019 for 15.19) and its major version.
func (s *Session) ServerVersion(ctx context.Context) (num, major int, err error) {
	var text string
	if err := s.db.QueryRowContext(ctx, "SELECT current_setting('server_version_num')").Scan(&text); err != nil {
		return 0, 0, err
	}
	num, err = strconv.Atoi(text)
	if err != nil {
		return 0, 0, fmt.Errorf("the server's version number %q is not a number", text)
	}
	return num, num / 10000, nil
}

// name names the target in messages.
func (t Target) name() string {
	return fmt.Sprintf("%s@%s:%d/%s", t.User, t.Host, t.Port, t.Database)
}

// sslRequestCode is the protocol's SSLRequest code (1234 << 16 | 5679).
const sslRequestCode = 80877103

// dialer opens the driver's connections through the target's own dialer,
// negotiating TLS first when the target has it.
type dialer struct{ t Target }

// Dial implements pq.Dialer.
func (d dialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

// DialTimeout implements pq.Dialer.
func (d dialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return d.DialContext(ctx, network, address)
}

// DialContext implements pq.DialerContext. The driver's network and
// address are ignored: where the server is, is the target's dialer's to
// know.
func (d dialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	conn, err := d.t.Dial(ctx)
	if err != nil {
		return nil, err
	}
	if d.t.TLS == nil {
		return conn, nil
	}
	tlsConn, err := startTLS(ctx, conn, d.t.TLS)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("TLS to %s:%d: %w", d.t.Host, d.t.Port, err)
	}
	return tlsConn, nil
}

// startTLS asks the server for TLS (an SSLRequest, answered by one byte)
// and runs the handshake. Exactly one byte is read before the handshake,
// so nothing the server sends unencrypted after its answer can be taken
// for protocol (the class CVE-2021-23214 was).
func startTLS(ctx context.Context, conn net.Conn, cfg *tls.Config) (net.Conn, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}
	var req [8]byte
	binary.BigEndian.PutUint32(req[0:4], 8)
	binary.BigEndian.PutUint32(req[4:8], sslRequestCode)
	if _, err := conn.Write(req[:]); err != nil {
		return nil, err
	}
	var answer [1]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil {
		return nil, fmt.Errorf("reading the server's answer to a TLS request: %w", err)
	}
	switch answer[0] {
	case 'S':
	case 'N':
		return nil, errors.New("the server does not offer TLS, and this connection requires it")
	default:
		return nil, fmt.Errorf("the server answered a TLS request with %q, which is not the protocol", answer[0])
	}
	client := tls.Client(conn, cfg)
	if err := client.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return client, nil
}
