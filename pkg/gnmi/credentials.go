// Package gnmi: the credential a call carries and the address a client
// dials.
package gnmi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"google.golang.org/grpc/credentials"

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
