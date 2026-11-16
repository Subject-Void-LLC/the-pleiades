// Package devicetls: a device refusing this side's client certificate, or
// its absence, said as a refusal that names the setting to change. A server
// that requires a client certificate answers with a TLS alert: in TLS 1.2
// inside the handshake, and in TLS 1.3 after the client's side of it has
// finished, so the client meets it on its first read, after it may already
// have written a request. That request reaches the server encrypted and is
// discarded unread: the server processes no request, and no login is
// attempted. Asking again gets the same alert.
package devicetls

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
)

// The TLS alerts (RFC 8446 section 6.2) a server sends about the client's
// certificate. Each is matched only when it came from the device (a remote
// alert) on a connection whose server asked for a client certificate: Go
// wraps an alert this side sends in its own handshake error too, and a server
// or a middlebox that never asked for a certificate (a proxy denying the
// address, say) is refusing something a certificate cannot change.
var clientCertificateAlerts = []tls.AlertError{
	40,  // handshake_failure: a Go server's TLS 1.2 answer to no certificate (measured)
	42,  // bad_certificate: a certificate the server refused
	43,  // unsupported_certificate
	44,  // certificate_revoked
	45,  // certificate_expired
	46,  // certificate_unknown
	48,  // unknown_ca: the certificate's issuer is not one the device trusts
	49,  // access_denied: the certificate is valid and the device denies it
	116, // certificate_required: TLS 1.3's answer to no certificate at all
}

// alertCertificateRequired is certificate_required, which names its own
// cause, so it is read as this side's certificate refused even on a
// connection whose request for one went unrecorded.
const alertCertificateRequired tls.AlertError = 116

// remoteAlert is the operation Go's crypto/tls names an alert the peer sent.
const remoteAlert = "remote error"

// ClientCertificateRefusedError is a device refusing the connection over
// this side's client certificate: none presented to a server that requires
// one, or one it does not accept. The server processed no request.
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
	// Cause is the error the refusal came back as.
	Cause error
}

// Error says what the device refused and what to change.
func (e *ClientCertificateRefusedError) Error() string {
	const store = "the device's credential (add-credential --certificate and --key, or --pfx)"
	const unread = "the device processed no request, so no login was attempted"
	switch {
	case e.Presented:
		return fmt.Sprintf("the device refused the client certificate this side presented (%s), and %s: "+
			"store a certificate and key its server trusts in %s, which %s true presents", e.Alert, unread, store, ClientCertificateProperty)
	case e.Withheld:
		return fmt.Sprintf("the device asked for a client certificate from authorities the stored one is not from, so none was presented and it refused the connection (%s), and %s: "+
			"store a certificate one of its authorities signed in %s (%s true presents it)", e.Alert, unread, store, ClientCertificateProperty)
	}
	return fmt.Sprintf("the device requires a client certificate and none was presented (%s), and %s: "+
		"store the certificate and key in %s and set the device's %s property to true", e.Alert, unread, store, ClientCertificateProperty)
}

// Unwrap returns the error the refusal came back as.
func (e *ClientCertificateRefusedError) Unwrap() error { return e.Cause }

// Refused reports that asking again would be refused the same way: the
// same certificate, or the same lack of one, meets the same alert. The
// engine's Refused stops a task's until and its loop's next item, and a
// wait stops, as for a device certificate this side does not trust.
func (e *ClientCertificateRefusedError) Refused() bool { return true }

// connRecord is what one connection's handshake said about the client
// certificate: whether the server asked for one, and what was presented.
type connRecord struct {
	asked, presented, withheld bool
}

// remoteAlertText returns the text of the TLS alert the peer sent that err
// carries, or false.
func remoteAlertText(err error) (string, bool) {
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != remoteAlert || op.Err == nil {
		return "", false
	}
	return op.Err.Error(), true
}

// refusal reads alertErr, a failure on a connection rec recorded, as this
// side's client certificate refused, with cause as the refusal's cause.
func (rec connRecord) refusal(alertErr, cause error) (*ClientCertificateRefusedError, bool) {
	text, ok := remoteAlertText(alertErr)
	if !ok {
		return nil, false
	}
	// Go's remote alert is an unexported type whose text is the same as
	// tls.AlertError's for the same code, so the two are compared by text.
	for _, alert := range clientCertificateAlerts {
		if text == alert.Error() && (rec.asked || alert == alertCertificateRequired) {
			return &ClientCertificateRefusedError{Alert: text, Presented: rec.presented, Withheld: rec.withheld, Cause: cause}, true
		}
	}
	return nil, false
}
