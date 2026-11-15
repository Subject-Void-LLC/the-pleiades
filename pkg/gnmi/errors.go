// Package gnmi: a server's refusal of a call, read into a typed error that
// keeps its gRPC status code. The message is the device's own words, which
// may echo what it was sent, so it is masked by value and by shape before it
// is kept, and quoted so a control character in it is shown, never acted on.
package gnmi

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// Error is a gNMI or gNOI server's refusal of one call.
type Error struct {
	// RPC names the call: Capabilities, Get, Set, Subscribe, or a gNOI
	// method such as System.Time.
	RPC string
	// Code is the gRPC status code the server answered with.
	Code codes.Code
	// Message is the server's message, masked and quoted (farside).
	Message string
}

// Error says the call, the status code and what the server said.
func (e *Error) Error() string {
	return fmt.Sprintf("gnmi %s: the server answered %s: %s", e.RPC, e.Code, e.Message)
}

// RefusedError is a login the server refused (Unauthenticated). It ends the
// client: the answer is the same however often it is asked, and each ask
// with a password is one more failed login toward the account's lockout, so
// the engine's Refused stops a task's until, its loop's next item and a
// Runner's redelivery from asking again.
type RefusedError struct {
	// RPC names the call that was refused.
	RPC string
	// Message is the server's message, masked and quoted.
	Message string
	// NoneSent is true when the client sent no credential metadata, so the
	// server is asking for one the device's record does not send.
	NoneSent bool
}

// Error says the login was refused, or how to send one.
func (e *RefusedError) Error() string {
	if e.NoneSent {
		return fmt.Sprintf("gnmi %s: the server asks for a credential and the device's record sends none (gnmi_auth none): %s", e.RPC, e.Message)
	}
	return fmt.Sprintf("gnmi %s: the server refused the login, which is not sent again: %s", e.RPC, e.Message)
}

// Refused reports that asking again would be refused the same way.
func (e *RefusedError) Refused() bool { return true }

// AnswerLostError is a Set whose answer never came: the deadline passed, or
// the connection broke, after the request was sent over a connection that was
// up. The device may have applied it or not; only reading the paths again
// says which.
type AnswerLostError struct {
	Err error
}

// Error says the answer was lost and what is not known.
func (e *AnswerLostError) Error() string {
	return fmt.Sprintf("gnmi Set: the answer was lost, so whether the device applied the change is not known: read the paths again to see: %v", e.Err)
}

// Unwrap returns why the answer was lost.
func (e *AnswerLostError) Unwrap() error { return e.Err }

// Code returns the gRPC status code err carries: an *Error's, Unauthenticated
// for a *RefusedError, and codes.Unknown for anything else.
func Code(err error) codes.Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	var r *RefusedError
	if errors.As(err, &r) {
		return codes.Unauthenticated
	}
	return codes.Unknown
}

// refused returns the refusal that ended this client, if one did.
func (c *Client) refused() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refusal
}

// fail sorts err, the failure of the call rpc: a refused TLS handshake as
// pkg/devicetls explains it, a refused login as a RefusedError (which ends
// the client), the caller's own deadline or cancellation as itself, and
// every other status as an *Error, its message masked.
func (c *Client) fail(ctx context.Context, rpc string, err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return fmt.Errorf("gnmi %s: %w", rpc, err)
	}
	message := farside.Masked(st.Message(), c.maskSecrets)
	switch st.Code() {
	case codes.Unavailable:
		if hs := c.handshakeFailure(); hs != nil {
			return hs
		}
	case codes.Unauthenticated:
		refusal := &RefusedError{RPC: rpc, Message: message, NoneSent: c.noneSent}
		c.mu.Lock()
		if c.refusal == nil {
			c.refusal = refusal
		}
		c.mu.Unlock()
		return refusal
	case codes.DeadlineExceeded, codes.Canceled:
		// The caller's own context ending is said as itself, so a wait can
		// tell its timeout from the server's refusal.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("gnmi %s: %w", rpc, ctxErr)
		}
	}
	return &Error{RPC: rpc, Code: st.Code(), Message: message}
}
