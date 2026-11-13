// Tests for a verify-full connection with no pinned CA run through a
// device's pg_dump or pg_restore, on a device whose client and libpq are
// both PostgreSQL 15: libpq before 16 reads sslrootcert=system as a file
// named system in its working directory, so the method refuses it there,
// and the tool never runs where a login's own files are.
package pg_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// libpqMissingSystem is how libpq before 16 refuses sslrootcert=system on
// a device with no file of that name where it runs.
const libpqMissingSystem = `root certificate file "system" does not exist`

// startOldLibpqDevice starts a device serving TLS for localhost from a
// fresh self-signed authority, whose PEM it returns, and skips unless the
// device's client is older than 16, which these tests are about.
func startOldLibpqDevice(t *testing.T) (*harness, string) {
	t.Helper()
	if major, err := strconv.Atoi(testsupport.PostgresMajor()); err != nil || major >= 16 {
		t.Skipf("the device's client is PostgreSQL %s (%v), whose libpq reads sslrootcert=system as the system's roots", testsupport.PostgresMajor(), err)
	}
	dir := t.TempDir()
	ca := selfSigned(t, dir)
	return startDeviceHarness(t, dir), string(ca)
}

// plant writes content to the device login's own directory under name,
// owned by the login, as anyone who can write there could.
func plant(t *testing.T, h *harness, name, content string) {
	t.Helper()
	h.dev.Root(t, "cat > "+remoteexec.QuoteArg(pg.DeviceHome+"/"+name)+" <<'PLANTED'\n"+content+"PLANTED\nchown "+pg.DeviceUser+" "+remoteexec.QuoteArg(pg.DeviceHome+"/"+name))
}

// TestSystemRoots_AClientOlderThan16IsRefusedBeforeContact: what the tools
// were handed before, PGSSLMODE=verify-full with PGSSLROOTCERT=system, is
// shown to be unsafe on this device first: libpq 15 fails closed with no
// file named system where it runs, and verifies the server against one
// that is planted there, which could be any authority. db.pg.dump and
// db.pg.restore, and their checks, refuse such a connection, naming the
// program, its version and the remedy, before any contact with the server
// (a connection to a port where nothing listens is refused the same way),
// and with the CA pinned the same device dumps.
func TestSystemRoots_AClientOlderThan16IsRefusedBeforeContact(t *testing.T) {
	h, ca := startOldLibpqDevice(t)
	ctx := context.Background()
	ssh := h.dev.Connect(t)
	// The password on a command line is this test's own throwaway one.
	probe := "PGHOST=localhost PGHOSTADDR=127.0.0.1 PGPORT=5432 PGUSER=" + adminUser + " PGPASSWORD=" + adminPassword +
		" PGDATABASE=app PGSSLMODE=verify-full PGSSLROOTCERT=system pg_dump --schema-only --file=/dev/null"
	res, err := ssh.Run(ctx, probe)
	if err != nil || res.ExitCode == 0 || !strings.Contains(res.Stderr, libpqMissingSystem) {
		t.Fatalf("libpq 15 with no file named system: exit %d, %v, %q; want it to fail closed", res.ExitCode, err, res.Stderr)
	}
	plant(t, h, "system", ca)
	if res, err := ssh.Run(ctx, probe); err != nil || res.ExitCode != 0 {
		t.Fatalf("libpq 15 with a planted file named system: exit %d, %v, %q; want it to verify against the planted authority", res.ExitCode, err, res.Stderr)
	}

	for name, port := range map[string]int{"system-roots": 5432, "nothing-listening": 1} {
		h.rc.connections = append(h.rc.connections, wire.DatabaseConnection{Name: name, Host: "127.0.0.1", Port: port, ServerName: "localhost", User: adminUser, Password: adminPassword})
	}
	h.rc.connections = append(h.rc.connections, wire.DatabaseConnection{Name: "pinned", Host: "127.0.0.1", Port: 5432, ServerName: "localhost", User: adminUser, Password: adminPassword, CAPEM: ca})
	for _, conn := range []string{"system-roots", "nothing-listening"} {
		dest := pg.DeviceHome + "/" + conn + ".dump"
		dumpParams := params(map[string]any{"connection": conn, "database": "app", "dest": dest})
		restoreParams := params(map[string]any{"connection": conn, "database": "copy", "src": dest, "create": true})
		for method, call := range map[string]func() error{
			"Dump":         func() error { _, err := pg.Dump(ctx, h.fresh(), h.device, dumpParams); return err },
			"CheckDump":    func() error { _, err := pg.CheckDump(ctx, h.fresh(), h.device, dumpParams); return err },
			"Restore":      func() error { _, err := pg.Restore(ctx, h.fresh(), h.device, restoreParams); return err },
			"CheckRestore": func() error { _, err := pg.CheckRestore(ctx, h.fresh(), h.device, restoreParams); return err },
		} {
			program := "pg_dump"
			if strings.Contains(method, "Restore") {
				program = "pg_restore"
			}
			err := call()
			if err == nil || !strings.Contains(err.Error(), "connection "+conn+" verifies the server against the device's own trusted roots, which "+program+" reads only from PostgreSQL 16, and the device's is 15") || !strings.Contains(err.Error(), "ca_pem") {
				t.Errorf("%s on %s = %v; want the refusal naming %s, its version and ca_pem", method, conn, err, program)
			}
		}
		if out := h.dev.Root(t, "ls "+pg.DeviceHome); strings.Contains(out, conn+".dump") {
			t.Errorf("a refused dump on %s wrote its archive: %s", conn, out)
		}
	}

	if _, err := pg.Dump(ctx, h.fresh(), h.device, params(map[string]any{"connection": "pinned", "database": "app", "dest": pg.DeviceHome + "/pinned.dump"})); err != nil {
		t.Fatalf("a dump with the CA pinned, on the same device: %v", err)
	}
	h.dev.Root(t, "test -s "+pg.DeviceHome+"/pinned.dump")
}

// TestSystemRoots_APlantedFileIsNeverReached: a client whose libpq is
// older than the version it reports (here pg_dump 15, run as if it were
// 16) is handed sslrootcert=system and reads it as a file named system in
// its working directory. That is the run's own private directory, so a
// file planted in the login's is never read and the connection fails
// closed.
func TestSystemRoots_APlantedFileIsNeverReached(t *testing.T) {
	h, ca := startOldLibpqDevice(t)
	plant(t, h, "system", ca)
	conn := wire.DatabaseConnection{Name: "system-roots", Host: "127.0.0.1", Port: 5432, ServerName: "localhost", User: adminUser, Password: adminPassword}
	res, err := pg.RunToolForTest(context.Background(), h.dev.Connect(t), conn, "app", []string{"pg_dump", "--schema-only", "--file=/dev/null"}, 16)
	if err == nil || !strings.Contains(err.Error(), libpqMissingSystem) {
		t.Fatalf("pg_dump with a planted file named system in the login's directory = %v (stderr %q); want libpq to find none where it runs", err, res.Stderr)
	}
}
