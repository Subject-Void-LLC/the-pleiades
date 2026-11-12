// The test that a tool run with no password hands the device's pg_dump no
// password file of the device login's own, and draws no warning about one.
package pg_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/db/pg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestNoAmbientPassfile_OnTheDevice: a connection with no password runs
// pg_dump with the run's own password file, holding no line, so the login
// is refused, though the device login has working ones holding the right
// password where libpq would otherwise look: the PGPASSFILE its
// environment names and its ~/.pgpass. Nothing is written about a password
// file's mode. The same run with the password logs in, so a planted file
// read would have too. It runs on this machine's device, whose environment
// is the test's, and on a device container whose client and libpq are
// PostgreSQL 15.
func TestNoAmbientPassfile_OnTheDevice(t *testing.T) {
	ctx := context.Background()
	pgpass := []byte("*:*:*:*:" + adminPassword + "\n")
	for _, tc := range []struct {
		name  string
		start func(t *testing.T) *harness
	}{
		{"this machine", func(t *testing.T) *harness {
			requireTools(t)
			h := newHarness(t)
			home, elsewhere := t.TempDir(), t.TempDir()
			for _, file := range []string{filepath.Join(home, ".pgpass"), filepath.Join(elsewhere, "pgpass")} {
				if err := os.WriteFile(file, pgpass, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// The device's shell inherits this process's environment.
			t.Setenv("HOME", home)
			t.Setenv("PGPASSFILE", filepath.Join(elsewhere, "pgpass"))
			return h
		}},
		{"a device container", func(t *testing.T) *harness {
			h := startDeviceHarness(t, "")
			plant(t, h, ".pgpass", string(pgpass))
			h.dev.Root(t, "chmod 600 "+pg.DeviceHome+"/.pgpass")
			return h
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.start(t)
			ssh := h.ssh(t)
			host, port := h.deviceView()
			conn := wire.DatabaseConnection{Name: "nopw", Host: host, Port: port, User: adminUser, SSLMode: wire.SSLDisable}
			dump := []string{"pg_dump", "--no-password", "--schema-only", "--file=/dev/null"}
			res, err := pg.RunToolForTest(ctx, ssh, conn, "app", dump, 16)
			if err == nil || !strings.Contains(err.Error(), "no password supplied") {
				t.Fatalf("pg_dump with no password = %v; want the server's refusal, a planted password file unread", err)
			}
			if strings.Contains(res.Stderr, "WARNING") || strings.Contains(res.Stderr, "password file") {
				t.Errorf("pg_dump wrote about a password file:\n%s", res.Stderr)
			}
			conn.Password = adminPassword
			if res, err := pg.RunToolForTest(ctx, ssh, conn, "app", dump, 16); err != nil || strings.Contains(res.Stderr, "WARNING") {
				t.Fatalf("the control, with the password: %v\n%s", err, res.Stderr)
			}
		})
	}
}
