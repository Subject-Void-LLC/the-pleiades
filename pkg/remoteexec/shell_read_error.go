// The error a Shell read ends in, carrying what the read had got so far.
package remoteexec

// ReadError is a ReadUntil that ended without matching: its context ran
// out, the session closed, or the byte bound was reached. Its message is
// the read's own, unchanged; Partial is what the read had accumulated
// when it ended, which the message never includes.
//
// A caller decides what of Partial is safe to show. A read that stopped
// on a device's question is worth naming, and pkg/netcli's Dialog names
// that one line; a read that stopped partway through a configuration dump
// may hold a secret, so nothing here prints it.
type ReadError struct {
	// Partial is what had been read when the read ended.
	Partial []byte

	err error
}

// Error is the read's own message.
func (e *ReadError) Error() string { return e.err.Error() }

// Unwrap returns the cause: the context's error, the session's I/O
// error, or the byte bound.
func (e *ReadError) Unwrap() error { return e.err }
