package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// tokenLifetime is how long a fetched token is treated as usable.
//
// Catalyst Center's token endpoint returns a bare JWT and no expiry field,
// so there is nothing authoritative to read. An hour is comfortably inside
// the controller's own default, and the 401-then-refresh path in Client.get
// is what actually makes expiry correct: this value only decides how often
// the happy path re-authenticates, never whether an expired token is
// noticed.
const tokenLifetime = time.Hour

// authPath is Catalyst Center's token endpoint. It is the one endpoint that
// uses basic auth rather than a bearer token.
const authPath = "/dna/system/api/v1/auth/token"

// authExpiredError marks a 401 so Client.get can tell "the token aged out,
// refresh and retry once" apart from "these credentials are wrong", which
// looks identical at the HTTP layer and must not be retried.
type authExpiredError struct {
	path string
}

func (e *authExpiredError) Error() string {
	return fmt.Sprintf("catalystcenter: %s returned 401 unauthorized", e.path)
}

// asAuthExpired is errors.As specialized to authExpiredError, so the retry
// site in Client.get reads as one condition rather than three lines of
// error plumbing.
func asAuthExpired(err error, target **authExpiredError) bool {
	return errors.As(err, target)
}

// authToken returns a usable token, fetching one if none is cached or the
// cached one has aged out.
func (c *Client) authToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	token, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}

	c.token = token
	c.tokenExpiry = time.Now().Add(tokenLifetime)
	return token, nil
}

// invalidateToken discards the cached token so the next request fetches a
// fresh one. It is what the 401 retry path calls.
func (c *Client) invalidateToken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = ""
	c.tokenExpiry = time.Time{}
}

// tokenResponse is the token endpoint's body. The field is capitalized in
// the wire format, which is why it needs an explicit tag.
type tokenResponse struct {
	Token string `json:"Token"`
}

// fetchToken authenticates with basic auth and returns a fresh token. The
// caller must hold c.mu.
func (c *Client) fetchToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+authPath, nil)
	if err != nil {
		return "", fmt.Errorf("catalystcenter: building auth request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("catalystcenter: authenticating: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		// Deliberately not an authExpiredError: a 401 from the token
		// endpoint itself means the credentials are wrong, and retrying
		// with the same credentials would just lock the account out.
		return "", fmt.Errorf("catalystcenter: authentication rejected for user %q", c.username)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("catalystcenter: auth endpoint returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("catalystcenter: reading auth response: %w", err)
	}

	var decoded tokenResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return "", fmt.Errorf("catalystcenter: decoding auth response: %w", err)
	}
	if decoded.Token == "" {
		return "", errors.New("catalystcenter: auth response carried no token")
	}
	return decoded.Token, nil
}
