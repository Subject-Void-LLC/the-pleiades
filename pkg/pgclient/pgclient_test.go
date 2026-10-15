// Tests for pgclient against a real PostgreSQL server in a container:
// the connection, what it never reads of this machine, TLS through its
// own negotiation, and the bounds and rules of a query.
package pgclient_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
)

// server is a running PostgreSQL and the address it is reached at.
type server struct {
	host string
	port int
}

// The account every test server is started with.
const (
	user     = "app"
	password = "app-secret-for-tests"
	database = "app"
)

// startServer starts PostgresImage; with tlsDir it serves TLS from the
// cert.pem and key.pem there.
func startServer(t *testing.T, tlsDir string) server {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a real PostgreSQL")
	}
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testpg.WithDatabase(database), testpg.WithUsername(user), testpg.WithPassword(password),
		testsupport.PostgresReady(),
	}
	if tlsDir != "" {
		// The server reads its key only when the server's own account owns
		// it and no one else can read it, so it is installed so at start.
		opts = append(opts,
			testcontainers.WithFiles(
				testcontainers.ContainerFile{HostFilePath: filepath.Join(tlsDir, "cert.pem"), ContainerFilePath: "/tls/cert.pem", FileMode: 0o644},
				testcontainers.ContainerFile{HostFilePath: filepath.Join(tlsDir, "key.pem"), ContainerFilePath: "/tls/key.pem", FileMode: 0o644},
			),
			testcontainers.WithEntrypoint("sh", "-c",
				"install -o postgres -m 600 /tls/key.pem /tmp/server.key && install -o postgres -m 644 /tls/cert.pem /tmp/server.crt && "+
					"exec docker-entrypoint.sh postgres -c ssl=on -c ssl_cert_file=/tmp/server.crt -c ssl_key_file=/tmp/server.key"),
		)
	}
	c, err := testpg.Run(ctx, testsupport.PostgresImage, opts...)
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		t.Fatalf("starting postgres: %v", err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	return server{host: host, port: int(port.Num())}
}

// target is a target for srv, dialed directly.
func (srv server) target(pw string, cfg *tls.Config) pgclient.Target {
	addr := net.JoinHostPort(srv.host, strconv.Itoa(srv.port))
	return pgclient.Target{
		Host: srv.host, Port: srv.port, User: user, Password: pw, Database: database, TLS: cfg,
		ApplicationName: "pgclient-test",
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
	}
}

