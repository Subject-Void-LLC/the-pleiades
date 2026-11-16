// Package devicetls: the certificate a device presented, seen on each
// verified handshake of the clients a caller builds under one context, so
// onboarding and a facts gatherer record it from the connection they already
// make, with no connection of their own and no change to any client.
//
// It travels on a context as the key log does (WithPresented), and only a
// configuration built with Settings.ConfigContext under that context records
// to it. Only a handshake that passed verification is recorded: Go calls
// VerifyConnection after its own checks succeed, and a pin's check runs
// first, so a certificate this side refused is never recorded as the
// device's.
package devicetls

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"sync"
)

// maxPresented bounds how many distinct certificates one Presented keeps,
// so a server that presents a new one on every connection cannot grow it.
const maxPresented = 8

// Presented holds the distinct certificates the devices presented on the
// verified handshakes of the clients built under one context. It is safe for
// concurrent use.
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
// which records the certificate of every verified handshake a configuration
// built with ConfigContext under the returned context makes.
func WithPresented(ctx context.Context) (context.Context, *Presented) {
	p := &Presented{}
	return context.WithValue(ctx, presentedKey{}, p), p
}

// presentedFrom returns the Presented ctx carries, nil when it carries none.
func presentedFrom(ctx context.Context) *Presented {
	p, _ := ctx.Value(presentedKey{}).(*Presented)
	return p
}

// observe records leaf unless it was seen already.
func (p *Presented) observe(leaf *x509.Certificate) {
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
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*x509.Certificate(nil), p.certs...), p.more
}

// watching returns verify followed by recording the handshake's leaf on p,
// for a handshake verify passed (or, with no verify, one Go's own checks
// passed, since Go calls VerifyConnection only after them).
func (p *Presented) watching(verify func(tls.ConnectionState) error) func(tls.ConnectionState) error {
	return func(cs tls.ConnectionState) error {
		if verify != nil {
			if err := verify(cs); err != nil {
				return err
			}
		}
		if len(cs.PeerCertificates) > 0 {
			p.observe(cs.PeerCertificates[0])
		}
		return nil
	}
}
