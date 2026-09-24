// Turning a panic inside github.com/pin/tftp/v3 into an error, while a
// panic in the caller's own reader or writer stays the caller's.
package tftpxfer

import (
	"errors"
	"fmt"
	"io"
)

// errLibraryPanic marks a panic inside github.com/pin/tftp/v3 that Get or
// Put recovered and returned as an error.
var errLibraryPanic = errors.New("the TFTP library panicked")

// callerPanic carries a panic raised by the caller's own reader or writer
// out through the library's frames, so recoverLibraryPanic can tell it
// from a panic inside the library and raise it again unchanged.
type callerPanic struct{ value any }

// markCallerPanic, deferred directly in a caller-facing Read or Write,
// relabels a panic there as the caller's.
func markCallerPanic() {
	if v := recover(); v != nil {
		panic(callerPanic{value: v})
	}
}

// recoverLibraryPanic turns a value recovered in Get or Put into their
// error, unless it is the caller's own panic, which it raises again.
func recoverLibraryPanic(v any, remoteFilename string) error {
	if p, ok := v.(callerPanic); ok {
		panic(p.value)
	}
	return fmt.Errorf("tftpxfer: transferring %q: %w: %v", remoteFilename, errLibraryPanic, v)
}

// callerWriter hands the library Get's destination, counting the bytes
// that reached it so a recovered panic still reports how much was written.
type callerWriter struct {
	w io.Writer
	n int64
}

// Write passes p to the caller's writer.
func (c *callerWriter) Write(p []byte) (n int, err error) {
	defer markCallerPanic()
	n, err = c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// callerReader hands the library Put's source, counting the bytes read
// from it so a recovered panic still reports how much was consumed. The
// library asks a reader for io.Seeker only when a tsize option is pending,
// which happens on a server's read path and never on this client's, so
// wrapping it hides nothing.
type callerReader struct {
	r io.Reader
	n int64
}

// Read fills p from the caller's reader.
func (c *callerReader) Read(p []byte) (n int, err error) {
	defer markCallerPanic()
	n, err = c.r.Read(p)
	c.n += int64(n)
	return n, err
}
