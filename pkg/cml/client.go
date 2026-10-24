// Package cml is a client for a Cisco Modeling Labs 2.x controller's REST
// API (/api/v0), the part an import reads: the controller's version, its
// labs and each lab's nodes with the addresses the controller observed.
// Measured against CML 2.7.2 in Cisco's DevNet sandbox (Phase 127c). It
// never changes a lab.
package cml

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// maxResponse bounds a response body read: a lab's nodes with their
// configurations run to megabytes, not more.
const maxResponse = 32 << 20

// Credentials are the account the client logs in as.
type Credentials struct {
	Username string
	Password string
}

// Client talks to one CML controller.
type Client struct {
	base  *url.URL
	http  *http.Client
	creds Credentials
	token string
}

// New returns a client for the controller at base (https://, or http://),
// sending requests through hc.
func New(base string, hc *http.Client, creds Credentials) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return nil, fmt.Errorf("cml: %q is not a controller's http or https base URL (scheme, host and port only)", base)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{base: u, http: hc, creds: creds}, nil
}

// Connect reads the controller's version and logs in. Every other call
// needs it first.
func (c *Client) Connect(ctx context.Context) (string, error) {
	var sys struct {
		Version string `json:"version"`
		Ready   bool   `json:"ready"`
	}
	if err := c.call(ctx, http.MethodGet, "/system_information", nil, &sys); err != nil {
		return "", err
	}
	if sys.Version == "" {
		return "", errors.New("cml: /api/v0/system_information names no version, so it is not a CML 2 controller")
	}
	if !sys.Ready {
		return "", fmt.Errorf("cml: the controller (CML %s) says it is not ready", sys.Version)
	}
	if c.creds.Username == "" {
		return "", errors.New("cml: a CML controller answers nothing without a login; give a username and password")
	}
	body, err := json.Marshal(map[string]string{"username": c.creds.Username, "password": c.creds.Password})
	if err != nil {
		return "", err
	}
	var token string
	if err := c.call(ctx, http.MethodPost, "/authenticate", body, &token); err != nil {
		var se *StatusError
		switch {
		case errors.As(err, &se) && (se.Status == http.StatusForbidden || se.Status == http.StatusUnauthorized):
			return "", fmt.Errorf("cml: the controller refused the login of %q", c.creds.Username)
		case se != nil:
			// The answer to a login is never quoted: a validation error
			// (FastAPI's 422) echoes the request, password included.
			return "", fmt.Errorf("cml: %s /api/v0/authenticate: %d (the answer is not shown, since it can echo the login)", http.MethodPost, se.Status)
		}
		return "", err
	}
	if token == "" {
		return "", errors.New("cml: the login answer holds no token")
	}
	c.token = token
	return sys.Version, nil
}

// Lab is one lab on the controller.
type Lab struct {
	ID    string `json:"id"`
	Title string `json:"lab_title"`
	State string `json:"state"`
}

// Labs lists the labs this login can see.
func (c *Client) Labs(ctx context.Context) ([]Lab, error) {
	var ids []string
	if err := c.get(ctx, "/labs", &ids); err != nil {
		return nil, err
	}
	labs := make([]Lab, 0, len(ids))
	for _, id := range ids {
		var lab Lab
		if err := c.get(ctx, "/labs/"+url.PathEscape(id), &lab); err != nil {
			return nil, err
		}
		lab.ID = id
		labs = append(labs, lab)
	}
	return labs, nil
}

// Node is one node of a lab, as the import reads it.
type Node struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	NodeDefinition string `json:"node_definition"`
	State          string `json:"state"`
	// Addresses are the IPv4 addresses the controller observed on the
	// node's interfaces, sorted; empty when it observed none.
	Addresses []netip.Addr `json:"-"`
	// ConfiguredAddresses are the IPv4 addresses the node's own startup
	// configuration names, sorted: a hint, never an address the import
	// takes on its own, since a configuration names every interface's.
	ConfiguredAddresses []netip.Addr `json:"-"`
}

