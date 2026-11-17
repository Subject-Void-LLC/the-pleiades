// Package devicetls: the certificate a device presented, recorded from each
// connection a caller's clients completed under one context, so onboarding
// and a facts gatherer record it from the connections they already make,
// with no connection of their own.
//
// A certificate is recorded only from a completed handshake: an HTTP
// response's TLS state (Transport), a gRPC handshake's AuthInfo, a dialed
// connection's state (Record). It is never recorded from VerifyConnection,
// which Go's client calls after checking the chain or the pin and before the
// server has proved it holds the certificate's key (TLS 1.3's
// CertificateVerify, TLS 1.2's signed key exchange): a server replaying a
// trusted certificate without its key would otherwise be recorded as the
// device, and a retry to the real device would leave two certificates.
//
// It travels on a context as the key log does (WithPresented), and
// PresentedFrom hands it to a client built under that context. Every method
// of a nil *Presented does nothing, so a client records unconditionally.
package devicetls

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"sync"
)

// maxPresented bounds how many distinct certificates one Presented keeps,
// so a server that presents a new one on every connection cannot grow it.
const maxPresented = 8

// Presented holds the distinct certificates the devices presented on the
// completed handshakes of the clients built under one context. It is safe
// for concurrent use.
type Presented struct {
	mu sync.Mutex
	// certs are the distinct leaf certificates seen, in the order first
	// seen, at most maxPresented.
	certs []*x509.Certificate
	// more is set once a certificate past maxPresented was seen.
	more bool
}

// presentedKey is the context key a Presented travels under.
type presentedKey struct{}

// WithPresented returns ctx carrying a new Presented, and that Presented,
// for the clients built under the returned context to record on.
func WithPresented(ctx context.Context) (context.Context, *Presented) {
	p := &Presented{}
	return context.WithValue(ctx, presentedKey{}, p), p
}

// PresentedFrom returns the Presented ctx carries, nil when it carries
// none, for a client built under ctx to record its completed handshakes on.
func PresentedFrom(ctx context.Context) *Presented {
	p, _ := ctx.Value(presentedKey{}).(*Presented)
	return p
}

// Record records the leaf certificate of cs when its handshake completed,
// unless it was seen already. A handshake that did not complete records
// nothing: the server never proved it holds the certificate's key.
func (p *Presented) Record(cs tls.ConnectionState) {
	if p == nil || !cs.HandshakeComplete || len(cs.PeerCertificates) == 0 {
		return
	}
	leaf := cs.PeerCertificates[0]
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, seen := range p.certs {
		if bytes.Equal(seen.Raw, leaf.Raw) {
			return
		}
	}
	if len(p.certs) == maxPresented {
		p.more = true
		return
	}
	p.certs = append(p.certs, leaf)
}

// Certificates returns each distinct certificate seen, in the order first
// seen, and whether more were seen than it keeps.
func (p *Presented) Certificates() ([]*x509.Certificate, bool) {
	if p == nil {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*x509.Certificate(nil), p.certs...), p.more
}

// Transport returns next recording, on p, the TLS state of every response
// it returns: a response came over a connection whose handshake completed.
// A nil p returns next itself.
func (p *Presented) Transport(next http.RoundTripper) http.RoundTripper {
	if p == nil {
		return next
	}
	return recordingTransport{next: next, seen: p}
}

// recordingTransport is an http.RoundTripper recording each response's TLS
// state on seen.
type recordingTransport struct {
	next http.RoundTripper
	seen *Presented
}

// RoundTrip implements http.RoundTripper.
func (t recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(req)
	if resp != nil && resp.TLS != nil {
		t.seen.Record(*resp.TLS)
	}
	return resp, err
}

// CloseIdleConnections closes next's idle connections, as http.Client's
// CloseIdleConnections asks of its transport.
func (t recordingTransport) CloseIdleConnections() {
	if c, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
