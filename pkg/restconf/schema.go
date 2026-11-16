// Package restconf: fetching a module's YANG source from the server, as RFC
// 8040 section 3.7 has a client do: from the URL its YANG library names for
// it (RFC 7895's schema leaf, or RFC 8525's location), fetched at the device's
// own origin with its own TLS and credential, never anywhere else. A URL
// naming another scheme, host or port is refused, since fetching it would
// send the device's credential to whoever answers there; one with no host is
// read against the device's origin.
//
// Measured on a Catalyst 8000V (IOS XE 17.15.1a, 2026-11-15): its RFC 8525
// library names no location, and its modules-state names one for every module
// at the address it was reached at, with the default port written out
// (https://10.10.20.178:443/restconf/tailf/modules/...), so the default port
// is read as the origin's own. rousette (2026-11-16, the notconf image) names
// none in either, though it serves sources under /yang/; a client finds no URL
// to fetch there, and says so rather than guessing a path.
package restconf

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// MediaTypeYANG is the media type of YANG source (RFC 6020 section 14).
const MediaTypeYANG = "application/yang"

// acceptSchema asks for YANG source first; IOS XE was measured answering 406
// to an Accept it does not serve and a source to application/yang-data+xml, so
// that is asked for next, and anything after: whatever comes back is held to
// being the module's source (yanglib.CheckSource) before it is kept.
const acceptSchema = MediaTypeYANG + ", " + MediaTypeXML + ";q=0.5, text/plain;q=0.3, */*;q=0.1"

// OffOriginError is a schema location at another origin than the device's,
// which is not fetched: the credential goes to the device's own origin only.
type OffOriginError struct {
	// Location is the URL the library named, quoted and bounded.
	Location string
	// Origin is the device's own origin.
	Origin string
}

// Error says what was refused and why.
func (e *OffOriginError) Error() string {
	return fmt.Sprintf("the device's library names %s for the source, which is not the device's own origin %s: its credential is sent to that origin only, so the source is not fetched (if both name one device, set the device's base URL to the origin it names)", e.Location, e.Origin)
}

// SchemaRef is location as a path on the device's own origin, as the client
// sends it (escaped, with any query): a location with no scheme or host is a
// path there; one naming a scheme, host or port other than the origin's (the
// default port of its scheme being the origin's own) is an *OffOriginError, as
// is one carrying user information. A fragment is dropped, as HTTP never
// sends one. The path is then held to every request's rules (httpapi.Join),
// which refuse a dot segment however it is written.
func (c *Client) SchemaRef(location string) (string, error) {
	quoted := farside.Text(location)
	u, err := url.Parse(strings.TrimSpace(location))
	if err != nil || u.Opaque != "" {
		return "", fmt.Errorf("the device's library names %s for the source, which is not a URL", quoted)
	}
	origin := c.base.Scheme + "://" + c.base.Host
	if u.User != nil {
		return "", &OffOriginError{Location: quoted, Origin: origin}
	}
	// A location with no scheme or host is a path on the origin, rooted
	// there as written: its dot segments are not resolved away here, so the
	// request's own path rules (httpapi.Join) refuse them as they refuse
	// them in any other location.
	if u.Host != "" || u.Scheme != "" {
		if !strings.EqualFold(u.Scheme, c.base.Scheme) || !strings.EqualFold(u.Hostname(), c.base.Hostname()) || portOf(u) != portOf(c.base) {
			return "", &OffOriginError{Location: quoted, Origin: origin}
		}
	}
	ref := u.EscapedPath()
	if !strings.HasPrefix(ref, "/") {
		// A relative path: under the base, where the request joins it.
		ref = "/" + ref
	} else if base := strings.TrimSuffix(c.base.EscapedPath(), "/"); base != "" {
		// A path from the origin's root, which the request would join under
		// the base's own path a second time (/r1/r1/...): it must lie under
		// that path, and is sent relative to it. New refuses a base with a
		// path today; this keeps a location right if one is ever allowed.
		if ref != base && !strings.HasPrefix(ref, base+"/") {
			return "", fmt.Errorf("the device's library names %s for the source, which is outside the device's base path %s", quoted, base)
		}
		ref = "/" + strings.TrimPrefix(strings.TrimPrefix(ref, base), "/")
	}
	if u.RawQuery != "" {
		ref += "?" + u.RawQuery
	}
	return ref, nil
}

// portOf is u's port, its scheme's default when it names none.
func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// GetSchema fetches the source at location (SchemaRef's rules) with the
// device's credential, reading at most limit bytes and refusing a longer
// answer, and returns its body as the device sent it. A status other than 200
// is a *StatusError, masked; a refused credential a *RefusedError.
func (c *Client) GetSchema(ctx context.Context, location string, limit int64) ([]byte, error) {
	ref, err := c.SchemaRef(location)
	if err != nil {
		return nil, err
	}
	// A source may sit outside the root (rousette's /yang/), so its read
	// proves nothing about the credential the root's reads have not.
	a, err := c.get(ctx, ref, acceptSchema, limit, false)
	if err != nil {
		return nil, err
	}
	if a.status != http.StatusOK {
		return nil, c.statusError(a)
	}
	return a.body, nil
}

// StateSchemas reads the schema entries of RFC 7895's modules-state, under
// the root DiscoverRoot found: the resource that names each source's URL on a
// server whose RFC 8525 library names none (IOS XE). A server that does not
// serve it answers a *StatusError.
func (c *Client) StateSchemas(ctx context.Context) ([]yanglib.Schema, error) {
	lib, err := c.readLibrary(ctx, pathModulesState)
	if err != nil {
		return nil, err
	}
	return yanglib.KeepSchemas(lib.Schemas), nil
}
