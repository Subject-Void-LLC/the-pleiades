// A run whose output is held to a bound: a remote command that writes more
// than its caller can use is cut off and reported, never buffered without end.
package remoteexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
)

// ErrOutputTooLarge is the error Run, RunWithStdin and RunWithStdinBounded
// return, wrapped, when a command writes more than its bound to standard
// output or standard error.
var ErrOutputTooLarge = errors.New("the command's output passed its bound")

// DefaultOutputLimit is the bound on each of standard output and standard
// error for Run and RunWithStdin: 64 MiB, far past anything a method reads
// back (a package list, a configuration file, a command's report), and far
// short of what a device that writes without end would make of the memory of
// the process running the task. A result that size is still held in memory,
// recorded as a stat and carried in a run's report, so output this large is
// better fetched as a file.
const DefaultOutputLimit = 64 << 20

// defaultOutputLimit is DefaultOutputLimit, as a variable so this package's
// tests can watch the bound end a command without making 64 MiB.
var defaultOutputLimit = DefaultOutputLimit

// RunWithStdinBounded is RunWithStdin with each of standard output and
// standard error held to limit bytes, which must be above zero.
//
// RunWithStdin holds a command to DefaultOutputLimit; this is for a caller
// that knows its command writes far less and wants a refusal sooner, as a
// method parsing a one-line answer does. Past the bound, the session is
// closed so the remote stops writing, the connection is tainted like any
// other cut-off command, and the call fails with ErrOutputTooLarge: output
// that was not kept whole is never returned as if it were.
func (c *Conn) RunWithStdinBounded(ctx context.Context, command string, stdin io.Reader, limit int) (Result, error) {
	if limit <= 0 {
		return Result{}, errors.New("remoteexec: an output bound must be above zero")
	}
	return c.run(ctx, command, stdin, limit)
}

// cappedBuffer is one stream's buffer. With a limit above zero, a write that
// would take it past the limit is refused, marks the run exceeded and closes
// its session (cut), which ends the remote command's output there.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	exceeded *atomic.Bool
	cut      func()
}

// Write appends p, or refuses it past the limit.
func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit > 0 && b.buf.Len()+len(p) > b.limit {
		b.exceeded.Store(true)
		b.cut()
		return 0, ErrOutputTooLarge
	}
	return b.buf.Write(p)
}
