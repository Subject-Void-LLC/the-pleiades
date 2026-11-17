// Package gnmi: the TLS handshake's own failure kept so a refused
// certificate is said as one, the device's certificate or this side's. gRPC
// reports either only as Unavailable with the error flattened into text.
//
// A TLS 1.3 server that refuses this side's client certificate does so after
// the client's side of the handshake has finished, so gRPC has already
// written its preface, and the alert may be lost to the reset that write
// draws. A connection failure on a client whose server asked for a
// certificate is therefore diagnosed on a fresh connection that writes
// nothing (pkg/devicetls.Watch.Diagnose), which reads the alert, when there is
// one, before anything else.
package gnmi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"google.golang.org/grpc/credentials"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// handshakeSeen holds the last TLS handshake failure on a client's
// connections. It is the client's, not one connection's: a handshake that
// succeeds clears it, so what it holds is always the latest handshake's
// failure, and gRPC dials one connection at a time for a client.
type handshakeSeen struct {
	mu   sync.Mutex
	last error
}

// set records err, the outcome of one handshake: nil clears an earlier
// failure once a handshake succeeds.
func (h *handshakeSeen) set(err error) {
	h.mu.Lock()
	h.last = err
	h.mu.Unlock()
}

// get returns the last handshake failure, or nil.
func (h *handshakeSeen) get() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

// capturingCredentials is TLS transport credentials that keep each client
// handshake's failure, which pkg/devicetls explains: the device's certificate
// refused as an *UntrustedError naming it, and this side's refused as a
// *ClientCertificateRefusedError naming the setting to change. Each stops a
// retry, since it is the same however often it is asked.
type capturingCredentials struct {
	credentials.TransportCredentials
	seen *handshakeSeen
	// presented records the certificate of each handshake that completed
	// (Options.Presented); nil records nothing.
	presented *devicetls.Presented
}

// ClientHandshake implements credentials.TransportCredentials, recording the
// handshake's outcome, and the certificate of one that completed.
func (c *capturingCredentials) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ClientHandshake(ctx, authority, raw)
	c.seen.set(err)
	if tlsInfo, ok := info.(credentials.TLSInfo); ok && err == nil {
		c.presented.Record(tlsInfo.State)
	}
	return conn, info, err
}

// Clone implements credentials.TransportCredentials. The clone shares the
// record, so a failure on any of the client's connections is seen.
func (c *capturingCredentials) Clone() credentials.TransportCredentials {
	return &capturingCredentials{TransportCredentials: c.TransportCredentials.Clone(), seen: c.seen, presented: c.presented}
}

// connectionFailure explains why the client's connection failed, or returns
// nil when nothing explains it (or the client is plaintext): the last
// handshake's failure as pkg/devicetls explains it, or, when the server
// asked for a client certificate and the connection broke after a handshake
// that succeeded, the refusal a diagnosis finds.
func (c *Client) connectionFailure(ctx context.Context) error {
	if c.handshake == nil {
		return nil
	}
	if err := c.handshake.get(); err != nil {
		explained := c.certWatch.Explain(ctx, err)
		var refused *devicetls.ClientCertificateRefusedError
		if errors.As(explained, &refused) {
			return c.refusedConnection(explained)
		}
		return fmt.Errorf("the TLS handshake with %s failed, so nothing was sent: %w", c.target, explained)
	}
	if !c.certWatch.Asked() {
		return nil
	}
	if refused, ok := c.certWatch.Diagnose(ctx, nil); ok {
		return c.refusedConnection(refused)
	}
	return nil
}

// refusedConnection says the server refused the connection over this side's
// client certificate.
func (c *Client) refusedConnection(refusal error) error {
	return fmt.Errorf("the gNMI server at %s refused the TLS connection: %w", c.target, refusal)
}
