// Package gnmi is an OpenConfig gNMI client: a device's configuration and
// state as YANG-modeled paths, read with Get and Subscribe and written with
// Set, over gRPC, beside a small read-only subset of gNOI (System.Time,
// File.Stat and CertificateManagement.GetCertificates).
//
// It reaches a device the way every other device API here is reached: the
// device's own target (host:port, never a resolver scheme), its own TLS
// (pkg/devicetls: always verified, a pinned certificate included, mutual TLS
// from the stored certificate), no proxy, and its own stored credential, sent
// as the username and password metadata the gNMI specification names, as a
// bearer token, or not at all. A connection without TLS is made only when the
// device's record asks for one, and a credential is sent over it only when
// the record asks for that as well: on a plaintext connection every message
// can be read on the network, the credential included. HPACK sends a header
// value as written or Huffman-coded, whichever is shorter, so the credential
// may even show in a plain byte search of the traffic, and anyone decoding
// the frames reads it either way.
//
// Every call is bounded in time as well as size: Capabilities, Get, Set, each
// gNOI call, a ONCE subscription and each round of a POLL one end at the
// client's call timeout (Options.CallTimeout, DefaultCallTimeout unless set)
// or the caller's deadline, whichever comes first, so a server that accepts a
// call and never answers cannot hold a run open. A STREAM subscription is
// meant to last and is bounded by its caller alone; under it, and under every
// call, keepalive pings close a connection whose server stopped answering.
//
// Every message is bounded (MaxMessageBytes), every path is built from parts
// (Path, Elem) and checked before it is sent, and every value a device
// answers is decoded with a bound on its size and its depth, an answer that
// breaks one being refused whole rather than cut. A device's refusal comes
// back as an *Error carrying its gRPC status code, its message masked by value
// (the client's own credential) and by shape (pkg/netcfg) before any of it is
// kept. A refused login (Unauthenticated) ends the client: every later call
// returns the same refusal unsent, since each login sent is one more failed
// attempt toward the account's lockout. That holds for one client, and each
// task makes its own, so a later task against the same device sends the
// login once more.
//
// gNMI is device-facing gRPC. It has nothing to do with the mesh's own
// Runner to Controller communication, which is NATS and pull-only.
package gnmi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// The ways a device's stored credential is sent (GNMIAuth).
const (
	// AuthPassword sends the credential's username and password as the
	// "username" and "password" metadata the gNMI specification's
	// authentication section names, on every call.
	AuthPassword = "password"
	// AuthBearer sends the credential's token as "authorization: Bearer".
	AuthBearer = "bearer"
	// AuthNone sends no credential metadata, as with a client certificate
	// alone or a server that authenticates nobody.
	AuthNone = "none"
)

// The bounds on what one call may carry.
const (
	// MaxMessageBytes bounds one message the device sends: a Get of a whole
	// tree is one message, so it is generous, and a message past it is
	// refused by gRPC rather than read.
	MaxMessageBytes = 64 << 20
	// MaxSendBytes bounds one message this client sends.
	MaxSendBytes = 16 << 20
)

// DefaultCallTimeout bounds one call when Options.CallTimeout is zero, as
// pkg/restconf bounds each request: a minute is long for a device to answer
// a read or apply a Set, and short enough that a server which never answers
// does not hold a run open.
const DefaultCallTimeout = time.Minute

// keepaliveTime and keepaliveTimeout are how the client finds a connection
// whose server stopped answering, under a call or a stream that is still
// open: after keepaliveTime with nothing read it pings the server, and closes
// the connection when no answer comes within keepaliveTimeout, which ends
// every call on it. The time is past five minutes because a gRPC server by
// default refuses a client pinging more often than that (GOAWAY,
// ENHANCE_YOUR_CALM, "too_many_pings"), which would end the very stream the
// ping protects. They are variables only so a test can prove the bound
// quickly (gRPC holds the time to at least ten seconds); nothing else sets
// them.
var (
	keepaliveTime    = 5*time.Minute + 30*time.Second
	keepaliveTimeout = 20 * time.Second
)

