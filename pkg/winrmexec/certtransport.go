// The WinRM transport that presents a client certificate.
//
// # Why this exists rather than the library's own ClientAuthRequest
//
// masterzen/winrm ships ClientAuthRequest, which does the same job, and
// this package used it first. It cannot be used, for one measured reason
// and two that follow from it.
//
// GO CANNOT DO TLS 1.3 CLIENT AUTHENTICATION AGAINST WINDOWS. A WinRM
// request carrying a client certificate over TLS 1.3 is answered with a
// bare 503 and an empty body: no content type, no SOAP fault, nothing to
// read. The identical request over TLS 1.2 is answered 200.
//
// The cause is on THIS side, and saying so precisely matters because the
// first version of this comment blamed Windows and was wrong. Windows is
// fine: the same request, over TLS 1.3, with the same certificate, from
// curl, is answered 200. Measured both ways on 2026-09-20 against Windows
// 11 build 26200.
//
// TLS 1.3 removed renegotiation and replaced it with post-handshake
// authentication (RFC 8446 section 4.6.2), which is how http.sys asks for a
// client certificate once it knows which URL was requested: IIS decides per
// virtual directory, so it waits to see the request before asking. Go's
// crypto/tls does not implement PHA: Conn.handlePostHandshakeMessage
// accepts a session ticket and a key update and answers anything else, a
// CertificateRequest included, with an unexpected_message alert. So the
// server asks, Go refuses, the server sees no certificate and returns 503.
// Over TLS 1.2 the same exchange happens by renegotiation, which Go does
// support and which the Renegotiation field below enables.
//
// # This cap is permanent, not a TODO
//
// Go's omission is deliberate rather than unfinished, and that matters for
// anyone maintaining this file. RFC 8740 forbids PHA with HTTP/2 outright,
// because it deadlocks multiplexed streams; the Go team additionally treats
// PHA as a legacy pattern that adds brittle state to the TLS machine, on
// the view that client authentication belongs in the initial handshake.
// They have said they are unlikely to implement it. OpenSSL made the other
// call, which is the entire difference between curl and Go here.
//
// So do not write this up as waiting on an upstream fix. Capping at TLS 1.2
// is the supported way to do WinRM client-certificate authentication from
// Go, and it is expected to stay that way. If it ever does become
// unnecessary, TestTransportCapsTLSAtTwelve is the test that documents the
// assumption and the WinRM Release Gate is what would prove the removal.
//
// ClientAuthRequest builds its tls.Config inside Transport and keeps it on
// an unexported field, so there is no seam to cap the version through. That
// is the whole reason for this file: the fix is one field, and the only way
// to reach it is to own the transport.
//
// Two things follow. The errors here carry the status and a bounded snippet
// of the body, because the library's "invalid content type" discards both
// and that is precisely what hid the defect above. And the tls.Config is
// ours, so CACert is honored exactly and InsecureSkipVerify is never set on
// this path at all: Transport refuses Options.Insecure rather than honoring
// it, for the reason written beside that refusal.
package winrmexec

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// maxCertificateTLSVersion caps the certificate path at TLS 1.2.
//
// See this file's own doc comment. The short version: Go's crypto/tls does
// not implement TLS 1.3 post-handshake client authentication, which is how
// Windows asks for the certificate, so over TLS 1.3 the certificate is
// never sent and the request is refused. This is a limitation of this
// client, not of the target, and it is a deliberate one that upstream is
// unlikely to lift.
//
// The cap is deliberately NOT applied to the password paths, which are
// untouched by this and negotiate whatever both ends agree on.
const maxCertificateTLSVersion = tls.VersionTLS12

// maxErrorBodyBytes bounds how much of a non-SOAP response an error quotes.
//
// Enough to carry an HTML error page's title or a SOAP fault's reason, and
// little enough that a server answering with a megabyte of nonsense cannot
// put a megabyte into a job record.
const maxErrorBodyBytes = 512

// certificateTransport is a winrm.Transporter that authenticates with a
// client certificate.
type certificateTransport struct {
	client *http.Client
	url    string
}

