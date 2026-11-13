// Whether a failure before the command was sent is the network's: the one
// failure a caller can retry knowing nothing ran and nothing will answer
// differently because it asked again.
package winrmexec

import (
	"context"
	"errors"
	"net"
	"net/url"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
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
//
// A call an open circuit refused is unreached too: nothing was sent, and
// the circuit opened because the host stopped answering, which is how a
// run's report reads the same refusal over SSH. It is never retried or
// counted, though: Retryable and the circuit's own record leave it out.
func (e *NotStartedError) Unreached() bool {
	if errors.Is(e.Err, breaker.ErrOpen) {
		return true
	}
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

// Refused reports whether the call ended in a refusal before any shell
// opened: the host turned down the credential (a 401, a TLS alert), or this
// side turned down the host's certificate. Asking again gets the same
// answer, and with a password counts one more failed login toward the
// account's lockout (a domain account's, everywhere at once), so a caller
// that would try again (a task's until, the next item of its loop) stops.
func (e *NotStartedError) Refused() bool {
	return credentialRefused(e.Err) || certificateRefused(e.Err)
}

// Retryable reports whether err is a failure a caller may try again knowing
// nothing ran: the device was unreached, and not because an open circuit
// refused the call, since asking again inside the cooldown is refused the
// same way and after it the try is the probe's to make.
func Retryable(err error) bool {
	var notStarted *NotStartedError
	return errors.As(err, &notStarted) && notStarted.Unreached() && !errors.Is(err, breaker.ErrOpen)
}
