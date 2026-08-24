// Establishing a NATS connection, and waiting for one to become usable.
//
// This is the other half of dial.go's contract. DialOptions decides how a
// connection behaves; this file decides when a caller may start using
// one, which stopped being trivial the moment RetryOnFailedConnect made
// nats.Connect return before connecting.

package topology

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// Connect dials url with the shared options and returns only once the
// connection is actually usable, or an error.
//
// This is the single entry point every composition root and constructor
// uses. It exists because DialOptions alone is half a contract:
// RetryOnFailedConnect(true) makes nats.Connect return a handle that is
// not connected to anything yet, so any caller that immediately builds a
// JetStream context, ensures a stream or creates a bucket must wait
// first. That wait was originally copy-pasted at all five dial sites,
// which meant a sixth site could satisfy the archtest guard, carry the
// right options, and still hand a dead handle to jetstream.New. Pairing
// the two here makes that unrepresentable rather than merely discouraged,
// and lets internal/archtest forbid a bare nats.Connect outside this
// package outright.
//
// component names the dialing subsystem and reaches the server as part of
// the connection name. logger may be nil, in which case slog.Default() is
// used. On any failure the connection is closed before returning, so a
// caller that gets an error owns nothing.
// ConnectOption adjusts a single connection beyond the shared option set.
//
// It exists for exactly one thing today, and the shape is deliberately
// narrow so it does not become a second place NATS behaviour is
// configured: DialOptions remains the single owner of how a connection
// behaves, and this carries only material a caller must supply because
// this package cannot read it from anywhere.
type ConnectOption func(*connectSettings)

type connectSettings struct {
	tls *tls.Config
}

// WithTLS supplies the client TLS configuration for a tls:// or wss://
// broker.
//
// Take the value from internal/tlscert's ServingCert.TLSClientConfig
// rather than building a tls.Config here. That helper exists precisely so
// no caller hand-writes one, which is where InsecureSkipVerify gets typed,
// and it carries the same TLS 1.2 floor this module states once for every
// direction.
func WithTLS(cfg *tls.Config) ConnectOption {
	return func(s *connectSettings) { s.tls = cfg }
}

func Connect(ctx context.Context, url string, logger *slog.Logger, component string, opts ...ConnectOption) (*nats.Conn, error) {
	// Validated here rather than at each composition root, so cmd/demo is
	// covered too: it reads no environment at all and dials a hardcoded
	// default, so an env-level check would have skipped the one site
	// nobody watches. This is also the only place every dial in the module
	// passes through, which is what makes the check unavoidable rather
	// than conventional.
	if err := ValidateNatsURL(url); err != nil {
		return nil, err
	}

	var settings connectSettings
	for _, opt := range opts {
		opt(&settings)
	}

	dialOpts := DialOptions(logger, component)
	if settings.tls != nil {
		// Secure first, then the config: nats.Secure turns TLS on, and
		// passing a *tls.Config to it is what makes verification use the
		// caller's root pool rather than the system one.
		dialOpts = append(dialOpts, nats.Secure(settings.tls))
	}

	nc, err := nats.Connect(url, dialOpts...)
	if err != nil {
		return nil, err
	}

	waitCtx, cancel := context.WithTimeout(ctx, ConnectWaitTimeout)
	defer cancel()
	if err := WaitForConnect(waitCtx, nc); err != nil {
		nc.Close()
		return nil, err
	}
	return nc, nil
}

// errClosedBeforeConnect is returned when a connection reached CLOSED
// without ever connecting.
//
// It carries no wrapped cause, and that is a measured decision rather
// than an omission. The obvious version wrapped nc.LastError(), on the
// reasoning that the driver's own error distinguishes "wrong address"
// from "broker not started yet". Measured against a real client dialing
// an unroutable address for twelve seconds, LastError() is nil the entire
// time and stays nil after Close: nats.go assigns nc.err when the client
// GIVES UP, and MaxReconnects(-1) means it never does. So the wrap was
// unreachable by construction, and worse than unreachable, because
// fmt.Errorf with %w and a nil error yields the literal text
// "%!w(<nil>)" and an error with no unwrap chain.
//
// The information itself is not lost. Every failed attempt reaches the
// operator through the ReconnectErrHandler that DialOptions installs,
// which logs the driver's real error ("dial tcp 192.0.2.1:4222: i/o
// timeout") on each try.
var errClosedBeforeConnect = errors.New("nats connection was closed before it connected")

// closedBeforeConnect returns the error for a connection that reached
// CLOSED without ever connecting.
func closedBeforeConnect(*nats.Conn) error { return errClosedBeforeConnect }

// connectPollInterval is how often WaitForConnect re-reads the
// connection's own state rather than waiting to be told about it.
const connectPollInterval = 250 * time.Millisecond

// connectState reports whether WaitForConnect can stop waiting on nc, and
// with what result. done is false while the connection is still trying.
//
// It is a function rather than three inline checks because WaitForConnect
// asks this same question from three places (before waiting at all, on
// every wakeup, and once more when the context ends), and three copies of
// a three-way state check is how one of them ends up subtly different.
// Being a function also makes every arm reachable from a test against a
// real connection, which inline arms inside a select are not.
func connectState(nc *nats.Conn) (done bool, err error) {
	switch {
	case nc.IsConnected():
		return true, nil
	case nc.IsClosed():
		return true, closedBeforeConnect(nc)
	default:
		return false, nil
	}
}

// WaitForConnect blocks until nc has completed its first successful
// connection, or ctx ends.
//
// It exists because RetryOnFailedConnect(true), set by DialOptions, changes
// what nats.Connect returns. Without it, Connect against an unreachable
// broker returns an error and every composition root in this module treats
// that error as fatal. With it, Connect returns a *Conn that is not
// connected to anything yet and is working on it in the background. That is
// the behaviour we want at the edge, and it is a trap for anything that
// immediately uses the handle, which is why Connect above pairs the two and
// callers are expected to use Connect rather than this directly.
//
// The status channel and the ticker are both only WAKEUPS: every decision
// is made by re-reading the connection through connectState. That matters
// because nats.go's status delivery is explicitly best-effort.
// sendStatusEvent tests a listener channel for closure with a NON-BLOCKING
// RECEIVE, so a buffered event nobody has read yet satisfies that receive,
// is silently drained, and the listener is deregistered permanently. A
// connection that reaches CONNECTED, drops, and reaches CONNECTED again
// inside the window this function is setting up can therefore lose its
// notification for good. Treating the channel as a hint rather than as the
// truth turns that from a failed startup against a healthy connection into
// a wait that is at most connectPollInterval late.
func WaitForConnect(ctx context.Context, nc *nats.Conn) error {
	statusCh := nc.StatusChanged(nats.CONNECTED, nats.CLOSED)
	defer nc.RemoveStatusListener(statusCh)

	poll := time.NewTicker(connectPollInterval)
	defer poll.Stop()

	for {
		// Checked before the first wait, not after it: the connection can
		// reach CONNECTED before the listener above was registered, and a
		// listener registered afterwards is never told about a transition
		// that already happened.
		if done, err := connectState(nc); done {
			return err
		}

		select {
		case <-ctx.Done():
			if done, err := connectState(nc); done {
				return err
			}
			// Deliberately not enriched with nc.LastError(): see
			// errClosedBeforeConnect for the measurement showing it is
			// nil throughout, and for where the driver's real error does
			// reach the operator.
			return fmt.Errorf("timed out waiting for the first nats connection: %w", ctx.Err())
		case <-poll.C:
		case <-statusCh:
		}
	}
}
