// The harness the db.pg methods' tests run against: a real PostgreSQL in a
// container, reached from a real SSH server in this process whose commands
// run through a real /bin/sh on this machine, so the tunnel, pg_dump and
// pg_restore are the real ones. The device sees the database at the
// container's mapped port on loopback.
package pg_test

import (
	"context"
	"net"
	"os/exec"
	"strconv"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The superuser every test server starts with.
const (
	adminUser     = "admin"
	adminPassword = "admin-pw-for-tests"
)

// device is an SSH device that runs commands.
type device struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *device) SSHHost() string          { return d.host }
func (d *device) SSHPort() int             { return d.port }
func (d *device) WorkingDirectory() string { return "" }

// stubContext keeps every return a method sets and answers for the
// connections it holds.
type stubContext struct {
	secrets     map[string]string
	stats       map[string]any
	connections []wire.DatabaseConnection
}

func (c *stubContext) InjectSecrets() map[string]string     { return c.secrets }
func (c *stubContext) SetStat(key string, value any) error  { c.stats[key] = value; return nil }
func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }
func (c *stubContext) DatabaseConnection(name string) (wire.DatabaseConnection, bool, error) {
	conn, ok := wire.FindDatabaseConnection(c.connections, name)
	return conn, ok, nil
}

// harness is one test's database server, device and run.
type harness struct {
	device *device
	rc     *stubContext
	host   string
	port   int
}

// newHarness starts PostgreSQL and the SSH device, with a connection
// named admin for the superuser.
func newHarness(t *testing.T) *harness { return startHarness(t, "") }

// startHarness is newHarness; with tlsDir, the server serves TLS from the
// cert.pem and key.pem there.
func startHarness(t *testing.T, tlsDir string) *harness {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a real PostgreSQL")
	}
	ctx := context.Background()
	opts := []testcontainers.ContainerCustomizer{
		testpg.WithDatabase("app"), testpg.WithUsername(adminUser), testpg.WithPassword(adminPassword),
		testsupport.PostgresReady(),
	}
	if tlsDir != "" {
		// The server reads its key only when its own account owns it and no
		// one else can read it, so it is installed so at start.
		opts = append(opts,
			testcontainers.WithFiles(
				testcontainers.ContainerFile{HostFilePath: tlsDir + "/cert.pem", ContainerFilePath: "/tls/cert.pem", FileMode: 0o644},
				testcontainers.ContainerFile{HostFilePath: tlsDir + "/key.pem", ContainerFilePath: "/tls/key.pem", FileMode: 0o644},
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
	mapped, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	h := &harness{
		device: &device{
			Stub: &inventorytest.Stub{StubID: "db-1", StubName: "db-box", StubState: inventory.StateActive,
				Caps: []capability.Name{capability.NameSSHTransport, capability.NameCommandExec}},
			host: srv.Host, port: srv.Port,
		},
		rc:   &stubContext{secrets: map[string]string{"username": srv.Username, "password": srv.Password}, stats: map[string]any{}},
		host: host, port: int(mapped.Num()),
	}
	h.connect("admin", adminUser, adminPassword)
	return h
}

// connect adds a connection named name for user, to the harness's server
// over loopback without TLS (a local server), and returns it.
func (h *harness) connect(name, user, password string, live ...string) wire.DatabaseConnection {
	conn := wire.DatabaseConnection{Name: name, Host: h.host, Port: h.port, User: user, Password: password, SSLMode: wire.SSLDisable, Live: live}
	h.rc.connections = append(h.rc.connections, conn)
	return conn
}

// sql runs statements as the superuser on database, dialing the server
// directly rather than through the methods under test, failing the test
// on an error. Each runs outside a transaction, so CREATE DATABASE can.
func (h *harness) sql(t *testing.T, database string, statements ...string) {
	t.Helper()
	s := h.admin(t, database)
	defer func() { _ = s.Close() }()
	for _, stmt := range statements {
		if _, err := s.DB().ExecContext(context.Background(), stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
}

// value reads one value as the superuser, as sql dials.
func (h *harness) value(t *testing.T, database, query string) string {
	t.Helper()
	s := h.admin(t, database)
	defer func() { _ = s.Close() }()
	var v string
	if err := s.DB().QueryRowContext(context.Background(), query).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

// admin is a superuser session on database, dialed directly.
func (h *harness) admin(t *testing.T, database string) *pgclient.Session {
	t.Helper()
	s, err := pgclient.Open(context.Background(), pgclient.Target{
		Host: h.host, Port: h.port, User: adminUser, Password: adminPassword, Database: database,
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", h.addr())
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fresh returns a new stats map for the next call.
func (h *harness) fresh() *stubContext {
	h.rc.stats = map[string]any{}
	return h.rc
}

// params builds a task's parameters with extra merged in.
func params(extra map[string]any) map[string]any {
	p := map[string]any{"insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// requireTools skips unless pg_dump and pg_restore at the server's major
// version (or, failing that, any) are on PATH, which the device's shell
// inherits.
func requireTools(t *testing.T) {
	t.Helper()
	testsupport.UsePostgresClientTools(t)
	for _, name := range []string{"pg_dump", "pg_restore"} {
		_, err := exec.LookPath(name)
		testsupport.Require(t, name, err == nil, name+" is not on PATH; the device these tests stand in for runs it")
	}
}

// addr is the server as the device sees it.
func (h *harness) addr() string { return net.JoinHostPort(h.host, strconv.Itoa(h.port)) }
