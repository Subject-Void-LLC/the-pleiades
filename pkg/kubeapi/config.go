// Package kubeapi is the one place this platform speaks to a Kubernetes API
// server: client-go behind a port (Part XVIII D5), with the client's
// configuration built here, from a device's record and its bound
// credential, and from nothing else.
//
// Nothing ambient ever reaches a cluster. No kubeconfig is loaded, no
// KUBECONFIG or home directory is read, and an in-cluster Runner's own
// ServiceAccount is never used: a runbook author would otherwise act with
// whatever the Runner's pod may do (D5's edge case 6). A kubeconfig is
// imported once, as a credential (Import), keeping four values and
// refusing anything that would run a program. internal/archtest holds both
// rules, and confines k8s.io imports to this package.
package kubeapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"k8s.io/client-go/rest"
)

// Endpoint is how a cluster is reached, from its device record.
type Endpoint struct {
	// Server is the API server's https URL, with a path when the server
	// sits behind a proxy that serves several clusters.
	Server string
	// CAPEM pins the cluster's certificate authority; empty trusts the
	// system's roots.
	CAPEM []byte
	// ServerName is the name the server's certificate is checked against,
	// when it differs from Server's host (a bastion's local port, or an
	// address the certificate does not name).
	ServerName string
	// Dial reaches Server's address, for a cluster behind a bastion; nil
	// dials directly.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Identity is who a session authenticates as, from the bound credential.
// Exactly one form is set.
type Identity struct {
	// Token is a static bearer token.
	Token string
	// Mint returns a bearer token and when it expires, for a credential
	// whose tokens live minutes (EKS). A session asks again before the
	// token it holds expires.
	Mint func(ctx context.Context) (string, time.Time, error)
	// ClientCertPEM and ClientKeyPEM are an x509 client identity.
	ClientCertPEM, ClientKeyPEM []byte
}

// Limits bound how fast one session asks: client-go's own client-side
// limiter. The server's API Priority and Fairness bounds every client
// together, and answers 429 with Retry-After when it must.
type Limits struct {
	QPS   float32
	Burst int
}

// DefaultLimits is what a session gets unless a caller says otherwise:
// four times client-go's own defaults (5 and 10), which were set for
// controllers running all the time, not for a task that reads and writes a
// handful of objects and ends.
var DefaultLimits = Limits{QPS: 20, Burst: 40}

// mintMargin is how long before a minted token expires a session mints the
// next one, so a request is never sent with a token about to lapse.
const mintMargin = time.Minute

// userAgent names this platform to the API server's audit log.
const userAgent = "pleiades"

// Config builds a client configuration from ep and id alone.
func Config(ep Endpoint, id Identity, limits Limits) (*rest.Config, error) {
	if err := checkServer(ep.Server); err != nil {
		return nil, err
	}
	if err := checkIdentity(id); err != nil {
		return nil, err
	}
	if limits.QPS <= 0 || limits.Burst <= 0 {
		limits = DefaultLimits
	}
	cfg := &rest.Config{
		Host: ep.Server,
		TLSClientConfig: rest.TLSClientConfig{
			CAData:     ep.CAPEM,
			ServerName: ep.ServerName,
			CertData:   id.ClientCertPEM,
			KeyData:    id.ClientKeyPEM,
		},
		BearerToken: id.Token,
		QPS:         limits.QPS,
		Burst:       limits.Burst,
		Dial:        ep.Dial,
		UserAgent:   userAgent,
		// A deprecation warning is the server's answer to a request, which
		// a method reports in its result; client-go would print it.
		WarningHandler: rest.NoWarnings{},
	}
	if id.Mint != nil {
		// One cache for every client the configuration builds: client-go
		// wraps a separate transport for each, and a token minted for one
		// serves them all until it nears expiry.
		cache := &tokenCache{mint: id.Mint}
		cfg.WrapTransport = func(next http.RoundTripper) http.RoundTripper {
			return &mintingTransport{next: next, cache: cache}
		}
	}
	return cfg, nil
}

// checkServer refuses a server address that is not an https URL with a
// host, or that carries anything a client could mistake for a credential.
func checkServer(server string) error {
	u, err := url.Parse(server)
	if err != nil {
		return fmt.Errorf("kubeapi: the API server address %q does not parse: %w", server, err)
	}
	switch {
	case u.Scheme != "https":
		return fmt.Errorf("kubeapi: the API server address %q is not https; a cluster is reached over TLS only", server)
	case u.Host == "":
		return fmt.Errorf("kubeapi: the API server address %q names no host", server)
	case u.User != nil:
		return fmt.Errorf("kubeapi: the API server address carries a user name or password; a cluster's credential is bound, never written into its address")
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("kubeapi: the API server address %q carries a query or fragment", server)
	}
	return nil
}

// checkIdentity refuses an identity with no form, or with more than one,
// since which one would win is client-go's choice, not this platform's.
func checkIdentity(id Identity) error {
	forms := 0
	if id.Token != "" {
		forms++
	}
	if id.Mint != nil {
		forms++
	}
	if len(id.ClientCertPEM) > 0 || len(id.ClientKeyPEM) > 0 {
		if len(id.ClientCertPEM) == 0 || len(id.ClientKeyPEM) == 0 {
			return errors.New("kubeapi: a client certificate needs both the certificate and its key")
		}
		forms++
	}
	switch forms {
	case 0:
		return errors.New("kubeapi: no credential: a cluster is reached with the credential bound to it, never an ambient one")
	case 1:
		return nil
	default:
		return errors.New("kubeapi: the credential holds more than one identity (a token and a client certificate); a cluster credential carries one")
	}
}

// mintingTransport sets each request's bearer token from a session's
// token cache.
type mintingTransport struct {
	next  http.RoundTripper
	cache *tokenCache
}

// tokenCache holds the token a minting credential last returned, shared by
// every client of one configuration.
type tokenCache struct {
	mint func(ctx context.Context) (string, time.Time, error)

	mu      sync.Mutex
	token   string
	expires time.Time
}

// RoundTrip sends req with a current token, on a copy, never altering the
// caller's request.
func (m *mintingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := m.cache.current(req.Context())
	if err != nil {
		return nil, err
	}
	out := req.Clone(req.Context())
	out.Header.Set("Authorization", "Bearer "+token)
	return m.next.RoundTrip(out)
}

// current returns the token in hand, or mints one when there is none or it
// expires within mintMargin.
func (m *tokenCache) current(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && time.Until(m.expires) > mintMargin {
		return m.token, nil
	}
	token, expires, err := m.mint(ctx)
	if err != nil {
		return "", fmt.Errorf("kubeapi: minting a token: %w", err)
	}
	if token == "" {
		return "", errors.New("kubeapi: minting a token returned none")
	}
	m.token, m.expires = token, expires
	return token, nil
}
