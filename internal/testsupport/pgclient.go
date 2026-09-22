// This file holds the one answer to "which pg_dump and pg_restore does a test
// run": the ones at the major version of PostgresImage, when this machine has
// them (FAILURE_PATTERNS.md #279, #292).
//
// A pg_restore newer than the server sends it settings the server does not
// have: 17 and later send transaction_timeout, which 15 rejects, so a restore
// through the machine's default client fails before loading a row.
// Production never meets this, because the backup image carries the server
// release's own programs beside the controller; a test that runs the tools
// on the host has to arrange the same match. It was a TestMain private to
// internal/backup until cmd/controller's backup gate, which runs the real
// controller binary's restore in a child process, failed the same way.
package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PostgresMajor is PostgresImage's major version: "15" for
// postgres:15.19-alpine.
func PostgresMajor() string {
	tag := PostgresImage[strings.LastIndex(PostgresImage, ":")+1:]
	return strings.SplitN(tag, ".", 2)[0]
}

// PostgresClientBinDir is where Debian and Ubuntu install PostgresMajor's
// client programs (/usr/lib/postgresql/<major>/bin, beside any other major),
// and whether this machine has them there.
func PostgresClientBinDir() (string, bool) {
	dir := filepath.Join("/usr/lib/postgresql", PostgresMajor(), "bin")
	_, err := os.Stat(filepath.Join(dir, "pg_restore"))
	return dir, err == nil
}

// UsePostgresClientTools puts PostgresClientBinDir first on PATH for the rest
// of tb, when this machine has it, so every pg_dump and pg_restore the test
// runs, or a child process it starts inherits, is the server's own major
// version. It reports whether it did. Without them the machine's default
// client is left in place, as before, and a mismatch fails the way #279 did.
func UsePostgresClientTools(tb testing.TB) bool {
	tb.Helper()
	dir, ok := PostgresClientBinDir()
	if !ok {
		return false
	}
	tb.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return true
}
