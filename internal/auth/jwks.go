package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
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

	// DefaultJWKSMaxStaleness is how long a fetched key set is trusted
	// without a successful refresh: 12 hours. Past it every token is
	// refused until a refresh succeeds (FAILURE_PATTERNS 1228).
	//
	// The bound exists for a key the identity provider withdrew while this
	// Controller could not reach it: without one, whoever held that key
	// could sign tokens here for as long as the outage lasted. Twelve hours
	// outlasts a shift or an overnight maintenance window, and is 48 of the
	// default 15 minute refreshes, so a run of missed refreshes does not
	// trip it. It is a judgment rather than a derived number; a deployment
	// setting for it is planned (Phase 142).
	DefaultJWKSMaxStaleness = 12 * time.Hour

	// defaultUnknownKidCooldown is the shortest gap between two fetches a
	// request can cause, by naming a kid the cache does not hold or by
	// meeting a stale key set (FAILURE_PATTERNS 1227).
	//
	// A token's kid is read before its signature is checked, and before the
	// rate limiter, so an anonymous caller chooses it. Without this gap each
	// such request was one outbound fetch. One minute keeps that to one
	// fetch a minute whatever the request rate, and costs a real key
	// rotation at most a minute: a new kid arriving inside the gap is
	// refused until it ends, and the background refresh finds it anyway.
	defaultUnknownKidCooldown = time.Minute
)

