// Package pseudotty: one pseudo terminal for a test of code that opens a
// serial device by its path, with the test holding the other end.
//
// The code under test opens the terminal's device path (the slave side),
// exactly as it opens a real serial port, and the test reads and writes the
// master side directly. That replaces a socat-linked pair of terminals: no
// program has to be installed, and no second line discipline sits between
// the two ends.
//
// It lives apart from internal/testsupport, which imports testcontainers-go,
// for the reason need and refusing do: a test using it pulls in only the
// standard library, need and github.com/creack/pty.
package pseudotty

import (
	"fmt"
	"os"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/creack/pty"
)

// Need is the need.Require name Open skips under where the machine cannot
// give a pseudo terminal (Windows). CI's Linux jobs require it.
const Need = need.PTY

// openPTY is the call Open gets its terminal from; a test replaces it to
// reach the failure a machine with pseudo terminals never gives.
var openPTY = pty.Open

// Open returns the device path of a new pseudo terminal's slave side, for
// the code under test to open, and its master side, far, for the test to
// read and write. Both close when t ends.
//
// Open keeps its own slave descriptor open until then. When the last
// descriptor of a slave closes, a read of the master fails (EIO on Linux,
// end of file on macOS and FreeBSD) and input not yet read is dropped, so
// code under test that opens and closes the device more than once would
// otherwise break the terminal between its calls. Cleanup closes the slave
// before the master, which wakes a goroutine still blocked reading far.
//
// The slave is left in the mode the kernel creates it in (canonical, with
// echo): the code under test sets the line up itself, and a test of it
// proves that only if nothing has done the work first.
//
// On a machine without pseudo terminals Open stops t through
// need.Require(Need) and returns empty values, which only a recording
// testing.TB reaches.
func Open(t testing.TB) (device string, far *os.File) {
	t.Helper()
	master, slave, err := openPTY()
	need.Require(t, Need, err == nil, fmt.Sprintf("opening a pseudo terminal: %v", err))
	if err != nil {
		return "", nil // t was stopped; only a recording TB carries on to here
	}
	t.Cleanup(func() {
		// The slave first: it ends any read still blocked on the master.
		_ = slave.Close()
		_ = master.Close()
	})
	return slave.Name(), master
}