// Options says how a client reaches one gNMI server.
type Options struct {
	// Target is the server's address, host:port (ValidateTarget).
	Target string
	// TLS is the client's TLS configuration, pkg/devicetls's Config; it must
	// be nil exactly when Plaintext is true.
	TLS *tls.Config
	// Plaintext connects without TLS. It is never implied by a nil TLS: a
	// caller that forgot its TLS configuration gets an error, not plaintext.
	Plaintext bool
	// Auth is how Secrets' credential is sent: AuthPassword, AuthBearer or
	// AuthNone.
	Auth string
	// AllowPlaintextCredentials accepts sending a credential over a
	// plaintext connection.
	AllowPlaintextCredentials bool
	// Secrets is the device's own credential, flattened under the
	// wire.Secret* keys.
	Secrets map[string]string
	// CallTimeout bounds each call from its start to its answer (a POLL
	// subscription's each round); zero is DefaultCallTimeout, and a negative
	// one is refused.
	CallTimeout time.Duration
	// PrefixTarget is the target every Get, Set and Subscribe prefix names
	// unless the request names its own (ValidatePrefixTarget), empty for
	// none. A server serving more than one data tree (SONiC: OC-YANG,
	// CONFIG_DB and others) may refuse a request that names none.
	PrefixTarget string
}

// Client is a gNMI client for one device. It is safe for concurrent use.
type Client struct {
	// conn is the gRPC channel; it connects on the first call.
	conn *grpc.ClientConn
	// api is the gNMI service over conn.
	api gpb.GNMIClient
	// target is the server's host:port.
	target string
	// verified is true over TLS, where the server's certificate is checked
	// on every connection.
	verified bool
	// handshake keeps the last TLS handshake failure, so a refused
	// certificate is reported as one rather than as gRPC's Unavailable.
	handshake *handshakeSeen
	// certWatch records whether the server asked for a client certificate
	// and whether one was presented, so a refusal says which; nil over
	// plaintext.
	certWatch *devicetls.Watch
	// secrets is the device's own credential, masked in every answer.
	secrets map[string]string
	// noneSent is true when the client sends no credential metadata.
	noneSent bool
	// callTimeout bounds each call (Options.CallTimeout).
	callTimeout time.Duration
	// prefixTarget is the target a request naming none takes
	// (Options.PrefixTarget).
	prefixTarget string

	mu sync.Mutex
	// extraSecrets are further values masked in every answer (MaskAlso).
	extraSecrets []string
	// refusal is the RefusedError that ended this client's use of the
	// credential, which every later call returns unsent.
	refusal error
}

