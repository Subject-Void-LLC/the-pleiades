// sealedNTLMTransport: WinRM over HTTP with NTLM message encryption that
// never sends a message unsealed.
//
// The library's own winrm.Encryption runs an NTLM handshake on an empty
// request before each message and seals the message with the session it
// gets. When that handshake fails for any reason (a 401 from a handshake
// split across two connections, a connection the host closed under load, a
// 503), it discards the error and posts the message through plain NTLM,
// unsealed: the whole SOAP envelope, a script's text, its environment
// values and a stdin chunk cross the network in the clear, and Windows,
// which refuses unencrypted WinRM by default, answers "401 - invalid
// content type" after reading it. That was the intermittent 401 seen on
// long transfers (FAILURE_PATTERNS 532).
//
// This transport does the same handshake, through github.com/bodgit/ntlmssp's
// HTTP client, and seals each message with the session it makes, in the
// WS-MV framing Windows reads (frame, unseal). It has no unsealed path: a
// handshake that does not complete is an error naming why, with nothing of
// the message sent. Each transport has its own connection pool, held to one
// connection, so a handshake's three requests share one socket and another
// exchange's traffic cannot split them. A message the host refuses with 401
// was not processed, so it is sent again once after a fresh handshake.
package winrmexec

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bodgit/ntlmssp"
	ntlmhttp "github.com/bodgit/ntlmssp/http"
	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// soapContentType is a WS-Man message's content type.
const soapContentType = "application/soap+xml;charset=UTF-8"

// maxReply bounds a reply's body, unsealed or not.
const maxReply = 64 << 20

// errLoginRefused is a handshake the host answered with 401: it refused the
// account or its password.
var errLoginRefused = errors.New("the host refused the login (HTTP 401)")

// sealedNTLMTransport implements winrm.Transporter. The library gives a
// transporter no access to the client's address or credential, so they
// are held here, given by newExchange.
type sealedNTLMTransport struct {
	url, username, password string
	client                  *http.Client
}

// Transport builds the transport's own connection pool: one connection to
// the host, kept alive between the handshake and the message as NTLM needs,
// with endpoint.Timeout bounding each reply's headers as the library's own
// transports do.
func (t *sealedNTLMTransport) Transport(endpoint *winrm.Endpoint) error {
	t.client = &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxConnsPerHost:       1,
		MaxIdleConnsPerHost:   1,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: endpoint.Timeout,
	}}
	return nil
}

// Post sends one message sealed, after a fresh handshake, and returns the
// reply unsealed. A message refused with 401 is sent once more after
// another handshake; any other failure is returned as it is.
func (t *sealedNTLMTransport) Post(_ *winrm.Client, message *soap.SoapMessage) (string, error) {
	ctx := context.Background()
	if t.client == nil {
		return "", errors.New("winrm: the sealed NTLM transport was used before Transport built it")
	}
	reply, refused, err := t.post(ctx, message.String())
	if refused {
		reply, _, err = t.post(ctx, message.String())
	}
	return reply, err
}