// Transport builds the HTTP client for one endpoint. It is called by
// winrm.NewClientWithParameters immediately after the decorator returns.
func (t *certificateTransport) Transport(endpoint *winrm.Endpoint) error {
	pair, err := tls.X509KeyPair(endpoint.Cert, endpoint.Key)
	if err != nil {
		return fmt.Errorf("winrm: the client certificate and private key are not a usable pair: %w", err)
	}

	// Server verification is NOT skippable here, and Options.Insecure is
	// refused rather than ignored.
	//
	// The library's transport honors it, and this one deliberately does
	// not. Presenting a client certificate to a server you have not
	// verified hands that certificate to whoever answered, which is the one
	// thing certificate authentication exists to prevent; an insecure
	// password session risks a password, an insecure certificate session
	// risks the identity itself. internal/credtype/lookup/hashivault makes
	// the same argument for the same reason, and proves it with a negative
	// control rather than with the absence of a flag.
	//
	// Refused rather than silently ignored because this phase has already
	// been bitten twice by a value that was accepted, carried and dropped
	// (FAILURE_PATTERNS 274, and the passphrase on the loose certificate
	// path). A caller who asked for something they will not get should be
	// told so. CACert is the supported way to reach a server with a private
	// authority.
	if endpoint.Insecure {
		return fmt.Errorf("winrm: certificate authentication will not skip server verification: presenting a " +
			"client certificate to an unverified server hands it to whoever answers, so supply the server's " +
			"certificate authority instead")
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   maxCertificateTLSVersion,
		// The library sets this and it is kept deliberately: Windows may
		// renegotiate to ask for the certificate, and refusing outright
		// fails the connection rather than the request.
		Renegotiation: tls.RenegotiateOnceAsClient,
	}
	if len(endpoint.CACert) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(endpoint.CACert) {
			return fmt.Errorf("winrm: the CA bundle holds no certificate this platform could read")
		}
		config.RootCAs = pool
	}

	t.client = &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: config,
	}}
	t.url = endpointURL(endpoint)
	return nil
}

// Post sends one SOAP message and returns the response body.
func (t *certificateTransport) Post(_ *winrm.Client, message *soap.SoapMessage) (string, error) {
	request, err := http.NewRequest(http.MethodPost, t.url, strings.NewReader(message.String()))
	if err != nil {
		return "", fmt.Errorf("winrm: building the request for %s: %w", t.url, err)
	}
	request.Header.Set("Content-Type", "application/soap+xml;charset=UTF-8")
	// The WS-Man mutual authentication profile. It carries no credential of
	// its own: the certificate presented during the handshake is the
	// credential, and the target maps it to an account.
	request.Header.Set("Authorization",
		"http://schemas.dmtf.org/wbem/wsman/1/wsman/secprofile/https/mutual")

	response, err := t.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("winrm: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	body, readErr := io.ReadAll(response.Body)
	if readErr != nil {
		return "", fmt.Errorf("winrm: reading the response from %s: %w", t.url, readErr)
	}

	if !strings.Contains(response.Header.Get("Content-Type"), "application/soap+xml") {
		return "", fmt.Errorf("winrm: %s answered %d with %s rather than SOAP%s",
			t.url, response.StatusCode, describeContentType(response.Header.Get("Content-Type")),
			describeBody(body, response.StatusCode))
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("winrm: %s answered %d: %s", t.url, response.StatusCode, snippet(body))
	}
	return string(body), nil
}

// describeContentType names an absent content type rather than printing an
// empty pair of quotes, since absent is what a bare http.sys refusal looks
// like and it is a meaningful clue rather than a missing value.
func describeContentType(value string) string {
	if value == "" {
		return "no content type at all"
	}
	return strconv.Quote(value)
}

// describeBody adds the hint this package exists because of.
//
// An empty 503 with no content type is the exact signature of a client
// certificate that never reached the server, and that is worth saying where
// it is seen. Naming a specific likely cause beside a generic failure is
// the difference between an afternoon and a minute, and this one cost the
// afternoon.
func describeBody(body []byte, status int) string {
	if len(body) == 0 {
		if status == http.StatusServiceUnavailable {
			return ". An empty 503 here means the server saw no client certificate. Check that the certificate is" +
				" mapped to an account on the target and that its issuing authority is trusted there. If this" +
				" transport has been changed to allow TLS 1.3, that is the more likely cause: Go does not" +
				" implement post-handshake client authentication, so the certificate is never sent"
		}
		return " and an empty body"
	}
	return ": " + snippet(body)
}

// snippet renders a bounded, single-line extract of a response body.
func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > maxErrorBodyBytes {
		text = text[:maxErrorBodyBytes] + "..."
	}
	return strings.Join(strings.Fields(text), " ")
}

// endpointURL builds the WS-Man URL for an endpoint.
//
// It is rebuilt here rather than taken from the endpoint, whose own url
// method is unexported. net.JoinHostPort brackets an IPv6 literal, which is
// the case Target.Host's own documentation records as having produced an
// unparsable URL once already.
func endpointURL(endpoint *winrm.Endpoint) string {
	scheme := "http"
	if endpoint.HTTPS {
		scheme = "https"
	}
	u := url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.Port)),
		Path:   "/wsman",
	}
	return u.String()
}
