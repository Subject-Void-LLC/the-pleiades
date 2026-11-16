// The gRPC probe's TLS handshakes, kept so a certificate this side refused
// is said as one. gRPC reports a failed handshake only as Unavailable, its
// cause flattened into text, which would hide the certificate's fingerprints
// from the person who must check them and keep onboarding from recording it
// on first use (WithFirstUsePins). pkg/gnmi keeps its client's the same way.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"google.golang.org/grpc/credentials"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// handshakeRecord holds the last client handshake's failure on a probe's
// connections, nil once one succeeds. gRPC dials one connection at a time
// for a client, so what it holds is the latest handshake's.
type handshakeRecord struct {
	mu   sync.Mutex
	last error
}

// keptHandshakes is TLS transport credentials that record each client
// handshake's outcome on record. A clone shares the record, so a failure on
// any of the client's connections is seen.
type keptHandshakes struct {
	credentials.TransportCredentials
	record *handshakeRecord
}

// ClientHandshake implements credentials.TransportCredentials, recording
// the handshake's outcome.
func (k keptHandshakes) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, info, err := k.TransportCredentials.ClientHandshake(ctx, authority, raw)
	k.record.mu.Lock()
	k.record.last = err
	k.record.mu.Unlock()
	return conn, info, err
}

// Clone implements credentials.TransportCredentials, sharing the record.
func (k keptHandshakes) Clone() credentials.TransportCredentials {
	return keptHandshakes{TransportCredentials: k.TransportCredentials.Clone(), record: k.record}
}

// refusedCertificate returns the last handshake's failure as the device's
// certificate refused (a *devicetls.UntrustedError, naming its
// fingerprints), when that is what it was; nil otherwise.
func (r *handshakeRecord) refusedCertificate() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return nil
	}
	explained := devicetls.Explain(r.last)
	var untrusted *devicetls.UntrustedError
	if !errors.As(explained, &untrusted) {
		return nil
	}
	return fmt.Errorf("the TLS handshake failed, so nothing was sent: %w", explained)
}