// New returns a client for the gNMI server opts names. It connects nothing:
// the first call does. It refuses a target that is not host:port, a TLS
// configuration that contradicts Plaintext, a credential mode it does not
// know, a mode whose credential the secrets do not hold, and a credential
// over a plaintext connection the options do not accept.
func New(opts Options) (*Client, error) {
	if _, err := ValidateTarget(opts.Target); err != nil {
		return nil, err
	}
	switch {
	case opts.Plaintext && opts.TLS != nil:
		return nil, errors.New("a plaintext connection takes no TLS configuration")
	case !opts.Plaintext && opts.TLS == nil:
		return nil, errors.New("no TLS configuration was given and plaintext was not asked for")
	}
	perRPC, err := credentialFor(opts.Auth, opts.Secrets, opts.AllowPlaintextCredentials)
	if err != nil {
		return nil, err
	}
	if opts.Plaintext && perRPC != nil && !opts.AllowPlaintextCredentials {
		return nil, errors.New("the device's credential would cross a plaintext connection unencrypted, and its record does not accept that: refusing to send it")
	}
	if opts.CallTimeout < 0 {
		return nil, errors.New("a call timeout is not negative")
	}
	if err := ValidatePrefixTarget(opts.PrefixTarget); err != nil {
		return nil, err
	}
	c := &Client{target: opts.Target, verified: !opts.Plaintext, secrets: opts.Secrets, noneSent: perRPC == nil, callTimeout: opts.CallTimeout, prefixTarget: opts.PrefixTarget}
	if c.callTimeout == 0 {
		c.callTimeout = DefaultCallTimeout
	}
	var transport credentials.TransportCredentials
	if opts.Plaintext {
		transport = insecure.NewCredentials()
	} else {
		// gRPC offers h2 alone, so a diagnosis offers the same.
		cfg, watch := devicetls.Watched(opts.TLS, opts.Target, "h2")
		c.handshake, c.certWatch = &handshakeSeen{}, watch
		transport = &capturingCredentials{TransportCredentials: credentials.NewTLS(cfg), seen: c.handshake}
	}
	dial := []grpc.DialOption{
		grpc.WithTransportCredentials(transport),
		// The target is an address, never a name a proxy chooses the route
		// to: the credential belongs to the device.
		grpc.WithNoProxy(),
		// A call is sent once. A Set retried by the channel could apply twice,
		// and a refused login retried is one more failed attempt.
		grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageBytes), grpc.MaxCallSendMsgSize(MaxSendBytes)),
		// Pings only while a call or a stream is open, so an idle client
		// sends none.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: keepaliveTime, Timeout: keepaliveTimeout}),
	}
	if perRPC != nil {
		dial = append(dial, grpc.WithPerRPCCredentials(perRPC))
	}
	// passthrough hands the target to the dialer as written: no resolver
	// scheme an inventory value could choose.
	conn, err := grpc.NewClient("passthrough:///"+opts.Target, dial...)
	if err != nil {
		return nil, err
	}
	c.conn, c.api = conn, gpb.NewGNMIClient(conn)
	return c, nil
}

// Close closes the client's connection. A call after Close fails.
func (c *Client) Close() error {
	return c.conn.Close()
}

// Verified reports whether the server's identity is verified: true over
// TLS, where pkg/devicetls always verifies the certificate, and false over a
// plaintext connection, where nothing proves who answered.
func (c *Client) Verified() bool {
	return c.verified
}

// Target returns the server's host:port.
func (c *Client) Target() string {
	return c.target
}

// PrefixTarget returns the target a request naming none takes, empty for
// none.
func (c *Client) PrefixTarget() string {
	return c.prefixTarget
}

// targetOf is the target a request's prefix names: the request's own, none
// for NoPrefixTarget, or the client's when it names none.
func (c *Client) targetOf(requested string) string {
	if requested == NoPrefixTarget {
		return ""
	}
	if requested != "" {
		return requested
	}
	return c.prefixTarget
}

// ready connects the channel and waits until it is ready, so a call that
// changes something (Set) is sent only over a connection that came up: a
// failure after that is a lost answer, never a refusal of something unsent.
// A refused TLS handshake is returned as pkg/devicetls explains it.
func (c *Client) ready(ctx context.Context) error {
	c.conn.Connect()
	for {
		state := c.conn.GetState()
		switch state {
		case connectivity.Ready:
			return nil
		case connectivity.TransientFailure, connectivity.Shutdown:
			if err := c.connectionFailure(ctx); err != nil {
				return err
			}
			return fmt.Errorf("the connection to %s did not come up (%s)", c.target, state)
		}
		// WaitForStateChange returns false only when ctx ends.
		if !c.conn.WaitForStateChange(ctx, state) {
			return fmt.Errorf("the connection to %s did not come up: %w", c.target, context.Cause(ctx))
		}
	}
}

// call is ctx bounded by the client's call timeout, for one call: the
// earlier of the two ends it. When the client's bound is what ends it, the
// context's cause (and so the call's error) says the server did not answer in
// time, never that the caller gave up.
func (c *Client) call(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, c.callTimeout, c.timedOut())
}

// timedOut is the cause a call ends with when the client's own bound passes:
// still a context.DeadlineExceeded under errors.Is.
func (c *Client) timedOut() error {
	return fmt.Errorf("the server did not answer within %s: %w", c.callTimeout, context.DeadlineExceeded)
}
