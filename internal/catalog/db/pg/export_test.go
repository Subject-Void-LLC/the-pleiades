// Test seams into package pg for its external tests.
package pg

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// SetReadBackForTest replaces dump's read back of the archive it wrote
// until the returned restore runs, so a test can fail it after the archive
// is in place.
func SetReadBackForTest(f func(ctx context.Context, ssh *remoteexec.Conn, dest string) (int64, string, error)) (restore func()) {
	was := readBack
	readBack = f
	return func() { readBack = was }
}
