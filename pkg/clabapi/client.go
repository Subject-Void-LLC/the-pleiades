// Package clabapi is the client for a containerlab API server (the REST API
// srl-labs/clab-api-server serves) and the reference text of every method the
// virt.clab collection builds on it.
//
// The client logs in with a username and password, holds the token it gets
// for one call sequence only, and sends it only to the origin it was made for.
// Neither the password nor the token is returned, logged or put in an error: an
// answer that echoes one back is masked before it is quoted.
package clabapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

// maxBody bounds one answer from the server.
const maxBody = 8 << 20

// Client calls one containerlab API server.
type Client struct {
	base *url.URL
	http *http.Client
	// secrets are the values masked wherever the client quotes the server: the
	// password, and the token once a login has returned one.
	secrets []string
}

// NewClient returns a client for the server at base. tlsConfig is the
// device's own TLS (nil for an http:// base), and secrets are the values that
// must never appear in an error or an answer quoted from the server: they are
// masked by value wherever the client quotes it. No proxy is used and a
// redirect is followed only within the server's origin, so a login reaches the
// base URL's origin and nowhere else.
func NewClient(base *url.URL, tlsConfig *tls.Config, secrets ...string) *Client {
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 30 * time.Second, TLSClientConfig: tlsConfig}
	return &Client{
		base: base,
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 || !httpapi.SameOrigin(base, req.URL) {
					return http.ErrUseLastResponse
				}
				return nil
			},
		},
		secrets: secrets,
	}
}

// maskText replaces each secret the client holds in s.
func (c *Client) maskText(s string) string {
	for _, secret := range c.secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[masked]")
		}
	}
	return s
}

// APIError is an answer the server gave with a status other than 200: its
// status and its own words.
type APIError struct {
	// Status is the HTTP status.
	Status int
	// Path is the route called, without its query.
	Path string
	// Message is the server's error text, masked and bounded.
	Message string
}

// Error describes the refusal.
func (e *APIError) Error() string {
	return fmt.Sprintf("%s returned %d: %s", e.Path, e.Status, e.Message)
}

// NotFound reports whether err is a 404 from the server.
func NotFound(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusNotFound
}

// Unauthorized reports whether err is the server refusing the token (a 401):
// the request was refused before it ran, so asking again after a fresh login
// repeats nothing.
func Unauthorized(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Status == http.StatusUnauthorized
}

// Login logs in and returns the bearer token. The token is the caller's to
// hold for the calls it makes now and to drop after.
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	if username == "" || password == "" {
		return "", errors.New("the containerlab API server needs a stored username and password for this device (http_auth basic)")
	}
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", err
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := c.Call(ctx, "", http.MethodPost, "/login", nil, bytes.NewReader(body), &out); err != nil {
		return "", fmt.Errorf("logging in: %w", err)
	}
	if out.Token == "" {
		return "", errors.New("logging in: the server returned no token")
	}
	// From here an answer that echoes the token back is masked too.
	c.secrets = append(c.secrets, out.Token)
	return out.Token, nil
}

// Call sends one request to path on the server and decodes a 200 answer as
// JSON into out (nil to discard it, a *string to take the body as text). A
// body, when given, is sent as JSON. Any other status is an *APIError.
func (c *Client) Call(ctx context.Context, token, method, path string, query url.Values, body io.Reader, out any) error {
	ref := path
	if len(query) > 0 {
		ref += "?" + query.Encode()
	}
	full, err := httpapi.Join(c.base, ref)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, full.String(), body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %s", path, c.maskText(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	content, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return fmt.Errorf("%s: reading the answer: %s", path, c.maskText(err.Error()))
	}
	if len(content) > maxBody {
		return fmt.Errorf("%s: the answer is larger than %d MiB", path, maxBody>>20)
	}
	if resp.StatusCode != http.StatusOK {
		return &APIError{Status: resp.StatusCode, Path: path, Message: c.said(content)}
	}
	switch o := out.(type) {
	case nil:
		return nil
	case *string:
		*o = string(content)
		return nil
	}
	if err := json.Unmarshal(content, out); err != nil {
		return fmt.Errorf("%s: the answer is not the JSON expected: %w", path, err)
	}
	return nil
}

// said is the server's error text: the "error" of a JSON answer, else the
// body, masked and cut to a length fit for an error.
func (c *Client) said(content []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	text := strings.TrimSpace(string(content))
	if json.Unmarshal(content, &e) == nil && e.Error != "" {
		text = e.Error
	}
	return farside.OutputTail(c.maskText(text))
}
