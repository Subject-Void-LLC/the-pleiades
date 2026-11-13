// The harness the db.pg methods' tests run against: a real PostgreSQL in a
// container, reached from a real SSH server in this process whose commands
// run through a real /bin/sh on this machine, so the tunnel, pg_dump and
// pg_restore are the real ones. The device sees the database at the
// container's mapped port on loopback.
package pg_test

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	testpg "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pgclient"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
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
	// host and port are the server as this process dials it.
	host string
	port int
	// dbHost and dbPort are the server as the device dials it, when that
	// differs (a device container reaching its own database).
	dbHost string
	dbPort int
	// dev is the device container, when the device is one.
	dev *pg.TestDevice
}

// startDeviceHarness is startHarness with the device a database host in a
// container (pg.StartTestDevice), whose client programs and libpq are
// PostgresImage's own release, reaching its database at 127.0.0.1:5432.
func startDeviceHarness(t *testing.T, tlsDir string) *harness {
	t.Helper()
	dev := pg.StartTestDevice(t, adminUser, adminPassword, tlsDir)
	h := &harness{
		device: &device{
			Stub: &inventorytest.Stub{StubID: "db-1", StubName: "db-box", StubState: inventory.StateActive,
				Caps: []capability.Name{capability.NameSSHTransport, capability.NameCommandExec}},
			host: dev.Host, port: dev.SSHPort,
		},
		rc:   &stubContext{secrets: map[string]string{"username": pg.DeviceUser, "password": pg.DevicePassword}, stats: map[string]any{}},
		host: dev.Host, port: dev.DBPort, dbHost: "127.0.0.1", dbPort: 5432, dev: dev,
	}
	h.connect("admin", adminUser, adminPassword)
	return h
}

// ssh opens a connection to the device as its login, closed when t ends.
func (h *harness) ssh(t *testing.T) *remoteexec.Conn {
	t.Helper()
	if h.dev != nil {
		return h.dev.Connect(t)
	}
	auth, err := remoteexec.AuthFrom(h.rc.secrets["username"], h.rc.secrets["password"], nil, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).
		Connect(context.Background(), nil, remoteexec.Target{Host: h.device.host, Port: h.device.port}, auth)
	if err != nil {
		t.Fatalf("connecting to the device: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// deviceView is the server's host and port as the device dials it.
func (h *harness) deviceView() (string, int) {
	if h.dbHost != "" {
		return h.dbHost, h.dbPort
	}
	return h.host, h.port
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
	host, port := h.deviceView()
	conn := wire.DatabaseConnection{Name: name, Host: host, Port: port, User: user, Password: password, SSLMode: wire.SSLDisable, Live: live}
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

// requireTools stops a test that runs pg_dump and pg_restore unless this
// machine, which the device's shell runs on, has what db.pg.dump and
// db.pg.restore check a device for and a client the fixture's server
// takes (clienttools_test.go says which stop each lack gets).
func requireTools(t testing.TB) {
	t.Helper()
	requireToolsFrom(t, testsupport.UsePostgresClientTools)
}

// requireToolsFrom is requireTools with pin standing in for
// UsePostgresClientTools, so a test can give the answer.
func requireToolsFrom(t testing.TB, pin func(testing.TB) bool) {
	t.Helper()
	need.POSIXShell(t)
	testsupport.Require(t, need.Timeout, hostTimeoutTakesKill(),
		"this machine stands in for the device, and its timeout command takes no -k and duration (macOS has none), which db.pg.dump and db.pg.restore refuse a device without")
	server, err := strconv.Atoi(testsupport.PostgresMajor())
	if err != nil {
		t.Fatalf("the fixture's major version %q is not a number: %v", testsupport.PostgresMajor(), err)
	}
	stopUnlessUsable(t, judgeTools(pin(t), server, majorOnPath))
}

// addr is the server as the device sees it.
func (h *harness) addr() string { return net.JoinHostPort(h.host, strconv.Itoa(h.port)) }