// open opens a session or fails the test.
func open(t *testing.T, tg pgclient.Target) *pgclient.Session {
	t.Helper()
	s, err := pgclient.Open(context.Background(), tg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestOpen_ReadsNothingOfThisMachine plants, where the driver would look
// for them, a password file and a PGPASSWORD holding the right password
// and a client certificate, then connects with no password: the server
// must refuse the login, since none of them may be read.
func TestOpen_ReadsNothingOfThisMachine(t *testing.T) {
	srv := startServer(t, "")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".pgpass"), []byte("*:*:*:*:"+password+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".postgresql"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".postgresql", "postgresql.crt"), []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PGPASSFILE", filepath.Join(home, ".pgpass"))
	t.Setenv("PGPASSWORD", password)
	t.Setenv("PGDATABASE", "postgres")

	if s, err := pgclient.Open(context.Background(), srv.target("", nil)); err == nil {
		_ = s.Close()
		t.Fatal("a login with no password succeeded: a planted password was read")
	} else if !strings.Contains(err.Error(), "password") {
		t.Fatalf("the refusal = %v, want the server refusing the password", err)
	}
	s := open(t, srv.target(password, nil))
	num, major, err := s.ServerVersion(context.Background())
	if err != nil || major != num/10000 || strconv.Itoa(major) != testsupport.PostgresMajor() {
		t.Fatalf("ServerVersion = %d, %d, %v", num, major, err)
	}
}

// TestQuery_OneReadOnlyStatementWithinItsBounds: a read-only query cannot
// write; more than one statement is refused; past the row or byte bound
// it fails rather than return part; a timeout is the server's; NULL and
// the empty string stay apart in CSV; a write commits.
func TestQuery_OneReadOnlyStatementWithinItsBounds(t *testing.T) {
	srv := startServer(t, "")
	s := open(t, srv.target(password, nil))
	ctx := context.Background()
	limits := pgclient.Limits{Statement: 5 * time.Second, Lock: time.Second}
	if _, err := s.Exec(ctx, "CREATE TABLE t (id int PRIMARY KEY, note text, raw bytea)", nil, limits); err != nil {
		t.Fatal(err)
	}
	n, err := s.Exec(ctx, "INSERT INTO t VALUES (1, NULL, '\\xff00'), (2, '', NULL), (3, 'a,\"b\"', 'ok')", nil, limits)
	if err != nil || n != 3 {
		t.Fatalf("Exec = %d, %v", n, err)
	}
	read := func(sql string, maxRows, maxBytes int) (*pgclient.Table, error) {
		return s.Query(ctx, pgclient.QueryOptions{SQL: sql, ReadOnly: true, Limits: limits, MaxRows: maxRows, MaxBytes: maxBytes})
	}
	table, err := read("SELECT id, note, raw FROM t ORDER BY id", 10, 1<<10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := table.CSV(), "id,note,raw\r\n1,,\\xff00\r\n2,\"\",\r\n3,\"a,\"\"b\"\"\",ok\r\n"; got != want {
		t.Errorf("CSV =\n%q\nwant\n%q", got, want)
	}

	for sql, want := range map[string]string{
		"INSERT INTO t VALUES (4, 'x', NULL)": "read-only transaction",
		"SELECT 1; DELETE FROM t":             "cannot insert multiple commands into a prepared statement",
		"SELECT pg_sleep(10)":                 "statement timeout",
	} {
		if _, err := s.Query(ctx, pgclient.QueryOptions{SQL: sql, ReadOnly: true, Limits: pgclient.Limits{Statement: 300 * time.Millisecond}, MaxRows: 10, MaxBytes: 1 << 10}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want %q", sql, err, want)
		}
	}
	if _, err := read("SELECT id FROM t", 2, 1<<10); !errors.Is(err, pgclient.ErrTooManyRows) {
		t.Errorf("past the row bound = %v", err)
	}
	if _, err := read("SELECT repeat('x', 100) FROM t", 10, 150); !errors.Is(err, pgclient.ErrTooLarge) {
		t.Errorf("past the byte bound = %v", err)
	}

	// A write that returns rows commits them.
	table, err = s.Query(ctx, pgclient.QueryOptions{SQL: "INSERT INTO t (id, note) VALUES ($1, $2) RETURNING id", Args: []any{9, "nine"}, Limits: limits, MaxRows: 1, MaxBytes: 64})
	if err != nil || len(table.Rows) != 1 || *table.Rows[0][0] != "9" {
		t.Fatalf("a returning write = %+v, %v", table, err)
	}
	if table, err := read("SELECT note FROM t WHERE id = 9", 1, 64); err != nil || *table.Rows[0][0] != "nine" {
		t.Errorf("the write was not committed: %+v, %v", table, err)
	}
}

// TestSnapshot_IsOneMoment: what another session commits while a
// snapshot is open is not seen by it.
func TestSnapshot_IsOneMoment(t *testing.T) {
	srv := startServer(t, "")
	ctx := context.Background()
	s, other := open(t, srv.target(password, nil)), open(t, srv.target(password, nil))
	if _, err := s.Exec(ctx, "CREATE TABLE t (id int)", nil, pgclient.Limits{}); err != nil {
		t.Fatal(err)
	}
	err := s.Snapshot(ctx, pgclient.Limits{Statement: 5 * time.Second}, func(tx *sql.Tx) error {
		var before, after int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&before); err != nil {
			return err
		}
		if _, err := other.Exec(ctx, "INSERT INTO t VALUES (1)", nil, pgclient.Limits{}); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&after); err != nil {
			return err
		}
		if before != 0 || after != 0 {
			t.Errorf("the snapshot saw %d then %d rows, want 0 then 0", before, after)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO t VALUES (2)"); err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("a write in a snapshot = %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTLS_IsNegotiatedHereAndAlwaysVerified: against a server with TLS
// on, a pinned authority connects and the session is encrypted; a name
// the certificate does not hold, or an authority that did not sign it, is
// refused; against a server without TLS, a TLS target is refused rather
// than sent in the clear.
func TestTLS_IsNegotiatedHereAndAlwaysVerified(t *testing.T) {
	dir := t.TempDir()
	caPEM := selfSigned(t, dir)
	srv := startServer(t, dir)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)

	s := open(t, srv.target(password, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "localhost"}))
	var encrypted bool
	if err := s.DB().QueryRowContext(context.Background(), "SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()").Scan(&encrypted); err != nil || !encrypted {
		t.Fatalf("the session is not encrypted: %v, %v", encrypted, err)
	}

	other := x509.NewCertPool()
	other.AppendCertsFromPEM(selfSigned(t, t.TempDir()))
	for name, cfg := range map[string]*tls.Config{
		"a name it does not hold": {MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "db.example.internal"},
		"another authority":       {MinVersion: tls.VersionTLS12, RootCAs: other, ServerName: "localhost"},
	} {
		if s, err := pgclient.Open(context.Background(), srv.target(password, cfg)); err == nil {
			_ = s.Close()
			t.Errorf("%s: connected", name)
		} else if !strings.Contains(err.Error(), "certificate") {
			t.Errorf("%s: %v", name, err)
		}
	}

	plain := startServer(t, "")
	if s, err := pgclient.Open(context.Background(), plain.target(password, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool})); err == nil {
		_ = s.Close()
		t.Fatal("a TLS target connected to a server without TLS")
	} else if !strings.Contains(err.Error(), "does not offer TLS") {
		t.Fatalf("a server without TLS = %v", err)
	}
}

// selfSigned writes a self-signed certificate for localhost and
// 127.0.0.1, and its key, to dir as cert.pem and key.pem, returning the
// certificate's PEM.
func selfSigned(t *testing.T, dir string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPEM
}
