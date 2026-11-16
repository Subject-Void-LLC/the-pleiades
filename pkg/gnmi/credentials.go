// Package gnmi: the credential a call carries, the address a client dials,
// and the TLS handshake's own failure kept so a refused certificate is said
// as one.
package gnmi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"google.golang.org/grpc/credentials"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ValidateTarget checks target is host:port with a port from 1 to 65535 and
// returns the host. A gRPC name-resolver scheme (dns:///, unix:) is refused:
// the target is an address, and a scheme would let an inventory value choose
// how, and where, the platform connects.
func ValidateTarget(target string) (string, error) {
	if target == "" {
		return "", errors.New("the gNMI target is empty: it must be host:port")
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		return "", errors.New("the gNMI target must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("the gNMI target has no valid port")
	}
	for _, r := range host {
		// A space, a control character or a slash has no place in a host
		// name or address, and a slash is how a resolver scheme starts.
		if r <= ' ' || r == '/' || r == 0x7f {
			return "", errors.New("the gNMI target has a host that is not a name or address")
		}
	}
	return host, nil
}

// credentialFor returns the per-call credential auth names, from secrets,
// or nil for AuthNone. A mode whose credential the secrets do not hold is
// refused rather than sent empty.
func credentialFor(auth string, secrets map[string]string, allowPlaintext bool) (credentials.PerRPCCredentials, error) {
	switch auth {
	case AuthNone:
		return nil, nil
	case AuthPassword:
		user, pass := secrets[wire.SecretUsername], secrets[wire.SecretPassword]
		if user == "" || pass == "" {
			return nil, errors.New("gnmi_auth is password and the device's credential holds no username and password: store one with add-credential, or set gnmi_auth to none")
		}
		return passwordCredential{metadata: map[string]string{"username": user, "password": pass}, plaintext: allowPlaintext}, nil
	case AuthBearer:
		token := secrets[wire.SecretBearerToken]
		if token == "" {
			return nil, errors.New("gnmi_auth is bearer and the device's credential holds no token: store one with add-credential --token")
		}
		return passwordCredential{metadata: map[string]string{"authorization": "Bearer " + token}, plaintext: allowPlaintext}, nil
	}
	return nil, fmt.Errorf("gnmi_auth %q is not one of %s, %s or %s", auth, AuthPassword, AuthBearer, AuthNone)
}

// passwordCredential is the metadata every call carries. It is a value with
// no String method of its own on purpose: it is never printed, and its
// fields are unexported so nothing reflects it into a log by accident.
type passwordCredential struct {
	metadata  map[string]string
	plaintext bool
}

// GetRequestMetadata implements credentials.PerRPCCredentials: the same
// metadata on every call, a copy so gRPC cannot change the client's own.
func (p passwordCredential) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	out := make(map[string]string, len(p.metadata))
	for k, v := range p.metadata {
		out[k] = v
	}
	return out, nil
}

// RequireTransportSecurity implements credentials.PerRPCCredentials: gRPC
// itself refuses to send the credential over a connection without TLS unless
// the record accepted that, a second check behind New's own.
func (p passwordCredential) RequireTransportSecurity() bool {
	return !p.plaintext
}

// handshakeSeen holds the last TLS handshake failure on a client's
// connections, which gRPC reports only as Unavailable with the error flattened
// into text.
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
// handshake's failure: pkg/devicetls.Explain turns Go's certificate refusal
// into an *UntrustedError naming the certificate, which a person can check
// and pin, and which stops a retry, since the same certificate is refused the
// same way however often it is asked.
type capturingCredentials struct {
	credentials.TransportCredentials
	seen *handshakeSeen
}

// ClientHandshake implements credentials.TransportCredentials, recording the
// handshake's outcome.
func (c *capturingCredentials) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := c.TransportCredentials.ClientHandshake(ctx, authority, raw)
	c.seen.set(err)
	return conn, info, err
}

// Clone implements credentials.TransportCredentials. The clone shares the
// record, so a failure on any of the client's connections is seen.
func (c *capturingCredentials) Clone() credentials.TransportCredentials {
	return &capturingCredentials{TransportCredentials: c.TransportCredentials.Clone(), seen: c.seen}
}

// handshakeFailure returns the client's last TLS handshake failure as
// pkg/devicetls explains it, or nil when there is none (or the client is
// plaintext).
func (c *Client) handshakeFailure() error {
	if c.handshake == nil {
		return nil
	}
	err := c.handshake.get()
	if err == nil {
		return nil
	}
	return fmt.Errorf("the TLS handshake with %s failed, so nothing was sent: %w", c.target, devicetls.Explain(err))
}