// post runs one handshake and sends body sealed. refused reports a sealed
// message the host answered with 401, which it did not process.
func (t *sealedNTLMTransport) post(ctx context.Context, body string) (reply string, refused bool, err error) {
	user, domain := splitAccount(t.username)
	session, err := ntlmssp.NewClient(ntlmssp.SetUserInfo(user, t.password), ntlmssp.SetDomain(domain), ntlmssp.SetVersion(ntlmssp.DefaultVersion()))
	if err != nil {
		return "", false, fmt.Errorf("winrm: NTLM session: %w", err)
	}
	// The client's own sealing (its Encryption option) replaces a request's
	// body without its ContentLength, which net/http refuses to send, so
	// this transport seals the message itself, below, and the client only
	// authenticates.
	client, err := ntlmhttp.NewClient(t.client, session)
	if err != nil {
		return "", false, fmt.Errorf("winrm: NTLM client: %w", err)
	}

	// The handshake, on an empty request: the client sends a request's body
	// on its first, unauthenticated try, so the message goes only after the
	// session that seals it exists.
	shake, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, nil)
	if err != nil {
		return "", false, err
	}
	shake.Header.Set("Content-Type", soapContentType)
	shake.Header.Set("Content-Length", "0")
	resp, err := client.Do(shake)
	if err != nil {
		return "", false, fmt.Errorf("winrm: NTLM handshake with %s: %w; nothing of the message was sent", t.url, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxReply))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return "", false, fmt.Errorf("winrm: %w; nothing of the message was sent", errLoginRefused)
	case resp.StatusCode != http.StatusOK || !session.Complete() || session.SecuritySession() == nil:
		return "", false, fmt.Errorf("winrm: the NTLM handshake with %s ended with HTTP %d and no sealed session; nothing of the message was sent", t.url, resp.StatusCode)
	}

	// The message, sealed with the session the handshake made, in the
	// WS-MV form Windows reads: the signature's length, the signature and
	// the sealed message, in a multipart/encrypted body.
	sec := session.SecuritySession()
	sealed, signature, err := sec.Wrap([]byte(body))
	if err != nil {
		return "", false, fmt.Errorf("winrm: sealing the message: %w", err)
	}
	payload, err := frame(signature, sealed, len(body))
	if err != nil {
		return "", false, fmt.Errorf("winrm: framing the sealed message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", sealedContentType)
	resp, err = client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("winrm: sending to %s: %w", t.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if err != nil {
		return "", false, fmt.Errorf("winrm: reading the reply from %s: %w", t.url, err)
	}
	if len(data) > maxReply {
		return "", false, fmt.Errorf("winrm: the reply from %s is over %d bytes", t.url, maxReply)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", true, fmt.Errorf("winrm: %s refused the sealed message (HTTP 401) after the handshake", t.url)
	}
	replyType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(replyType, "multipart/encrypted") {
		if data, replyType, err = unseal(sec, data, replyType); err != nil {
			return "", false, fmt.Errorf("winrm: unsealing the reply from %s (HTTP %d): %w", t.url, resp.StatusCode, err)
		}
	}
	// A WS-Man fault comes back as a SOAP body with status 500, which the
	// library parses; anything that is not SOAP is not an answer.
	if !strings.Contains(replyType, "application/soap+xml") {
		return "", false, fmt.Errorf("winrm: %s answered HTTP %d with %q, not a WS-Man reply", t.url, resp.StatusCode, replyType)
	}
	return string(data), false, nil
}

// The WS-MV framing of a sealed message ([MS-WSMV] 2.2.9.1.1), as Windows
// writes and reads it: two MIME parts whose headers are indented with a
// tab, the first naming the protocol and the plain message's type and
// length, the second the signature's length, the signature and the sealed
// message, then the closing boundary.
const (
	sealBoundary      = "--Encrypted Boundary"
	sealProtocol      = "application/HTTP-SPNEGO-session-encrypted"
	sealedContentType = `multipart/encrypted;protocol="` + sealProtocol + `";boundary="Encrypted Boundary"`
	octetStreamHeader = "\tContent-Type: application/octet-stream\r\n"
)

// signatureLength is an NTLM message signature's length, and its WS-MV
// prefix, little-endian.
const signatureLength = 16

var signatureLengthPrefix = []byte{signatureLength, 0, 0, 0}

// frame is a sealed message in its WS-MV framing. An NTLM signature is
// always signatureLength bytes; another length is refused.
func frame(signature, sealed []byte, plainLength int) ([]byte, error) {
	if len(signature) != signatureLength {
		return nil, fmt.Errorf("an NTLM signature of %d bytes, not %d", len(signature), signatureLength)
	}
	var b bytes.Buffer
	b.WriteString(sealBoundary + "\r\n")
	b.WriteString("\tContent-Type: " + sealProtocol + "\r\n")
	fmt.Fprintf(&b, "\tOriginalContent: type=%s;Length=%d\r\n", soapContentType, plainLength)
	b.WriteString(sealBoundary + "\r\n")
	b.WriteString(octetStreamHeader)
	b.Write(signatureLengthPrefix)
	b.Write(signature)
	b.Write(sealed)
	b.WriteString(sealBoundary + "--\r\n")
	return b.Bytes(), nil
}

// unseal reads a sealed WS-MV reply: the length its first part declares,
// the signature and the sealed message in its second, unsealed and checked
// against the signature and that length.
func unseal(sec interface {
	Unwrap([]byte, []byte) ([]byte, error)
}, data []byte, _ string) ([]byte, string, error) {
	head, rest, ok := bytes.Cut(data, []byte(octetStreamHeader))
	if !ok {
		return nil, "", errors.New("the sealed reply has no application/octet-stream part")
	}
	_, lengthText, ok := bytes.Cut(head, []byte("Length="))
	if !ok {
		return nil, "", errors.New("the sealed reply does not say its message's length")
	}
	end := bytes.IndexAny(lengthText, "\r\n;")
	if end < 0 {
		end = len(lengthText)
	}
	want, err := strconv.Atoi(string(bytes.TrimSpace(lengthText[:end])))
	if err != nil {
		return nil, "", fmt.Errorf("the sealed reply's length %q is not a number", lengthText[:end])
	}
	framed, _, _ := bytes.Cut(rest, []byte(sealBoundary))
	if len(framed) < 4 {
		return nil, "", errors.New("the sealed reply is too short to hold a signature")
	}
	n := binary.LittleEndian.Uint32(framed[:4])
	if int64(n) > int64(len(framed))-4 {
		return nil, "", errors.New("the sealed reply's signature runs past its end")
	}
	plain, err := sec.Unwrap(framed[4+n:], framed[4:4+n])
	if err != nil {
		return nil, "", err
	}
	if len(plain) != want {
		return nil, "", fmt.Errorf("the sealed reply holds %d bytes where it declares %d", len(plain), want)
	}
	return plain, soapContentType, nil
}

// splitAccount splits DOMAIN\user or user@domain into the user and the
// domain, as the library does.
func splitAccount(account string) (user, domain string) {
	if u, d, ok := strings.Cut(account, "@"); ok {
		return u, d
	}
	if d, u, ok := strings.Cut(account, `\`); ok {
		return u, d
	}
	return account, ""
}
