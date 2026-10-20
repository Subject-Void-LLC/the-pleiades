// Whether a failure before the command was sent is the network's: the one
// failure a caller can retry knowing nothing ran and nothing will answer
// differently because it asked again.
package winrmexec

import (
	"context"
	"errors"
	"net"
	"net/url"
)

// Unreached reports whether the device never answered: a connection that
// failed or timed out, or the operation's deadline passing before a shell
// opened. Nothing was sent, so a retry is safe.
//
// A failure the device sent back is not: a refused login (HTTP 401), a TLS
// alert, a fault instead of a shell. The host is reachable and refusing
// something, and asking again presents the same credential, which for a
// password is one more failed login toward the account's lockout. Go reports
// a TLS alert as a *net.OpError with Op "remote error", so that one is
// excluded by name. A caller's own cancellation is not the network either.
func (e *NotStartedError) Unreached() bool {
	var opErr *net.OpError
	if errors.As(e.Err, &opErr) {
		return opErr.Op != "remote error"
	}
	var urlErr *url.Error
	if errors.As(e.Err, &urlErr) && urlErr.Timeout() {
		return true
	}
	return errors.Is(e.Err, context.DeadlineExceeded)
}
