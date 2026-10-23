// Package batfish is a client for Batfish's REST API (v2): create a
// network, upload a snapshot, parse it, fork it with interfaces or nodes
// deactivated, and ask it questions. It speaks the API pybatfish speaks,
// traced against Batfish 2026.08.27.3685 (Phase 127b's spike 1), so no
// Python is needed to drive the model.
//
// Batfish has no authentication. Its API key header is sent with the
// default value pybatfish sends; the service must be reached only where
// that is acceptable, which the batfish device type decides.
package batfish

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The headers every request carries.
const (
	apiKeyHeader  = "X-Batfish-Apikey"
	versionHeader = "X-Batfish-Version"
	// defaultAPIKey is the key pybatfish sends when none is set; Batfish
	// does not check it.
	defaultAPIKey = "00000000000000000000000000000000"
	// clientVersion is the client version this client claims, the
	// pybatfish release whose traffic it follows.
	clientVersion = "2026.09.17.3748"
)

// maxResponse bounds a response body read: an answer of every route in a
// large network is megabytes, and nothing Batfish says should be larger.
const maxResponse = 64 << 20

// pollInterval is how often a work item is polled.
const pollInterval = 250 * time.Millisecond

// Client talks to one Batfish service.
type Client struct {
	base *url.URL
	http *http.Client

	templatesOnce sync.Once
	templates     map[string]string
	templatesErr  error
}

// New returns a client for the service at base (http:// or https://),
// sending its requests through hc.
func New(base string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("batfish: %q is not an http or https base URL", base)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{base: u, http: hc}, nil
}

// do sends one request and returns the body of a 2xx answer, or an error
// carrying Batfish's own words.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) ([]byte, error) {
	u := *c.base
	u.Path += path
	u.RawQuery = query.Encode()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), r)
	if err != nil {
		return nil, err
	}
	req.Header.Set(apiKeyHeader, defaultAPIKey)
	req.Header.Set(versionHeader, clientVersion)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("batfish: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	out, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("batfish: %s %s: reading the answer: %w", method, path, err)
	}
	if len(out) > maxResponse {
		return nil, fmt.Errorf("batfish: %s %s: the answer is over %d bytes", method, path, maxResponse)
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("batfish: %s %s: %s: %s", method, path, res.Status, firstLine(string(out)))
	}
	return out, nil
}