// Refusals Key returns. None of them carries the caller's kid, which is
// attacker-chosen text.
var (
	// errNoKid is a token with no kid header. Every key this provider holds
	// has a kid (parseJWK refuses one without), so no fetch could find a key
	// for it, and none is made.
	errNoKid = errors.New("auth: the token names no kid, and every JWKS key has one")

	// errUnknownKid is a kid the key set does not hold, even after a fetch.
	errUnknownKid = errors.New("auth: no JWKS key has the token's kid")

	// errRefetchCoolingDown is a fetch a request asked for inside the
	// cooldown after the last one.
	errRefetchCoolingDown = errors.New("the key set was fetched less than a cooldown ago")
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
// 32.1). A failed or empty refresh never discards a previously good cache,
// so a short IdP outage degrades to "still trusting the keys we already
// had". That trust has an age limit, maxStaleness: past it the cached keys
// are refused, so a long outage ends in refusing every token rather than in
// trusting a key the IdP may have withdrawn.
type jwksKeyProvider struct {
	jwksURL    string
	displayURL string // jwksURL with any password removed, for messages and logs
	httpClient *http.Client
	logger     *slog.Logger     // nil means slog.Default(), read when a line is written
	now        func() time.Time // the clock key age is measured on

	mu        sync.RWMutex
	keys      map[string]interface{} // kid -> *rsa.PublicKey or *ecdsa.PublicKey
	fetchedAt time.Time              // when keys was last replaced by a successful fetch
	failures  int                    // refreshes failed in a row since the last success

	refreshInterval    time.Duration
	maxStaleness       time.Duration
	unknownKidCooldown time.Duration
	stopCh             chan struct{}
	stopOnce           sync.Once

	// flightMu guards the request-path fetch: inflight is the one fetch
	// running for requests, shared by every request that needs it, and
	// lastOnDemand is when the latest one began, for the cooldown.
	flightMu     sync.Mutex
	inflight     *jwksFlight
	lastOnDemand time.Time
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
// a request still causes a fetch, shared and rate limited, on an unknown
// kid or a stale key set.
func WithRefreshInterval(d time.Duration) JWKSOption {
	return func(p *jwksKeyProvider) { p.refreshInterval = d }
}

// WithMaxStaleness overrides DefaultJWKSMaxStaleness, how long the cached
// keys are trusted without a successful refresh. There is no "off" value:
// NewJWKSKeyProvider refuses a non-positive duration.
func WithMaxStaleness(d time.Duration) JWKSOption {
	return func(p *jwksKeyProvider) { p.maxStaleness = d }
}

// WithLogger sets where failed refreshes are reported. Without it the
// provider writes to slog.Default(), read at the moment of writing, so a
// composition root that installs its logger after building the provider
// still receives every line.
func WithLogger(l *slog.Logger) JWKSOption {
	return func(p *jwksKeyProvider) { p.logger = l }
}

// NewJWKSKeyProvider builds a KeyProvider that fetches its verification
// keys from jwksURL. It performs a real fetch at construction time and
// fails closed: an empty URL, one that is not https, an unreachable
// endpoint, or a document with zero usable keys all return an error rather
// than constructing a provider that would silently reject every token later.
//
// https is required because the key set is the trust anchor: whoever can
// change it in transit can sign any token this Controller accepts
// (FAILURE_PATTERNS 1229).
func NewJWKSKeyProvider(jwksURL string, opts ...JWKSOption) (KeyProvider, error) {
	if jwksURL == "" {
		return nil, fmt.Errorf("auth: JWKS URL must not be empty")
	}
	parsed, err := url.Parse(jwksURL)
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("auth: JWKS URL is not an absolute URL")
	}
	if parsed.Scheme != "https" {
		// The scheme alone, never the URL: a URL can carry a password.
		return nil, fmt.Errorf("auth: JWKS URL must be an https URL, not %q: the key set decides which tokens are accepted, so it is never fetched over plain HTTP", parsed.Scheme)
	}

	p := &jwksKeyProvider{
		jwksURL:            jwksURL,
		displayURL:         parsed.Redacted(),
		httpClient:         &http.Client{Timeout: defaultJWKSTimeout},
		now:                time.Now,
		keys:               make(map[string]interface{}),
		refreshInterval:    defaultJWKSRefreshInterval,
		maxStaleness:       DefaultJWKSMaxStaleness,
		unknownKidCooldown: defaultUnknownKidCooldown,
		stopCh:             make(chan struct{}),
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.maxStaleness <= 0 {
		return nil, fmt.Errorf("auth: the JWKS staleness bound must be positive, got %s", p.maxStaleness)
	}

	if err := p.refresh(context.Background()); err != nil {
		return nil, fmt.Errorf("auth: initial JWKS fetch from %s: %w", p.displayURL, err)
	}

	if p.refreshInterval > 0 {
		go p.runBackgroundRefresh(p.refreshInterval)
	}
	return p, nil
}

// Key implements KeyProvider.
//
// A cached key is returned only while the key set is younger than
// maxStaleness. An unknown kid (a rotation since the last refresh) or a
// stale key set causes one fetch, shared by every request waiting on it and
// at most one per cooldown, so no rate of requests can turn into the same
// rate of fetches from the IdP (FAILURE_PATTERNS 1227).
func (p *jwksKeyProvider) Key(ctx context.Context, kid string) (interface{}, error) {
	if kid == "" {
		return nil, errNoKid
	}
	if key, known, fresh := p.lookup(kid); known && fresh {
		return key, nil
	}

	fetchErr := p.refetchOnDemand(ctx)
	key, known, fresh := p.lookup(kid)
	switch {
	case known && fresh:
		return key, nil
	case known:
		// The kid is one the IdP published, but nothing has confirmed the
		// key set for longer than the bound allows.
		return nil, p.staleError(fetchErr)
	case fetchErr != nil:
		return nil, fmt.Errorf("auth: the token's kid is not in the cached key set, and refreshing it failed: %w", fetchErr)
	default:
		return nil, errUnknownKid
	}
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

// lookup returns the cached key for kid, whether the cache holds one, and
// whether the key set is still inside its staleness bound.
func (p *jwksKeyProvider) lookup(kid string) (key interface{}, known, fresh bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	key, known = p.keys[kid]
	return key, known, keyAge(p.now(), p.fetchedAt) <= p.maxStaleness
}

// runBackgroundRefresh refetches the key set every interval until Close.
func (p *jwksKeyProvider) runBackgroundRefresh(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			// A failure keeps the last good keys, inside their staleness
			// bound. refreshAndReport has already logged it, which is the
			// one thing this loop owes it (FAILURE_PATTERNS 1228).
			_ = p.refreshAndReport(context.Background(), "background")
		}
	}
}

// refresh fetches and parses jwksURL, replacing the cached key set only if
// parsing produced at least one usable key. A failed or empty fetch never
// touches the existing cache or its fetch time, so a bad response degrades
// to "stale" rather than "empty", and a stale set keeps aging toward the
// bound.
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB is generous for a key set
	if err != nil {
		return fmt.Errorf("reading JWKS body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// The identity provider's own reason, bounded and quoted.
		return fmt.Errorf("fetching JWKS: unexpected status %d: %s", resp.StatusCode, farside.Text(string(body)))
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
		return fmt.Errorf("the JWKS document contained no usable RSA or EC keys")
	}

	p.mu.Lock()
	p.keys = parsed
	p.fetchedAt = p.now()
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
