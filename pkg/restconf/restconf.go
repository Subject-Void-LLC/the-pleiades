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

// answer is one bounded answer: its status, media type and body.
type answer struct {
	status    int
	mediaType string
	body      []byte
}

// get sends one authenticated GET of ref, a path on the device's origin as
// escaped, asking for accept, and reads at most limit bytes of the answer.
// A transport failure is an error, a certificate nothing trusts named by its
// fingerprint (devicetls.Explain); so is a credential the server refused
// (401, or 403 when this device sends none) and an answer past limit. Any
// other status is returned for the caller to judge, a 403 to an
// authenticated client among them: RFC 8040 section 7 answers access-denied,
// access control refusing one resource, with 403, and the credential is good.
func (c *Client) get(ctx context.Context, ref, accept string, limit int64) (answer, error) {
	u, err := httpapi.Join(c.base, ref)
	if err != nil {
		return answer{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return answer{}, err
	}
	req.Header.Set("Accept", accept)
	if err := httpapi.Authorize(req, c.auth, c.secrets); err != nil {
		return answer{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return answer{}, devicetls.Explain(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// One byte past the bound is read, so an answer exactly at it is
	// accepted and one over it is known to be over without reading the rest.
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return answer{}, fmt.Errorf("reading the answer to GET %s: %w", u.EscapedPath(), err)
	}
	if int64(len(body)) > limit {
		return answer{}, fmt.Errorf("%w: the answer to GET %s is larger than %d bytes, which is not read", errUnusable, u.EscapedPath(), limit)
	}
	a := answer{status: resp.StatusCode, mediaType: mediaTypeOf(resp.Header.Get("Content-Type")), body: body}
	if a.status == http.StatusUnauthorized || (a.status == http.StatusForbidden && c.auth == httpapi.AuthNone) {
		return answer{}, c.refused(a)
	}
	return a, nil
}

// refused is the error for an answer refusing the client's credential, or
// asking for one the device's record sends none of.
func (c *Client) refused(a answer) error {
	said := statusError(a)
	if c.auth == httpapi.AuthNone {
		return fmt.Errorf("the device asked for a credential (%d), and this device sends none: set its http_auth property to basic or bearer to send its stored credential: %w", a.status, said)
	}
	return fmt.Errorf("the device refused the credential: %w", said)
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
	a, err := c.get(ctx, ref, acceptYANG, MaxDataBytes)
	if err != nil {
		return nil, "", err
	}
	if a.status != http.StatusOK {
		return nil, "", statusError(a)
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