// configAddress finds an IPv4 address an IOS or Linux configuration
// assigns: "ip address A.B.C.D", "ip addr add A.B.C.D/N", "address A.B.C.D",
// "ifconfig eth0 A.B.C.D".
var configAddress = regexp.MustCompile(`(?m)(?:^\s*ip address|ip addr add|^\s*address|ifconfig\s+\S+)\s+(\d{1,3}(?:\.\d{1,3}){3})\b`)

// Nodes lists a lab's nodes with the addresses the controller observed and
// the ones their configurations name.
func (c *Client) Nodes(ctx context.Context, labID string) ([]Node, error) {
	var raw []struct {
		Node
		Configuration json.RawMessage `json:"configuration"`
	}
	lab := "/labs/" + url.PathEscape(labID)
	if err := c.get(ctx, lab+"/nodes?data=true", &raw); err != nil {
		return nil, err
	}
	var l3 map[string]struct {
		Interfaces map[string]struct {
			IP4 []string `json:"ip4"`
		} `json:"interfaces"`
	}
	if err := c.get(ctx, lab+"/layer3_addresses", &l3); err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(raw))
	for _, r := range raw {
		n := r.Node
		for _, iface := range l3[n.ID].Interfaces {
			for _, s := range iface.IP4 {
				if a, err := netip.ParseAddr(strings.SplitN(s, "/", 2)[0]); err == nil && a.Is4() {
					n.Addresses = append(n.Addresses, a)
				}
			}
		}
		n.Addresses = sortedUnique(n.Addresses)
		for _, m := range configAddress.FindAllStringSubmatch(configText(r.Configuration), -1) {
			if a, err := netip.ParseAddr(m[1]); err == nil && a.Is4() && !a.IsUnspecified() {
				n.ConfiguredAddresses = append(n.ConfiguredAddresses, a)
			}
		}
		n.ConfiguredAddresses = sortedUnique(n.ConfiguredAddresses)
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// configText is a node's configuration as text: CML 2.7 sends one string,
// later releases a list of files.
func configText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var files []struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &files) == nil {
		var b strings.Builder
		for _, f := range files {
			b.WriteString(f.Content)
			b.WriteByte('\n')
		}
		return b.String()
	}
	return ""
}

// sortedUnique sorts addrs and drops repeats.
func sortedUnique(addrs []netip.Addr) []netip.Addr {
	sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
	out := addrs[:0]
	for i, a := range addrs {
		if i == 0 || a != addrs[i-1] {
			out = append(out, a)
		}
	}
	return out
}

// StatusError is an answer outside 2xx.
type StatusError struct {
	Method, Path string
	Status       int
	Body         string
}

// Error names the call and the controller's own words, bounded and quoted.
func (e *StatusError) Error() string {
	return fmt.Sprintf("cml: %s /api/v0%s: %d: %s", e.Method, e.Path, e.Status, firstLine(e.Body))
}

// get reads one JSON answer once logged in.
func (c *Client) get(ctx context.Context, path string, into any) error {
	if c.token == "" {
		return errors.New("cml: Connect first")
	}
	return c.call(ctx, http.MethodGet, path, nil, into)
}

// call sends one request under /api/v0 and decodes a 2xx answer into into.
func (c *Client) call(ctx context.Context, method, path string, body []byte, into any) error {
	u := c.base.String() + "/api/v0" + path
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cml: %s /api/v0%s: %w", method, strings.SplitN(path, "?", 2)[0], err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("cml: %s /api/v0%s: reading the answer: %w", method, path, err)
	}
	if len(out) > maxResponse {
		return fmt.Errorf("cml: %s /api/v0%s: the answer is over %d bytes", method, path, maxResponse)
	}
	if res.StatusCode/100 != 2 {
		return &StatusError{Method: method, Path: path, Status: res.StatusCode, Body: string(out)}
	}
	if err := json.Unmarshal(out, into); err != nil {
		return fmt.Errorf("cml: %s /api/v0%s: the answer is not what this client reads: %w", method, path, err)
	}
	return nil
}

// firstLine is a controller's words fit for an error: the first line, at
// most 300 bytes, quoted so a control character is shown rather than
// acted on.
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
