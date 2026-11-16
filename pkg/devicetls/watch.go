// Package devicetls: watching a device's TLS configuration for its server's
// request for a client certificate, and diagnosing a failed connection on a
// fresh one when the server's alert may have been lost.
//
// A TLS 1.3 server that refuses this side's certificate sends its alert and
// closes. The client has usually written a request by then (gRPC its preface
// at once, net/http its request), and a write that reaches a closed
// connection draws a reset. A host that discards queued data on a reset
// (Windows does; macOS often) then reports the reset rather than the alert.
// So a failure on a connection whose server asked for a client certificate is
// diagnosed on a connection of its own, which writes nothing after its
// handshake: the server's alert, when it sends one, is then the first thing
// read, and no reset can come before it. That connection carries no request
// and no credential beyond the client certificate the handshake presents.
package devicetls

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"sync"
	"time"
)

// diagnoseTimeout bounds a diagnosis from its dial to its one read. A server
// that accepted the certificate and sends nothing first (an HTTP/1.1 server
// waits for the request; RFC 7540 makes SETTINGS an HTTP/2 server's first
// frame, but not that it be sent before the client's preface) ends the read
// at this bound, or sooner at the caller's deadline or cancellation, and is
// read as no refusal. It is a variable only so a test can shorten it.
var diagnoseTimeout = 2 * time.Second

// Watch records whether a device's server asked for a client certificate,
// and diagnoses a failed connection to it. Go's client presents a stored
// certificate only when the server's request accepts its issuer, so the
// configuration alone does not say what was sent. It is safe for concurrent
// use.
type Watch struct {
	// base is the configuration as given, which a diagnosis dials with.
	base *tls.Config
	// addr is the device's host:port, which a diagnosis dials.
	addr string
	mu   sync.Mutex
	// asked is true once a server asked this configuration's connections
	// for a client certificate. It only decides whether a failure is worth a
	// diagnosis, whose own connection's record decides the verdict.
	asked bool
}

// Watched returns a copy of cfg whose connections record on the returned
// Watch whether the server asked for a client certificate, and the Watch,
// which diagnoses a failed connection to addr (host:port) on one of its own.
// nextProtos is the ALPN the real connections offer when cfg does not say
// (gRPC's h2, net/http's h2 and http/1.1), so a diagnosis is not refused for
// a protocol the real connection would have agreed. The certificate chosen is
// the one Go would choose: cfg's own GetClientCertificate, or else the first
// of cfg.Certificates the request accepts, or none. A nil cfg is returned as
// nil, with a Watch that diagnoses nothing.
func Watched(cfg *tls.Config, addr string, nextProtos ...string) (*tls.Config, *Watch) {
	w := &Watch{addr: addr}
	if cfg == nil {
		return nil, w
	}
	w.base = cfg.Clone()
	if len(w.base.NextProtos) == 0 {
		w.base.NextProtos = nextProtos
	}
	out := cfg.Clone()
	out.GetClientCertificate = choose(cfg, func(connRecord) {
		w.mu.Lock()
		w.asked = true
		w.mu.Unlock()
	})
	return out, w
}

// choose is the GetClientCertificate that answers as Go would for cfg and
// hands seen what it presented.
func choose(cfg *tls.Config, seen func(connRecord)) func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	own, certs := cfg.GetClientCertificate, cfg.Certificates
	return func(cri *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		chosen, err := pick(cri, own, certs)
		presented := chosen != nil && len(chosen.Certificate) > 0
		seen(connRecord{asked: true, presented: presented, withheld: !presented && (own != nil || len(certs) > 0)})
		return chosen, err
	}
}

// pick is the certificate Go's client answers cri with: own's answer when
// set, else the first of certs whose issuer and key cri accepts, else an
// empty certificate, which presents none.
func pick(cri *tls.CertificateRequestInfo, own func(*tls.CertificateRequestInfo) (*tls.Certificate, error), certs []tls.Certificate) (*tls.Certificate, error) {
	if own != nil {
		return own(cri)
	}
	for i := range certs {
		if cri.SupportsCertificate(&certs[i]) == nil {
			return &certs[i], nil
		}
	}
	return new(tls.Certificate), nil
}

// HTTPSAddress is the host:port an https URL's connection dials, which a
// Watch for it diagnoses: its port, or 443.
func HTTPSAddress(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// HTTPNextProtos is the ALPN net/http offers, for a Watch on a
// configuration that names none.
var HTTPNextProtos = []string{"h2", "http/1.1"}

// Asked reports whether a server asked this configuration's connections for
// a client certificate.
func (w *Watch) Asked() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.asked
}

// Explain is the package Explain for a connection made with the watched
// configuration: a device certificate this side refused is an
// *UntrustedError, a failure on a connection whose server asked for a client
// certificate is diagnosed (Diagnose) and comes back as the
// *ClientCertificateRefusedError the diagnosis finds, and any other error is
// returned as it is.
func (w *Watch) Explain(ctx context.Context, err error) error {
	// The device's certificate is checked first: an alert this side sent
	// over it is never read as the device's alert about this side's.
	explained := Explain(err)
	var untrusted *UntrustedError
	if errors.As(explained, &untrusted) || !w.Asked() || !worthDiagnosing(ctx, err) {
		return explained
	}
	if refusal, ok := w.Diagnose(ctx, err); ok {
		return refusal
	}
	return err
}

// worthDiagnosing reports whether err, a connection's failure, could be a
// refused client certificate whose alert was lost: anything but a timeout or
// the caller's own cancellation, after which there is no time to look. The
// shapes are many (a reset, a broken pipe, the connection's end, and
// net/http's HTTP/2 "client conn could not be established", which keeps no
// cause), so none is required.
func worthDiagnosing(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var timeout net.Error
	return !errors.As(err, &timeout) || !timeout.Timeout()
}

// Diagnose opens one connection to the device with the watched
// configuration, writes nothing after its handshake, and reads once, within
// diagnoseTimeout or ctx, whichever ends first: the handshake's failure, or a
// TLS 1.3 server's alert after it, read as this side's client certificate
// refused on a connection whose server asked for one, is returned as a
// *ClientCertificateRefusedError whose cause is cause. A server that answers,
// stays silent, or cannot be reached is no refusal.
func (w *Watch) Diagnose(ctx context.Context, cause error) (*ClientCertificateRefusedError, bool) {
	if w.base == nil || w.addr == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, diagnoseTimeout)
	defer cancel()
	var rec connRecord
	cfg := w.base.Clone()
	cfg.GetClientCertificate = choose(w.base, func(r connRecord) { rec = r })
	if cfg.ServerName == "" {
		// As net/http and gRPC name the server: the host being dialed.
		if host, _, err := net.SplitHostPort(w.addr); err == nil {
			cfg.ServerName = host
		}
	}
	raw, err := (&net.Dialer{}).DialContext(ctx, "tcp", w.addr)
	if err != nil {
		return nil, false
	}
	defer func() { _ = raw.Close() }()
	// The read below honours the deadline alone, so the context's end is
	// made a deadline that has passed.
	stop := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Now()) })
	defer stop()
	conn := tls.Client(raw, cfg)
	if err := conn.HandshakeContext(ctx); err != nil {
		return rec.refusal(err, cause)
	}
	if conn.ConnectionState().Version != tls.VersionTLS13 || !rec.asked {
		// In TLS 1.2 a refusal ends the handshake itself, and a server that
		// asked for no certificate refused none.
		return nil, false
	}
	if _, err := conn.Read(make([]byte, 1)); err != nil {
		return rec.refusal(err, cause)
	}
	return nil, false
}
