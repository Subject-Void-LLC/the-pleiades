// Package devicetls: a device refusing this side's client certificate, or
// its absence, said as a refusal that names the setting to change. A server
// that requires a client certificate answers with a TLS alert: in TLS 1.2
// inside the handshake, and in TLS 1.3 after the client's side of it has
// finished, on the first read, so the client meets it as a read error on a
// connection it took for up. Either way the server read no request, so no
// password or token reached it, and asking again gets the same alert.
package devicetls

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
)

// The TLS alerts (RFC 8446 section 6.2) a server sends about the client's
// certificate. Each is matched only when it came from the device (a remote
// alert): Go wraps an alert this side sends in its own handshake error too,
// and an alert this side sent about the device's certificate is the device's
// certificate refused, which Explain says.
var clientCertificateAlerts = []tls.AlertError{
	42,  // bad_certificate: a certificate the server refused
	43,  // unsupported_certificate
	44,  // certificate_revoked
	45,  // certificate_expired
	46,  // certificate_unknown
	48,  // unknown_ca: the certificate's issuer is not one the device trusts
	49,  // access_denied: the certificate is valid and the device denies it
	116, // certificate_required: TLS 1.3's answer to no certificate at all
}

// alertHandshakeFailure is handshake_failure (40), which a Go server sends in
// TLS 1.2 for a client that presented no certificate it requires (measured:
// "tls: client didn't provide a certificate"). It also says a cipher suite or
// a version was not agreed, so it is read as this side's certificate refused
// only after the server asked for one (Watch).
const alertHandshakeFailure tls.AlertError = 40

// remoteAlert is the operation Go's crypto/tls names an alert the peer sent.
const remoteAlert = "remote error"

// ClientCertificateRefusedError is a device refusing the connection over
// this side's client certificate: none presented to a server that requires
// one, or one it does not accept. The server read no request, so no password
// or token reached it.
type ClientCertificateRefusedError struct {
	// Alert is the TLS alert the device sent, as Go names it
	// ("tls: certificate required").
	Alert string
	// Presented is true when this side presented a client certificate, so
	// the device refused that one.
	Presented bool
	// Withheld is true when the device's credential holds a client
	// certificate and none was presented, because the device asked for one
	// from authorities the stored one is not from.
	Withheld bool
	// Cause is the error the alert came back as.
	Cause error
}

// Error says what the device refused and what to change.
func (e *ClientCertificateRefusedError) Error() string {
	const store = "the device's credential (add-credential --certificate and --key, or --pfx)"
	switch {
	case e.Presented:
		return fmt.Sprintf("the device refused the client certificate this side presented (%s), so it read no request and no password or token reached it: "+
			"store a certificate and key its server trusts in %s, which %s true presents", e.Alert, store, ClientCertificateProperty)
	case e.Withheld:
		return fmt.Sprintf("the device asked for a client certificate from authorities the stored one is not from, so none was presented and it refused the connection (%s); "+
			"it read no request and no password or token reached it: store a certificate one of its authorities signed in %s (%s true presents it)", e.Alert, store, ClientCertificateProperty)
	}
	return fmt.Sprintf("the device requires a client certificate and none was presented (%s), so it read no request and no password or token reached it: "+
		"store the certificate and key in %s and set the device's %s property to true", e.Alert, store, ClientCertificateProperty)
}

// Unwrap returns the error the alert came back as.
func (e *ClientCertificateRefusedError) Unwrap() error { return e.Cause }

// Refused reports that asking again would be refused the same way: the
// same certificate, or the same lack of one, meets the same alert. The
// engine's Refused stops a task's until and its loop's next item, and a
// wait stops, as for a device certificate this side does not trust.
func (e *ClientCertificateRefusedError) Refused() bool { return true }

// Watch records, for the connections a TLS configuration makes, whether the
// server asked for a client certificate and whether one was presented. Go's
// client presents a stored certificate only when the server's request
// accepts its issuer, so the configuration alone does not say what was sent.
// It is safe for concurrent use.
type Watch struct {
	mu sync.Mutex
	// asked is true once a server asked for a client certificate.
	asked bool
	// presented is true when the last certificate asked for was presented.
	presented bool
	// withheld is true when the last one asked for was not presented though
	// the configuration holds a certificate.
	withheld bool
}

// Watched returns a copy of cfg whose connections record on the Watch it
// returns whether the server asked for a client certificate and whether one
// was presented. The certificate chosen is the one Go would choose: cfg's own
// GetClientCertificate, or else the first of cfg.Certificates the request
// accepts, or none. A nil cfg is returned as nil, with a Watch that records
// nothing.
func Watched(cfg *tls.Config) (*tls.Config, *Watch) {
	w := &Watch{}
	if cfg == nil {
		return nil, w
	}
	out := cfg.Clone()
	own, certs := cfg.GetClientCertificate, cfg.Certificates
	out.GetClientCertificate = func(cri *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		chosen, err := pick(cri, own, certs)
		w.record(chosen != nil && len(chosen.Certificate) > 0, own != nil || len(certs) > 0)
		return chosen, err
	}
	return out, w
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

// record notes one request for a client certificate.
func (w *Watch) record(presented, held bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked, w.presented, w.withheld = true, presented, held && !presented
}

// Asked reports whether a server asked for a client certificate.
func (w *Watch) Asked() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.asked
}

// Explain is the package Explain for a connection made with the watched
// configuration: a device certificate this side refused is an
// *UntrustedError, a device's alert about this side's client certificate a
// *ClientCertificateRefusedError (handshake_failure only after the device
// asked for one), and any other error is returned as it is.
func (w *Watch) Explain(err error) error {
	// The device's certificate is checked first: an alert this side sent
	// over it is never read as the device's alert about this side's.
	explained := Explain(err)
	var untrusted *UntrustedError
	if errors.As(explained, &untrusted) {
		return explained
	}
	if refusal, ok := w.Refusal(err); ok {
		return refusal
	}
	return err
}

// Refusal returns err as a *ClientCertificateRefusedError when it is a TLS
// alert the device sent about this side's client certificate, and false
// otherwise.
func (w *Watch) Refusal(err error) (*ClientCertificateRefusedError, bool) {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != remoteAlert || op.Err == nil {
		return nil, false
	}
	w.mu.Lock()
	asked, presented, withheld := w.asked, w.presented, w.withheld
	w.mu.Unlock()
	// Go's remote alert is an unexported type whose text is the same as
	// tls.AlertError's for the same code, so the two are compared by text.
	text := op.Err.Error()
	matched := asked && text == alertHandshakeFailure.Error()
	for _, alert := range clientCertificateAlerts {
		matched = matched || text == alert.Error()
	}
	if !matched {
		return nil, false
	}
	return &ClientCertificateRefusedError{Alert: text, Presented: presented, Withheld: withheld, Cause: err}, true
}
