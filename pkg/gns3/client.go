// Package gns3 is a client for a GNS3 server's REST API, the part an import
// reads: the server's version, its projects and each project's nodes. It
// speaks both server lines, 3.x (/v3, a bearer token from its login) and
// 2.2.x (/v2, basic auth when the server enables it), measured against
// gns3-server 3.0.6 and 2.2.61 (Phase 127c's spike). It never changes a
// project: a closed project's nodes are listed as the server keeps them.
package gns3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxResponse bounds a response body read: a project of a few hundred
// nodes is well under a megabyte.
const maxResponse = 16 << 20

// The two API lines a server may speak.
const (
	APIv3 = "v3"
	APIv2 = "v2"
)

// ErrNeedsLogin is Connect's answer for a 3.x server given no username:
// every call there needs a token.
var ErrNeedsLogin = errors.New("gns3: a GNS3 3.x server answers nothing without a login; give a username and password")

// Credentials are the account a client logs in as. A 3.x server needs
// them; a 2.2.x server takes them as basic auth when it asks for any.
type Credentials struct {
	Username string
	Password string
}

// Client talks to one GNS3 server.
type Client struct {
	base  *url.URL
	http  *http.Client
	creds Credentials

	api   string
	token string
}

// New returns a client for the server at base (http:// or https://, with
// its port), sending requests through hc.
func New(base string, hc *http.Client, creds Credentials) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("gns3: %q is not a server's http or https base URL (scheme, host and port only)", base)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{base: u, http: hc, creds: creds}, nil
}

// Version is what a server says it runs.
type Version struct {
	// API is APIv3 or APIv2, the line the client now speaks.
	API string `json:"api"`
	// Version is the server's own version, e.g. "3.0.6".
	Version string `json:"version"`
}

// Connect learns which API line the server speaks, /v3 first, and logs in
// on a 3.x server. Every other call needs it first.
func (c *Client) Connect(ctx context.Context) (Version, error) {
	for _, api := range []string{APIv3, APIv2} {
		status, out, err := c.raw(ctx, http.MethodGet, "/"+api+"/version", nil, "", api == APIv2)
		if err != nil {
			return Version{}, err
		}
		if status == http.StatusNotFound {
			continue
		}
		if status/100 != 2 {
			return Version{}, fmt.Errorf("gns3: GET /%s/version: %d: %s", api, status, firstLine(string(out)))
		}
		var v struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(out, &v); err != nil || v.Version == "" {
			return Version{}, fmt.Errorf("gns3: /%s/version names no version", api)
		}
		c.api = api
		if api == APIv3 {
			if err := c.login(ctx); err != nil {
				return Version{}, err
			}
		}
		return Version{API: api, Version: v.Version}, nil
	}
	return Version{}, errors.New("gns3: the server answers neither /v3/version nor /v2/version, so it is not a GNS3 server this client knows")
}

// login takes a bearer token from a 3.x server's OAuth2 password login.
func (c *Client) login(ctx context.Context) error {
	if c.creds.Username == "" {
		return ErrNeedsLogin
	}
	form := url.Values{"username": {c.creds.Username}, "password": {c.creds.Password}}
	status, out, err := c.raw(ctx, http.MethodPost, "/v3/access/users/login", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", false)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return fmt.Errorf("gns3: the server refused the login of %q", c.creds.Username)
	}
	if status/100 != 2 {
		return fmt.Errorf("gns3: POST /v3/access/users/login: %d: %s", status, firstLine(string(out)))
	}
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(out, &t); err != nil || t.AccessToken == "" {
		return errors.New("gns3: the login answer holds no access token")
	}
	c.token = t.AccessToken
	return nil
}

// Project is one project on the server.
type Project struct {
	ID     string `json:"project_id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Projects lists the server's projects.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	if err := c.get(ctx, "/projects", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Node is one node of a project, as the import reads it.
type Node struct {
	ID       string `json:"node_id"`
	Name     string `json:"name"`
	NodeType string `json:"node_type"`
	// Console is the console's TCP port, nil when the node has none.
	Console     *int   `json:"console"`
	ConsoleType string `json:"console_type"`
	// ConsoleHost is where the console listens, "0.0.0.0" for every
	// address of the server's compute.
	ConsoleHost string `json:"console_host"`
	Status      string `json:"status"`
}

// Nodes lists a project's nodes, open or closed.
func (c *Client) Nodes(ctx context.Context, projectID string) ([]Node, error) {
	var out []Node
	if err := c.get(ctx, "/projects/"+url.PathEscape(projectID)+"/nodes", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// get reads one JSON answer under the connected API line.
func (c *Client) get(ctx context.Context, path string, into any) error {
	if c.api == "" {
		return errors.New("gns3: Connect first")
	}
	full := "/" + c.api + path
	status, out, err := c.raw(ctx, http.MethodGet, full, nil, "", c.api == APIv2)
	if err != nil {
		return err
	}
	if status/100 != 2 {
		return fmt.Errorf("gns3: GET %s: %d: %s", full, status, firstLine(string(out)))
	}
	if err := json.Unmarshal(out, into); err != nil {
		return fmt.Errorf("gns3: GET %s: the answer is not what this client reads: %w", full, err)
	}
	return nil
}

// raw sends one request and returns the status and the bounded body. basic
// sends the credentials as basic auth, which a 2.2.x server takes.
func (c *Client) raw(ctx context.Context, method, path string, body io.Reader, contentType string, basic bool) (int, []byte, error) {
	u := *c.base
	u.Path = path
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	switch {
	case c.token != "":
		req.Header.Set("Authorization", "Bearer "+c.token)
	case basic && c.creds.Username != "":
		req.SetBasicAuth(c.creds.Username, c.creds.Password)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("gns3: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return 0, nil, fmt.Errorf("gns3: %s %s: reading the answer: %w", method, path, err)
	}
	if len(out) > maxResponse {
		return 0, nil, fmt.Errorf("gns3: %s %s: the answer is over %d bytes", method, path, maxResponse)
	}
	return res.StatusCode, out, nil
}

// firstLine is a server's words fit for an error: the first line, at most
// 300 bytes, quoted so a control character is shown rather than acted on.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return fmt.Sprintf("%q", s)
}
