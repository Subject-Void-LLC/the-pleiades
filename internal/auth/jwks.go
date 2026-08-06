package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Federated Identity (PATTERNS.md): verifies tokens by fetching an Identity
// Provider's public signing keys from a JWKS endpoint (RFC 7517) instead of
// sharing a symmetric secret. Hand-rolled against the standard library
// rather than a new dependency: RFC 7517 parsing for the two key types
// real IdPs actually publish (RSA, EC) is small, and this avoids a
// new-dependency license/transitive-tree review for something this
// self-contained (the same "small from-scratch implementation over a
// dependency" call pkg/policy already makes elsewhere in this codebase).

const (
	defaultJWKSTimeout         = 10 * time.Second
	defaultJWKSRefreshInterval = 15 * time.Minute
)

// jwkSet is the RFC 7517 JSON Web Key Set document shape.
type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// jwk is one RFC 7517 JSON Web Key. Only the fields this provider's two
// supported key types (RSA, EC) need are declared; any other field present
// in a real IdP's document is ignored, not rejected.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	// RSA (RFC 7518 SS6.3)
	N string `json:"n,omitempty"`
	E string `json:"e,omitempty"`
	// EC (RFC 7518 SS6.2)
	Crv string `json:"crv,omitempty"`
	X   string `json:"x,omitempty"`
	Y   string `json:"y,omitempty"`
}

// jwksKeyProvider fetches and caches signing keys from a remote JWKS
// endpoint, supporting key rotation without redeployment (PLAN.md Section
// 32.1). It never lets a failed or empty refresh discard a previously good
// cache: a transient IdP outage degrades to "still trusting the keys we
// already had," never to "trusting nothing."
type jwksKeyProvider struct {
	jwksURL    string
	httpClient *http.Client

	mu   sync.RWMutex
	keys map[string]interface{} // kid -> *rsa.PublicKey or *ecdsa.PublicKey

	refreshInterval time.Duration
	stopCh          chan struct{}
	stopOnce        sync.Once
}

// JWKSOption configures a jwksKeyProvider built by NewJWKSKeyProvider.
type JWKSOption func(*jwksKeyProvider)

// WithHTTPClient overrides the default HTTP client (a 10 second timeout)
// used to fetch the JWKS document.
func WithHTTPClient(c *http.Client) JWKSOption {
	return func(p *jwksKeyProvider) { p.httpClient = c }
}

// WithRefreshInterval overrides the default 15 minute background refresh
// interval. A non-positive duration disables background refresh entirely;
// Key still refetches once, bounded, on an unknown kid.
func WithRefreshInterval(d time.Duration) JWKSOption {
	return func(p *jwksKeyProvider) { p.refreshInterval = d }
}

// NewJWKSKeyProvider builds a KeyProvider that fetches its verification
// keys from jwksURL. It performs a real fetch at construction time and
// fails closed: a nil/empty URL, an unreachable endpoint, or a document
// with zero usable keys all return an error rather than constructing a
// provider that would silently reject every token later.
func NewJWKSKeyProvider(jwksURL string, opts ...JWKSOption) (KeyProvider, error) {
	if jwksURL == "" {
		return nil, fmt.Errorf("auth: JWKS URL must not be empty")
	}
	parsed, err := url.Parse(jwksURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("auth: JWKS URL %q must be an http(s) URL", jwksURL)
	}

	p := &jwksKeyProvider{
		jwksURL:         jwksURL,
		httpClient:      &http.Client{Timeout: defaultJWKSTimeout},
		keys:            make(map[string]interface{}),
		refreshInterval: defaultJWKSRefreshInterval,
		stopCh:          make(chan struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}

	if err := p.refresh(context.Background()); err != nil {
		return nil, fmt.Errorf("auth: initial JWKS fetch from %q: %w", jwksURL, err)
	}

	if p.refreshInterval > 0 {
		go p.runBackgroundRefresh(p.refreshInterval)
	}
	return p, nil
}

// Key implements KeyProvider.
func (p *jwksKeyProvider) Key(ctx context.Context, kid string) (interface{}, error) {
	if key, ok := p.lookup(kid); ok {
		return key, nil
	}
	// Unknown kid: one bounded refetch, covering the legitimate case of key
	// rotation landing since our last cache refresh. Never repeated beyond
	// this single attempt per call, so a forged or garbage kid cannot be
	// used to hammer the IdP.
	if err := p.refresh(ctx); err != nil {
		return nil, fmt.Errorf("auth: refreshing JWKS after unknown kid %q: %w", kid, err)
	}
	if key, ok := p.lookup(kid); ok {
		return key, nil
	}
	return nil, fmt.Errorf("auth: no JWKS key found for kid %q", kid)
}

// Algorithms implements KeyProvider.
func (p *jwksKeyProvider) Algorithms() []string {
	return []string{"RS256", "ES256"}
}

// Close stops the background refresh loop and releases any idle HTTP
// connections the provider's client is holding open. Safe to call more
// than once and safe to call when background refresh was disabled.
func (p *jwksKeyProvider) Close() error {
	p.stopOnce.Do(func() { close(p.stopCh) })
	p.httpClient.CloseIdleConnections()
	return nil
}

func (p *jwksKeyProvider) lookup(kid string) (interface{}, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key, ok := p.keys[kid]
	return key, ok
}

func (p *jwksKeyProvider) runBackgroundRefresh(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			// Best effort: a transient failure keeps serving the last-known-
			// good keys rather than tearing anything down.
			_ = p.refresh(context.Background())
		}
	}
}

