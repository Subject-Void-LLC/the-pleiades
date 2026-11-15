// Package restconf is an RFC 8040 RESTCONF client: YANG data over HTTPS.
//
// It reaches a device the way every other HTTP API here is reached
// (pkg/httpapi, pkg/devicetls): the device's own base URL, its own TLS
// (always verified, a pinned certificate included), its own stored
// credential, no proxy, and no redirect followed, so the credential reaches
// the device's origin and nowhere else.
//
// Its root is discovered, never assumed: RFC 8040 section 3.1 has a client
// read /.well-known/host-meta and take the root its "restconf" link names,
// and a root that leaves the device's own scheme, host and port is refused.
// Every resource path is built (Path, Segment), each list key escaped on its
// own, never concatenated: a key holding "/" or "," would otherwise address
// another resource.
//
// Every answer is bounded and refused, never cut, past its bound; the
// server's own ietf-restconf:errors come back as a structured error, its
// text masked by pkg/netcfg before any of it is kept.
package restconf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

// The media types RFC 8040 section 11.3 defines for YANG data.
const (
	MediaTypeXML  = "application/yang-data+xml"
	MediaTypeJSON = "application/yang-data+json"
)

// acceptYANG asks for XML first, the encoding the YANG library parser and
// NETCONF share, and takes JSON from a server that serves only it: RFC 8040
// section 5.2 requires a server to support at least one of the two.
const acceptYANG = MediaTypeXML + ", " + MediaTypeJSON + ";q=0.9"

// The bounds on what one answer may be. Measured on a Catalyst 8000V (IOS XE
// 17.15.1a): its YANG library, 559 modules, is about 110 KiB as XML.
const (
	// MaxHostMetaBytes bounds the host-meta document.
	MaxHostMetaBytes = 64 << 10
	// MaxDataBytes bounds one data resource's answer.
	MaxDataBytes = 16 << 20
)

// Client is a RESTCONF client for one device.
type Client struct {
	// base is the device's origin.
	base *url.URL
	// hc sends every request; it follows no redirect.
	hc *http.Client
	// auth is how the credential is sent (httpapi.AuthBasic and the rest).
	auth string
	// secrets is the device's own credential, flattened.
	secrets map[string]string
	// extraSecrets are further values masked in every answer (MaskAlso).
	extraSecrets []string
	// dataAnswered is set once a read under the root was answered with a
	// status that does not refuse the credential, after which a 403 is
	// access control (do's rule).
	dataAnswered bool
	// refusal is the RefusedError that ended this client's use of the
	// credential, which every later request returns unsent.
	refusal error
	// root is the API root's path as escaped, with no trailing slash, once
	// DiscoverRoot has found it; "" before then, and "" for a root at the
	// origin itself, which rootFound tells apart.
	root      string
	rootFound bool
}

// New returns a client for the RESTCONF server at base, an http or https
// origin with no path, query, fragment or user information
// (httpapi.ValidateBaseURL), sending the credential in secrets as auth says.
// allowPlaintext is the device's explicit acceptance of a credential sent
// over http://. hc should follow no redirect: ClientFor's does not.
func New(base string, hc *http.Client, auth string, allowPlaintext bool, secrets map[string]string) (*Client, error) {
	u, err := httpapi.ValidateBaseURL(base, auth, allowPlaintext)
	if err != nil {
		return nil, err
	}
	if u.Path != "" && u.Path != "/" {
		// The root is found from the origin's host-meta, so a path here
		// would be ignored, describing a reach the client does not make.
		return nil, errors.New("the RESTCONF base URL must be an origin with no path: the root is found from its /.well-known/host-meta")
	}
	u.Path, u.RawPath = "", ""
	if hc == nil {
		return nil, errors.New("a RESTCONF client needs an HTTP client")
	}
	return &Client{base: u, hc: hc, auth: auth, secrets: secrets}, nil
}

// Close drops the client's idle connections, so a caller that makes a fresh
// client for each try (wait.restconf) never takes a connection kept open
// from an earlier one as an answer.
func (c *Client) Close() {
	c.hc.CloseIdleConnections()
}

// Verified reports whether the server's identity is verified on every
// request: true over https, where pkg/devicetls always verifies the
// certificate (a chain, a pinned authority or a pinned certificate), and
// false over http://, where nothing proves who answered.
func (c *Client) Verified() bool {
	return c.base.Scheme == "https"
}

// Base returns the device's origin.
func (c *Client) Base() string {
	return c.base.String()
}

// answer is one bounded answer: its status, media type and body, and the
// Location header a POST's answer names its created resource in.
type answer struct {
	status    int
	mediaType string
	body      []byte
	location  string
}

// get sends one authenticated GET of ref, a path on the device's origin as
// escaped, asking for accept, and reads at most limit bytes of the answer.
// data says ref is a read under the root, which proves the credential where
// host-meta (which a server may answer with no login) does not.
func (c *Client) get(ctx context.Context, ref, accept string, limit int64, data bool) (answer, error) {
	return c.do(ctx, http.MethodGet, ref, accept, "", nil, limit, data)
}

// getAnonymous is get sending no credential, and judging no refusal: the
// caller reads the status. host-meta is read this way first, since RFC 8040
// has a client find the root before it authenticates anything, and a
// credential sent where none is asked for is one more place it could be
// echoed or logged.
func (c *Client) getAnonymous(ctx context.Context, ref, accept string, limit int64) (answer, error) {
	return c.send(ctx, http.MethodGet, ref, accept, "", nil, limit, false, true)
}

