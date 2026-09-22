// This file makes every test in this package run PostgreSQL's client
// programs at the pinned server's major version, when the machine has them.
//
// The tests used to run whichever pg_dump and pg_restore PATH found first, on
// the reasoning that any release able to read a 15.19 archive would do. That
// stopped being true the day this machine's default client became 18: its
// pg_restore reads the archive and then sends the server a setting 15 does not
// have (transaction_timeout), so every restore failed before loading a row.
// Production never meets this, because the backup image carries 15.19's own
// programs beside the controller; these tests have to arrange the same match.
package backup

import (
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestMain puts the pinned major version's client programs first on PATH
// before any test runs (testsupport.PostgresClientBinDir says where).
func TestMain(m *testing.M) {
	if dir, ok := testsupport.PostgresClientBinDir(); ok {
		_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	os.Exit(m.Run())
}