// Version returns the service's version, from GET /v2/version.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.do(ctx, http.MethodGet, "/v2/version", nil, nil, "")
	if err != nil {
		return "", err
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		return "", fmt.Errorf("batfish: /v2/version is not JSON: %w", err)
	}
	if s, ok := v["batfish"].(string); ok {
		return s, nil
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateNetwork creates a network to hold snapshots.
func (c *Client) CreateNetwork(ctx context.Context, network string) error {
	_, err := c.do(ctx, http.MethodPost, "/v2/networks", url.Values{"name": {network}}, nil, "")
	return err
}

// DeleteNetwork removes a network and every snapshot in it.
func (c *Client) DeleteNetwork(ctx context.Context, network string) error {
	_, err := c.do(ctx, http.MethodDelete, "/v2/networks/"+url.PathEscape(network), nil, nil, "")
	return err
}

// UploadSnapshot uploads a snapshot as a zip archive (Snapshot builds one)
// and parses it, returning once Batfish has finished.
func (c *Client) UploadSnapshot(ctx context.Context, network, snapshot string, zip []byte) error {
	path := "/v2/networks/" + url.PathEscape(network) + "/snapshots/" + url.PathEscape(snapshot)
	if _, err := c.do(ctx, http.MethodPost, path, nil, zip, "application/octet-stream"); err != nil {
		return err
	}
	return c.parse(ctx, network, snapshot)
}

// Interface names one interface of one node.
type Interface struct {
	Hostname  string `json:"hostname"`
	Interface string `json:"interface"`
}

// Fork makes snapshot from base with the interfaces and nodes given
// deactivated, and parses it: a scenario, such as a link failing.
func (c *Client) Fork(ctx context.Context, network, base, snapshot string, interfaces []Interface, nodes []string) error {
	body, err := json.Marshal(map[string]any{
		"snapshotBase": base, "snapshotNew": snapshot,
		"deactivateInterfaces": interfaces, "deactivateNodes": nodes,
		"restoreInterfaces": nil, "restoreNodes": nil, "zipFile": nil,
	})
	if err != nil {
		return err
	}
	if _, err := c.do(ctx, http.MethodPost, "/v2/networks/"+url.PathEscape(network)+"/snapshots:fork", nil, body, "application/json"); err != nil {
		return err
	}
	return c.parse(ctx, network, snapshot)
}

// parse runs the work item that parses a snapshot.
func (c *Client) parse(ctx context.Context, network, snapshot string) error {
	return c.work(ctx, network, snapshot, map[string]string{"testrig": snapshot, "si": "", "sv": "", "initinfo": ""})
}

// The work item statuses that end one.
var (
	workDone   = map[string]bool{"TERMINATEDNORMALLY": true}
	workFailed = map[string]bool{"TERMINATEDABNORMALLY": true, "TERMINATEDBYUSER": true, "ASSIGNMENTERROR": true, "REQUEUEFAILURE": true}
)

// work queues a work item and polls it until it ends.
func (c *Client) work(ctx context.Context, network, snapshot string, params map[string]string) error {
	// Batfish reads a work item's id as a UUID.
	id := uuid.NewString()
	body, err := json.Marshal(map[string]any{"containerName": network, "id": id, "requestParams": params, "testrigName": snapshot})
	if err != nil {
		return err
	}
	path := "/v2/networks/" + url.PathEscape(network) + "/work"
	if _, err := c.do(ctx, http.MethodPost, path, nil, body, "application/json"); err != nil {
		return err
	}
	for {
		out, err := c.do(ctx, http.MethodGet, path+"/"+id, nil, nil, "")
		if err != nil {
			return err
		}
		var status struct {
			Code string          `json:"workstatuscode"`
			Task json.RawMessage `json:"task"`
		}
		if err := json.Unmarshal(out, &status); err != nil {
			return fmt.Errorf("batfish: a work item's status is not JSON: %w", err)
		}
		switch {
		case workDone[status.Code]:
			return nil
		case workFailed[status.Code]:
			return fmt.Errorf("batfish: work on %s ended %s", snapshot, status.Code)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// Table is one question's answer: its column names and its rows, each
// cell decoded from JSON.
type Table struct {
	Columns []string
	Rows    []map[string]any
}

// Ask asks question (a template name, e.g. "routes") of snapshot, with
// the template's variables set to vars, and returns the answer's table.
// An answer whose status is not SUCCESS is an error.
func (c *Client) Ask(ctx context.Context, network, snapshot, question string, vars map[string]any) (Table, error) {
	tmpl, err := c.template(ctx, question)
	if err != nil {
		return Table{}, err
	}
	var q map[string]any
	if err := json.Unmarshal([]byte(tmpl), &q); err != nil {
		return Table{}, fmt.Errorf("batfish: the %s template is not JSON: %w", question, err)
	}
	instance, _ := q["instance"].(map[string]any)
	if instance == nil {
		return Table{}, fmt.Errorf("batfish: the %s template has no instance", question)
	}
	id, err := newID()
	if err != nil {
		return Table{}, err
	}
	name := "__pleiades_" + question + "_" + id
	instance["instanceName"] = name
	variables, _ := instance["variables"].(map[string]any)
	for k, v := range vars {
		variable, ok := variables[k].(map[string]any)
		if !ok {
			return Table{}, fmt.Errorf("batfish: %s takes no variable %q", question, k)
		}
		variable["value"] = v
	}
	body, err := json.Marshal(q)
	if err != nil {
		return Table{}, err
	}
	qpath := "/v2/networks/" + url.PathEscape(network) + "/questions/" + url.PathEscape(name)
	if _, err := c.do(ctx, http.MethodPut, qpath, nil, body, "application/octet-stream"); err != nil {
		return Table{}, err
	}
	if err := c.work(ctx, network, snapshot, map[string]string{"answer": "", "questionname": name, "testrig": snapshot}); err != nil {
		return Table{}, err
	}
	out, err := c.do(ctx, http.MethodGet, qpath+"/answer", url.Values{"snapshot": {snapshot}}, nil, "")
	if err != nil {
		return Table{}, err
	}
	return decodeAnswer(question, out)
}

// decodeAnswer reads an answer's first table element.
func decodeAnswer(question string, out []byte) (Table, error) {
	var answer struct {
		Status   string `json:"status"`
		Summary  any    `json:"summary"`
		Elements []struct {
			Metadata struct {
				Columns []struct {
					Name string `json:"name"`
				} `json:"columnMetadata"`
			} `json:"metadata"`
			Rows []map[string]any `json:"rows"`
		} `json:"answerElements"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return Table{}, fmt.Errorf("batfish: the answer to %s is not JSON: %w", question, err)
	}
	if answer.Status != "SUCCESS" {
		return Table{}, fmt.Errorf("batfish: %s was answered %q: %v", question, answer.Status, answer.Summary)
	}
	if len(answer.Elements) == 0 {
		return Table{}, fmt.Errorf("batfish: the answer to %s holds no table", question)
	}
	el := answer.Elements[0]
	t := Table{Rows: el.Rows}
	for _, col := range el.Metadata.Columns {
		t.Columns = append(t.Columns, col.Name)
	}
	if t.Rows == nil {
		t.Rows = []map[string]any{}
	}
	return t, nil
}

// template returns the server's own template for question.
func (c *Client) template(ctx context.Context, question string) (string, error) {
	c.templatesOnce.Do(func() {
		out, err := c.do(ctx, http.MethodGet, "/v2/question_templates", url.Values{"verbose": {"false"}}, nil, "")
		if err != nil {
			c.templatesErr = err
			return
		}
		if err := json.Unmarshal(out, &c.templates); err != nil {
			c.templatesErr = fmt.Errorf("batfish: the question templates are not JSON: %w", err)
		}
	})
	if c.templatesErr != nil {
		return "", c.templatesErr
	}
	t, ok := c.templates[strings.ToLower(question)]
	if !ok {
		return "", fmt.Errorf("batfish: the service has no question %q", question)
	}
	return t, nil
}

// NewNetworkName returns a network name no other run uses.
func NewNetworkName() (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	return "pleiades-" + id, nil
}

// newID returns 16 random hex characters.
func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("batfish: reading randomness: " + err.Error())
	}
	return hex.EncodeToString(b), nil
}

// firstLine returns s's first non-empty line, trimmed and bounded.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			if len(t) > 300 {
				t = t[:300]
			}
			return t
		}
	}
	return ""
}
