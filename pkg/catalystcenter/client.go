// Package catalystcenter is a minimal REST client for Cisco Catalyst
// Center (formerly DNA Center), covering the read-only endpoints Pleiades
// needs to import an inventory and gather facts.
//
// It lives under pkg/ rather than internal/ for a structural reason, not a
// visibility one. A built-in Collection method may import only pkg/,
// matching the constraint a third-party Collection will have to satisfy
// once Part X's OCI distribution exists, so a client shared between the
// net.catalyst.* methods and the inventory sync plugin has to live here or
// be written twice.
//
// It deliberately does not use internal/transport. That port is
// command-oriented (Exec sends a command string to a target and returns
// stdout, stderr, and an exit code), which is the right shape for SSH and
// the wrong shape for a REST API with paging, bearer tokens, and JSON
// bodies. Forcing this through Exec would mean encoding HTTP requests as
// command strings, which is a worse fit than net/http used directly.
//
// Scope is deliberately narrow: this client reads. There is no create,
// update, or delete anywhere in it, because the one thing Pleiades does
// with a Catalyst Center is treat it as an authoritative upstream source.
package catalystcenter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout bounds a single HTTP request. Catalyst Center's inventory
// endpoints are ordinarily fast, and a request that has not answered in
// this long is a network problem rather than a slow query.
const DefaultTimeout = 30 * time.Second

// maxResponseBytes caps how much of a response body is read. A controller
// is a trusted-ish upstream, but "trusted" is not "allowed to exhaust this
// process's memory": a compromised or malfunctioning endpoint streaming an
// unbounded body would otherwise take the runner down with it.
const maxResponseBytes = 64 << 20 // 64 MiB

// Client talks to one Catalyst Center. The zero value is not usable;
// construct one with New.
type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	// mu guards token and tokenExpiry. Several goroutines may share one
	// Client (a fact-gathering task fanned out across devices, for
	// instance), and an unguarded refresh would let two of them
	// authenticate at once and race on the stored token.
	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

// Option customizes a Client at construction time.
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client, so a deployment
// behind a proxy can supply a transport that knows about it.
//
// It has no callers today. The previous version of this comment claimed
// "tests use it to point at an httptest.Server", which was never true:
// this package's own tests reach their test server through New's baseURL
// argument, exactly as a real deployment reaches a real controller, and
// so does the inventory sync plugin through syncplugin.Config.Endpoint.
// A doc comment naming a caller that does not exist defeats the one
// check somebody would run to find it, which is why the claim is
// recorded as removed rather than quietly dropped.
//
// Order matters against WithInsecureSkipVerify: that option rebuilds the
// client from scratch, carrying over only Timeout, so it discards a
// transport this option installed earlier in the same New call. Pass
// this one last if both are needed, or build the insecure TLS config
// into the transport handed here and skip the other.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) { c.httpClient = hc }
}

// WithInsecureSkipVerify disables TLS certificate verification.
//
// It replaces the whole http.Client, keeping only the existing Timeout,
// so it silently discards a transport an earlier WithHTTPClient
// installed. See that option's doc comment for how to hold both.
//
// It exists because appliances ship with self-signed certificates, and a
// client that cannot express that pushes users to disable verification
// somewhere far less visible, like an environment variable read by every
// process on the box. Every caller that passes it is stating so in
// configuration a reviewer can grep for.
func WithInsecureSkipVerify(skip bool) Option {
	return func(c *Client) {
		if !skip {
			return
		}
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // #nosec G402 -- opt-in only, see this option's doc comment
				MinVersion:         tls.VersionTLS12,
			},
		}
		c.httpClient = &http.Client{Timeout: c.httpClient.Timeout, Transport: transport}
	}
}

// New creates a client for the Catalyst Center at baseURL, authenticating
// as username. It does not contact the controller: the first request that
// needs a token fetches one.
func New(baseURL, username, password string, opts ...Option) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("catalystcenter: invalid base URL %q: %w", baseURL, err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("catalystcenter: base URL scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("catalystcenter: base URL has no host: %s", baseURL)
	}
	if username == "" {
		return nil, fmt.Errorf("catalystcenter: username is required")
	}

	// Store the parsed form rather than the caller's raw string, so the
	// scheme and host are normalized exactly once. url.Parse already
	// lowercases the scheme, which means "Http://host" and "http://host"
	// would otherwise be stored as two different strings for the same
	// controller. That matters beyond tidiness: this value becomes the
	// synced controller's DeviceID and its catalyst_base_url property, so
	// an un-normalized copy would onboard the same appliance twice.
	normalized := &url.URL{
		Scheme: parsed.Scheme,
		Host:   parsed.Host,
		Path:   strings.TrimRight(parsed.Path, "/"),
	}

	c := &Client{
		baseURL:    normalized.String(),
		username:   username,
		password:   password,
		httpClient: &http.Client{Timeout: DefaultTimeout},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// BaseURL returns the controller's base URL, which the inventory device
// record and the CatalystAPICapable accessor both need.
func (c *Client) BaseURL() string { return c.baseURL }

// Close releases the client's pooled connections and discards its cached
// token. It is safe to call more than once, and a closed client still
// works: the next request simply opens a fresh connection and
// re-authenticates.
//
// It exists because HTTP keep-alive connections outlive the request that
// opened them by design, which is ordinarily what you want and is exactly
// what a goroutine-leak check flags at the end of a test. Without a way to
// say "I am done with this controller", every caller either leaks pooled
// connections for the process lifetime or reaches around this type to the
// http.Client it was built with.
func (c *Client) Close() error {
	c.httpClient.CloseIdleConnections()
	c.invalidateToken()
	return nil
}

// get issues an authenticated GET against path with query, decoding the
// JSON response body into out.
//
// It retries exactly once, and only on a 401, and only after discarding the
// cached token. Catalyst Center tokens expire, and a token that expired
// between two pages of the same listing is the ordinary case rather than an
// error worth surfacing. It is deliberately not a general retry loop: a 500
// or a timeout means something is wrong upstream, and hammering it would
// make that worse.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	body, err := c.doGet(ctx, path, query)
	if err != nil {
		var authErr *authExpiredError
		if !asAuthExpired(err, &authErr) {
			return err
		}
		c.invalidateToken()
		if body, err = c.doGet(ctx, path, query); err != nil {
			return err
		}
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("catalystcenter: decoding %s response: %w", path, err)
	}
	return nil
}

// doGet performs one authenticated GET and returns the raw response body.
func (c *Client) doGet(ctx context.Context, path string, query url.Values) ([]byte, error) {
	token, err := c.authToken(ctx)
	if err != nil {
		return nil, err
	}

	endpoint := c.baseURL + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("catalystcenter: building request for %s: %w", path, err)
	}
	req.Header.Set("X-Auth-Token", token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalystcenter: requesting %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &authExpiredError{path: path}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalystcenter: %s returned %s", path, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("catalystcenter: reading %s response: %w", path, err)
	}
	return body, nil
}