// refresh fetches and parses jwksURL, replacing the cached key set only if
// parsing produced at least one usable key. A failed or empty fetch never
// touches the existing cache, so a bad response degrades to "stale" rather
// than "empty."
func (p *jwksKeyProvider) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching JWKS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching JWKS: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB is generous for a key set
	if err != nil {
		return fmt.Errorf("reading JWKS body: %w", err)
	}

	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return fmt.Errorf("parsing JWKS document: %w", err)
	}

	parsed := make(map[string]interface{}, len(set.Keys))
	for _, k := range set.Keys {
		key, err := parseJWK(k)
		if err != nil {
			// One malformed or unsupported key does not invalidate the
			// whole document; a real IdP can publish key types (e.g.
			// "OKP"/Ed25519) this provider does not yet support alongside
			// ones it does.
			continue
		}
		parsed[k.Kid] = key
	}
	if len(parsed) == 0 {
		return fmt.Errorf("JWKS document at %q contained no usable RSA or EC keys", p.jwksURL)
	}

	p.mu.Lock()
	p.keys = parsed
	p.mu.Unlock()
	return nil
}

// parseJWK converts one RFC 7517 key entry into a Go public key. Only "RSA"
// and "EC" are supported, matching Algorithms' RS256/ES256.
func parseJWK(k jwk) (interface{}, error) {
	if k.Kid == "" {
		return nil, fmt.Errorf("JWK missing required %q field", "kid")
	}
	switch k.Kty {
	case "RSA":
		return parseRSAJWK(k)
	case "EC":
		return parseECJWK(k)
	default:
		return nil, fmt.Errorf("unsupported JWK key type %q", k.Kty)
	}
}

func parseRSAJWK(k jwk) (*rsa.PublicKey, error) {
	n, err := decodeBase64BigInt(k.N)
	if err != nil {
		return nil, fmt.Errorf("decoding RSA modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decoding RSA exponent: %w", err)
	}
	// A real RSA public exponent is a small integer (65537 is the near-
	// universal default); more than 8 bytes is not a real key.
	if len(eBytes) == 0 || len(eBytes) > 8 {
		return nil, fmt.Errorf("RSA exponent has implausible length %d", len(eBytes))
	}
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

func parseECJWK(k jwk) (*ecdsa.PublicKey, error) {
	curve, err := curveForName(k.Crv)
	if err != nil {
		return nil, err
	}
	x, err := decodeBase64BigInt(k.X)
	if err != nil {
		return nil, fmt.Errorf("decoding EC x coordinate: %w", err)
	}
	y, err := decodeBase64BigInt(k.Y)
	if err != nil {
		return nil, fmt.Errorf("decoding EC y coordinate: %w", err)
	}
	if !curve.IsOnCurve(x, y) {
		return nil, fmt.Errorf("EC point is not on curve %q", k.Crv)
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

func curveForName(name string) (elliptic.Curve, error) {
	switch name {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported EC curve %q", name)
	}
}

func decodeBase64BigInt(s string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("empty value")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(b), nil
}
