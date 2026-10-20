// The error a connection that never opened returns, and whether the device
// never answered it.
package remoteexec

import (
	"context"
	"errors"
	"fmt"
)

// DialError is a failure to open the SSH connection to Addr, or to one hop
// on the way: nothing reached a shell on the device, so nothing ran.
type DialError struct {
	// Addr is the address that was being dialed.
	Addr string
	// Err is why the dial failed.
	Err error
}

// Error implements error, in the words this package always used for it.
func (e *DialError) Error() string { return fmt.Sprintf("remoteexec: dial %s: %v", e.Addr, e.Err) }

// Unwrap exposes Err to errors.Is and errors.As.
func (e *DialError) Unwrap() error { return e.Err }

// Unreached reports whether the device never answered: the TCP connection
// or the SSH handshake failed or timed out. A retry is safe, since nothing
// was sent. A refused credential and a refused host key are answers, not
// silence: the device is reachable, and asking again changes nothing (a
// refused password counts toward the account's lockout). A caller's own
// cancellation is not the network either.
func (e *DialError) Unreached() bool {
	return !authRejected(e.Err) && !hostKeyRefused(e.Err) && !errors.Is(e.Err, context.Canceled)
}
