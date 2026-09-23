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
	"github.com/nats-io/nkeys"
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
	tls   *tls.Config
	creds CredentialSource
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

// CredentialSource returns the credential a connection should present,
// as the body of a NATS .creds file.
//
// It is called again on EVERY reconnect rather than once at dial, which
// is the whole reason it is a function. See WithCredentialSource.
type CredentialSource func() ([]byte, error)

// WithCredentials supplies a fixed mesh identity for this connection.
//
// BYTES RATHER THAN A PATH, deliberately, even though nats.UserCredentials
// takes a filename and would have been less code. The credential contains
// the user's private seed, and the whole point of minting short-lived
// identities is that the seed exists in exactly one place for a bounded
// time. Accepting a path would mean every caller first writes key material
// to a filesystem, where it outlives the process, survives a crash, and
// lands in whatever backs that directory. internal/meshid returns these
// bytes and nothing writes them down. A composition root that does read a
// file uses topology.CredentialsFromEnv and passes the result here.
//
// FIXED, which is a real limitation and is why WithCredentialSource
// exists beside it. A credential minted with an expiry stops working when
// it expires, the broker evicts the connection, and nats.go abandons
// reconnection after the same authentication error twice regardless of
// MaxReconnects. A process that must outlive one credential cannot use
// this.
func WithCredentials(creds []byte) ConnectOption {
	return WithCredentialSource(func() ([]byte, error) { return creds, nil })
}

// WithCredentialSource supplies a credential that is re-read on every
// reconnect.
//
// The distinction from WithCredentials is not stylistic and was measured.
// nats.go invokes the JWT and signature callbacks again on EVERY
// reconnect, not once at dial, so whatever they return the second time is
// what the broker sees. nats.UserCredentials(path) exploits that by
// re-reading its file each time, which is how a rotated credential is
// picked up without a restart. Handing over fixed bytes gives that up:
// the same expired JWT is presented forever.
//
// So a long-lived process takes a source. The Controller's mints a fresh
// credential from the signing key it already holds. A Runner's returns
// whatever its renewal has most recently obtained, falling back to
// re-reading its file.
//
// src must be safe to call from another goroutine, because the reconnect
// that calls it is the driver's, not the caller's.
func WithCredentialSource(src CredentialSource) ConnectOption {
	return func(s *connectSettings) { s.creds = src }
}

// credentialOption turns a credential source into the dial option that
// presents it, keeping the JWT and the seed in memory only.
//
// Both callbacks call src rather than closing over a parsed credential,
// so a source whose answer changes is honored on the next reconnect. The
// key pair is parsed per call and wiped immediately afterwards, which
// costs a base32 decode on each reconnect and is the right trade: the
// alternative is a seed held for the life of the process.
func credentialOption(src CredentialSource) (nats.Option, error) {
	// Neither callback's error is wrapped with the credential in it. A
	// signing failure reaches the client's error handler and the logs, and
	// the one thing that must never arrive there is the key material.
	return nats.UserJWT(
		func() (string, error) {
			creds, err := src()
			if err != nil {
				return "", fmt.Errorf("topology: obtaining the mesh credential: %w", err)
			}
			jwt, err := nkeys.ParseDecoratedJWT(creds)
			if err != nil {
				return "", fmt.Errorf("topology: reading the user jwt from the credential: %w", err)
			}
			return jwt, nil
		},
		func(nonce []byte) ([]byte, error) {
			creds, err := src()
			if err != nil {
				return nil, fmt.Errorf("topology: obtaining the mesh credential: %w", err)
			}
			kp, err := nkeys.ParseDecoratedUserNKey(creds)
			if err != nil {
				return nil, fmt.Errorf("topology: reading the user key from the credential: %w", err)
			}
			defer kp.Wipe()
			return kp.Sign(nonce)
		},
	), nil
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
	if settings.creds != nil {
		// Obtained AND parsed once here, before dialling, so a credential
		// that cannot be fetched or does not hold both halves is a clean
		// error from Connect rather than an authentication failure against
		// the broker later. Checking only that the source returned without
		// error would let malformed bytes straight through, since a source
		// handed fixed bytes has nothing to fail at. The dial path then
		// calls the source again on every reconnect.
		creds, err := settings.creds()
		if err != nil {
			return nil, fmt.Errorf("topology: obtaining the mesh credential: %w", err)
		}
		if err := validateCredential(creds); err != nil {
			return nil, fmt.Errorf("topology: the mesh credential is unusable: %w", err)
		}
		credOpt, err := credentialOption(settings.creds)
		if err != nil {
			return nil, err
		}
		dialOpts = append(dialOpts, credOpt)
	}
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

	// The one misconfiguration that is otherwise completely silent, and
	// the reverse of the one everybody expects.
	//
	// A process with no credential against a broker that requires one
	// fails the dial, loudly, and nobody is confused. A process WITH a
	// credential against a broker that requires nothing succeeds, does
	// all its work, and looks exactly like a correctly secured
	// deployment. An operator who has distributed credentials and
	// believes the mesh is closed has no way to find out that it is not,
	// because every healthy signal is present. The server tells us in its
	// INFO, so this asks.
	//
	// A warning rather than a refusal, deliberately: distributing
	// credentials BEFORE turning the broker on is a legitimate and
	// sensible rollout order, and refusing it would force operators to
	// flip the broker first, which is the order that causes an outage.
	if settings.creds != nil && !nc.AuthRequired() {
		l := logger
		if l == nil {
			l = slog.Default()
		}
		l.Warn("this process presented a mesh credential but the broker does not require one",
			"component", component,
			"meaning", "the broker accepts any client that can reach it, so the credential is proving nothing",
			"fix", "enable authentication on the broker, or unset "+CredentialsEnv+" if this is deliberate")
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