// do sends one authenticated request and reads at most limit bytes of the
// answer. A transport failure is an error, a certificate nothing trusts named
// by its fingerprint (devicetls.Explain). A refused credential is a
// RefusedError, judged before the answer's size so a refusal with a long body
// still ends the run: a 401 anywhere; a 403 when the device's record sends no
// credential; and a 403 to the first read under the root, since a server may
// answer a wrong password with 403, and a credential is never sent again once
// refused (FAILURE_PATTERNS 1238). Once one read under the root has been
// answered otherwise, a 403 is RFC 8040 section 7's access-denied, access
// control refusing that one resource to a good credential, and is returned
// for the caller to judge with every other status. An answer past limit is
// unusable.
func (c *Client) do(ctx context.Context, method, ref, accept, contentType string, body []byte, limit int64, data bool) (answer, error) {
	return c.send(ctx, method, ref, accept, contentType, body, limit, data, false)
}

// send is do, sending no credential and judging no refusal when anonymous.
func (c *Client) send(ctx context.Context, method, ref, accept, contentType string, body []byte, limit int64, data, anonymous bool) (answer, error) {
	if c.refusal != nil {
		// The credential was refused once; it is not sent again.
		return answer{}, c.refusal
	}
	u, err := httpapi.Join(c.base, ref)
	if err != nil {
		return answer{}, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return answer{}, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if !anonymous {
		if err := httpapi.Authorize(req, c.auth, c.secrets); err != nil {
			return answer{}, err
		}
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return answer{}, devicetls.Explain(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// One byte past the bound is read, so an answer exactly at it is
	// accepted and one over it is known to be over without reading the rest.
	read, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return answer{}, fmt.Errorf("reading the answer to %s %s: %w", method, u.EscapedPath(), err)
	}
	a := answer{status: resp.StatusCode, mediaType: mediaTypeOf(resp.Header.Get("Content-Type")), body: read, location: resp.Header.Get("Location")}
	if !anonymous && c.refuses(a.status, data) {
		if int64(len(a.body)) > limit {
			a.body = nil
		}
		c.refusal = c.refused(a)
		return answer{}, c.refusal
	}
	if data && a.status != http.StatusUnauthorized && a.status != http.StatusForbidden {
		c.dataAnswered = true
	}
	if int64(len(a.body)) > limit {
		return answer{}, fmt.Errorf("%w: the answer to %s %s is larger than %d bytes, which is not read", errUnusable, method, u.EscapedPath(), limit)
	}
	return a, nil
}

// refuses reports whether status refuses the credential (do's rule).
func (c *Client) refuses(status int, data bool) bool {
	switch {
	case status == http.StatusUnauthorized:
		return true
	case status != http.StatusForbidden:
		return false
	case c.auth == httpapi.AuthNone:
		return true
	}
	return data && !c.dataAnswered
}

// refused is the error for an answer refusing the client's credential, or
// asking for one the device's record sends none of.
func (c *Client) refused(a answer) error {
	return &RefusedError{Answer: c.statusError(a), NoneSent: c.auth == httpapi.AuthNone}
}

// RefusedError is a credential the device refused (401, or 403 to the first
// read under the root), or asked for when this device sends none (401 or
// 403). It ends a discovery, where a StatusError refuses one resource, so it
// does not unwrap to one: a caller asking errors.As for a StatusError must
// not mistake a refused login for a resource the server lacks (measured
// 2026-11-14 against rousette, which answers host-meta with no login and
// every data read with 401).
type RefusedError struct {
	// Answer is what the device said, masked.
	Answer *StatusError
	// NoneSent is true when the device's record sends no credential.
	NoneSent bool
}

// Error says the credential was refused, or how to send one.
func (e *RefusedError) Error() string {
	if e.NoneSent {
		return fmt.Sprintf("the device asked for a credential (%d), and this device sends none: set its http_auth property to basic or bearer to send its stored credential: %s", e.Answer.Status, e.Answer.Error())
	}
	return "the device refused the credential: " + e.Answer.Error()
}

// mediaTypeOf is a Content-Type header's media type, lower case and without
// parameters, or "" when it does not parse.
func mediaTypeOf(header string) string {
	mt, _, err := mime.ParseMediaType(header)
	if err != nil {
		return ""
	}
	return strings.ToLower(mt)
}

// isJSON reports whether an answer is JSON: by its media type when it names
// one, otherwise by its first byte that is not white space.
func (a answer) isJSON() bool {
	switch a.mediaType {
	case MediaTypeJSON, "application/json":
		return true
	case MediaTypeXML, "application/xml", "text/xml":
		return false
	}
	trimmed := strings.TrimLeft(string(a.body), " \t\r\n")
	return strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")
}

// GetData reads the data resource at p under the root's /data, which
// DiscoverRoot must have found, as XML or JSON, whichever the server sends
// (XML asked for first). A status other than 200 is a *StatusError carrying
// the server's ietf-restconf:errors.
func (c *Client) GetData(ctx context.Context, p Path) (body []byte, mediaType string, err error) {
	ref, err := c.dataRef(p)
	if err != nil {
		return nil, "", err
	}
	a, err := c.get(ctx, ref, acceptYANG, MaxDataBytes, true)
	if err != nil {
		return nil, "", err
	}
	if a.status != http.StatusOK {
		return nil, "", c.statusError(a)
	}
	mediaType = MediaTypeXML
	if a.isJSON() {
		mediaType = MediaTypeJSON
	}
	return a.body, mediaType, nil
}

// dataRef is the escaped path of p under the root's /data.
func (c *Client) dataRef(p Path) (string, error) {
	if !c.rootFound {
		return "", errors.New("the RESTCONF root has not been discovered")
	}
	escaped, err := p.Escaped()
	if err != nil {
		return "", err
	}
	ref := c.root + "/data"
	if escaped != "" {
		ref += "/" + escaped
	}
	return ref, nil
}
