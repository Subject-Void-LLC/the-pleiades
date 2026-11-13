// Test seams into package pg for its external tests.
package pg

import (
	"context"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// RunToolForTest runs command on the device through the script every tool
// run uses, as a program whose major version is major.
func RunToolForTest(ctx context.Context, ssh *remoteexec.Conn, conn wire.DatabaseConnection, database string, command []string, major int) (remoteexec.Result, error) {
	return toolRun{conn: conn, database: database, command: command, major: major, timeout: time.Minute}.run(ctx, ssh)
}

// SetReadBackForTest replaces dump's read back of the archive it wrote
// until the returned restore runs, so a test can fail it after the archive
// is in place.
func SetReadBackForTest(f func(ctx context.Context, ssh *remoteexec.Conn, dest string) (int64, string, error)) (restore func()) {
	was := readBack
	readBack = f
	return func() { readBack = was }
}
