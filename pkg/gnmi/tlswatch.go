// Package gnmi: the TLS handshake's own failure kept so a refused
// certificate is said as one, the device's certificate or this side's. gRPC
// reports either only as Unavailable with the error flattened into text.
//
// A TLS 1.3 server that requires a client certificate refuses one (or its
// absence) after the client's side of the handshake has finished, with an
// alert the client meets on its first read. gRPC writes its preface at once,
// and a write that reaches the server after it closed the connection draws a
// reset, which can discard the alert unread. So after a TLS 1.3 handshake in
// which the server asked for a client certificate, the client reads first:
// an HTTP/2 server sends its SETTINGS as soon as the connection is up, and
// the alert, when there is one, comes before it.
package gnmi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"google.golang.org/grpc/credentials"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// peekTimeout bounds the first read after a TLS 1.3 handshake in which the
// server asked for a client certificate. A server that sends nothing first
// in that time is taken as up, and the connection is used as it is.
const peekTimeout = 5 * time.Second

// handshakeSeen holds the last TLS handshake failure on a client's
// connections: the handshake's own, or a server's alert about the client's
// certificate read just after it.
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
// handshake's failure: pkg/devicetls turns Go's refusal of the device's
// certificate into an *UntrustedError naming it, and the device's refusal of
// this side's into a *ClientCertificateRefusedError naming the setting to
// change; each stops a retry, since it is the same however often it is asked.
type capturingCredentials struct {
	credentials.TransportCredentials
	seen *handshakeSeen
	// watch records whether the server asked for a client certificate and
	// whether one was presented.
	watch *devicetls.Watch
}

// ClientHandshake implements credentials.TransportCredentials, recording the
// handshake's outcome and, after a TLS 1.3 one in which the server asked for
// a client certificate, its first read, and returning the connection wrapped
// so an alert read later is recorded too.
func (c *capturingCredentials) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ClientHandshake(ctx, authority, raw)
	c.seen.set(err)
	if err != nil {
		return conn, info, err
	}
	watched := &alertConn{Conn: conn, seen: c.seen, watch: c.watch}
	if tlsInfo, ok := info.(credentials.TLSInfo); ok && tlsInfo.State.Version == tls.VersionTLS13 && c.watch.Asked() {
		if err := watched.peek(ctx); err != nil {
			_ = conn.Close()
			c.seen.set(err)
			return nil, nil, err
		}
	}
	// gRPC reads socket options through syscall.Conn where the connection
	// offers it, so the wrapper keeps offering it.
	if sc, ok := conn.(syscall.Conn); ok {
		return alertSyscallConn{alertConn: watched, sc: sc}, info, nil
	}
	return watched, info, nil
}

// Clone implements credentials.TransportCredentials. The clone shares the
// record and the watch, so a failure on any of the client's connections is
// seen.
func (c *capturingCredentials) Clone() credentials.TransportCredentials {
	return &capturingCredentials{TransportCredentials: c.TransportCredentials.Clone(), seen: c.seen, watch: c.watch}
}

// alertConn is a TLS connection that records a read failing on the server's
// alert about the client's certificate.
type alertConn struct {
	net.Conn
	seen  *handshakeSeen
	watch *devicetls.Watch
	// pending is what the first read took in, handed to the next Read.
	pending []byte
}

// peek reads the server's first byte before anything is written, within
// peekTimeout or ctx's deadline, whichever is sooner: an alert, or the
// connection ending, is returned; a byte is kept for the next Read; and a
// server sending nothing in time is no failure.
func (a *alertConn) peek(ctx context.Context) error {
	deadline := time.Now().Add(peekTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := a.Conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	first := make([]byte, 1)
	n, err := a.Conn.Read(first)
	// A read that timed out leaves a TLS connection usable: Go keeps only
	// errors that are not temporary.
	if clearErr := a.Conn.SetReadDeadline(time.Time{}); clearErr != nil && err == nil {
		err = clearErr
	}
	var timeout net.Error
	if err != nil && !(errors.As(err, &timeout) && timeout.Timeout()) {
		return err
	}
	a.pending = first[:n]
	return nil
}

// Read implements net.Conn: what the first read took in, then the
// connection's own, recording a client-certificate alert.
func (a *alertConn) Read(b []byte) (int, error) {
	if len(a.pending) > 0 {
		n := copy(b, a.pending)
		a.pending = a.pending[n:]
		return n, nil
	}
	n, err := a.Conn.Read(b)
	if err != nil {
		if _, refused := a.watch.Refusal(err); refused {
			a.seen.set(err)
		}
	}
	return n, err
}

// alertSyscallConn is an alertConn over a connection that offers
// syscall.Conn.
type alertSyscallConn struct {
	*alertConn
	sc syscall.Conn
}

// SyscallConn implements syscall.Conn with the wrapped connection's.
func (a alertSyscallConn) SyscallConn() (syscall.RawConn, error) {
	return a.sc.SyscallConn()
}

// handshakeFailure returns the client's last TLS handshake failure as
// pkg/devicetls explains it, or nil when there is none (or the client is
// plaintext): a certificate this side refused, a client certificate the
// server refused (or the lack of one), or the failure as it is.
func (c *Client) handshakeFailure() error {
	if c.handshake == nil {
		return nil
	}
	err := c.handshake.get()
	if err == nil {
		return nil
	}
	explained := c.certWatch.Explain(err)
	var refused *devicetls.ClientCertificateRefusedError
	if errors.As(explained, &refused) {
		// The server read nothing past the certificate it refused.
		return fmt.Errorf("the gNMI server at %s refused the TLS connection: %w", c.target, explained)
	}
	return fmt.Errorf("the TLS handshake with %s failed, so nothing was sent: %w", c.target, explained)
}
