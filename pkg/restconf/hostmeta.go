// Package restconf: finding the API root (RFC 8040 section 3.1). The client
// reads the origin's /.well-known/host-meta (RFC 6415's XRD document) with no
// credential and takes the href of its one link whose rel is "restconf",
// since a server may put its root anywhere. A root that names another
// scheme, host or port is refused: the credential belongs to the device's own
// origin, and a host-meta document is the device's answer, not a trusted
// setting.
//
// A server that answers host-meta 404 does not end the search there. IOS XE
// 17.15.1a, measured on a Catalyst 8000V on 2026-11-15, serves host-meta only
// to a valid login and answers 404 otherwise (no credential and a wrong one
// alike, with no WWW-Authenticate), while its reads under /restconf answer a
// missing or wrong credential with 401. Sending the credential to host-meta
// to learn the root would send a wrong password twice per try, so on a 404
// the client takes RFC 8040's conventional root, /restconf, provisionally,
// and the first authenticated read under it decides: an answer confirms it,
// a 401 is a refused credential (sent once), and a 404 says no RESTCONF is
// served there (RootDefault).
package restconf

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// hostMetaPath is RFC 6415's well-known location.
const hostMetaPath = "/.well-known/host-meta"

// xrdNamespace is the XRD 1.0 namespace host-meta's root is in.
const xrdNamespace = "http://docs.oasis-open.org/ns/xri/xrd-1.0"

// relRestconf is the link relation naming the RESTCONF root.
const relRestconf = "restconf"

// maxRootBytes bounds a root's href.
const maxRootBytes = 1024

// The ways a root is learned (RootSource).
const (
	// RootHostMeta is a root host-meta's restconf link named.
	RootHostMeta = "host-meta"
	// RootDefault is RFC 8040's conventional root, /restconf, taken when
	// host-meta answered 404, which the first read under it confirms.
	RootDefault = "default"
)

// conventionalRoot is the root RFC 8040's examples and IOS XE use.
const conventionalRoot = "/restconf"

// ErrNoRestconf is a server answering 404 both at host-meta and at the
// first read under the conventional root it was then tried at.
var ErrNoRestconf = errors.New("the device answered host-meta with 404 or a redirect and, under the conventional root /restconf, 404 again: it serves no RESTCONF there")

// DiscoverRoot reads the origin's host-meta, with no credential unless the
// server asks for one (401), and returns the RESTCONF root its restconf link
// names, as an escaped path with no trailing slash ("" for a root at the
// origin itself), which later reads are made under. A host-meta answered 404, or
// with a redirect (never followed), leaves the conventional /restconf as the root, provisionally (RootSource
// says RootDefault), for the first read under it to confirm. Another status,
// a document that is not XRD, one naming no restconf link or two different
// ones, and a root off the device's origin are each an error.
func (c *Client) DiscoverRoot(ctx context.Context) (string, error) {
	a, err := c.getAnonymous(ctx, hostMetaPath, "application/xrd+xml", MaxHostMetaBytes)
	if err == nil && a.status == http.StatusUnauthorized {
		// Asked for, so sent; a refusal of it is a RefusedError.
		a, err = c.get(ctx, hostMetaPath, "application/xrd+xml", MaxHostMetaBytes, false)
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, err)
	}
	switch a.status {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		// A redirect is never followed (the credential must not follow a
		// URL): IOS XE 17.15.1a answers host-meta without a valid login with
		// 302 to its WebUI's /login.html (measured 2026-11-15), which says
		// no more than a 404 does.
		c.root, c.rootFound, c.rootSource = conventionalRoot, true, RootDefault
		return c.root, nil
	default:
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, c.statusError(a))
	}
	href, err := restconfLink(a.body)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, err)
	}
	root, err := resolveRoot(c.base, href)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, err)
	}
	c.root, c.rootFound, c.rootSource = root, true, RootHostMeta
	return root, nil
}

// Root returns the root DiscoverRoot found, and whether it has.
func (c *Client) Root() (string, bool) {
	return c.root, c.rootFound
}

// RootSource says how the root was learned: RootHostMeta or RootDefault,
// empty before DiscoverRoot.
func (c *Client) RootSource() string {
	return c.rootSource
}

// restconfLink is the href of body's one restconf link. Two links naming the
// same href are one; two naming different hrefs are refused, since the
// client could not say which root the device means.
//
// A Link left unclosed is read as closed: rousette, in the notconf image the
// NETCONF and RESTCONF tests run, answers
// <XRD ...><Link rel='restconf' href='/restconf/'></XRD>, measured 2026-11-14,
// which a strict parser refuses as malformed. Only that is relaxed: the
// document must still be one XRD, and the href it names is then held to
// every rule resolveRoot applies.
func restconfLink(body []byte) (string, error) {
	var doc struct {
		XMLName xml.Name
		Links   []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"Link"`
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	dec.AutoClose = []string{"Link"}
	if err := dec.Decode(&doc); err != nil {
		return "", fmt.Errorf("the answer is not an XRD document: %w", err)
	}
	if doc.XMLName.Local != "XRD" || (doc.XMLName.Space != xrdNamespace && doc.XMLName.Space != "") {
		return "", errors.New("the answer is not an XRD document")
	}
	href := ""
	for _, l := range doc.Links {
		if strings.TrimSpace(l.Rel) != relRestconf {
			continue
		}
		h := strings.TrimSpace(l.Href)
		if href != "" && h != href {
			return "", errors.New("the document names two different restconf roots")
		}
		href = h
	}
	if href == "" {
		return "", errors.New("the document names no restconf link: the device may not serve RESTCONF")
	}
	return href, nil
}

// resolveRoot is href, a host-meta root, as an escaped path on base: an
// absolute path, or an absolute URL on base's own scheme, host and port,
// with no query, fragment or user information, no trailing slash, and
// nothing httpapi.Join would refuse (a dot segment, a control character).
func resolveRoot(base *url.URL, href string) (string, error) {
	if len(href) > maxRootBytes || !yanglib.Printable(href) {
		return "", fmt.Errorf("the restconf root is longer than %d bytes or holds a control character", maxRootBytes)
	}
	u, err := url.Parse(href)
	if err != nil {
		return "", errors.New("the restconf root is not a URL")
	}
	switch {
	case u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "":
		return "", errors.New("the restconf root carries a query, a fragment or user information")
	case u.Scheme != "" || u.Host != "":
		if !u.IsAbs() || !httpapi.SameOrigin(base, u) {
			return "", fmt.Errorf("the restconf root %s leaves the device's own origin %s, and is refused", farside.Text(href), base.String())
		}
	case !strings.HasPrefix(u.Path, "/"):
		return "", fmt.Errorf("the restconf root %s is not an absolute path", farside.Text(href))
	}
	root := strings.TrimRight(u.EscapedPath(), "/")
	// The root is checked as every request under it is joined, so a dot
	// segment or an escaped control character is refused here, once.
	probe := root + "/data"
	if _, err := httpapi.Join(base, probe); err != nil {
		return "", fmt.Errorf("the restconf root %s: %w", farside.Text(href), err)
	}
	return root, nil
}
