// Package restconf: finding the API root (RFC 8040 section 3.1). The client
// reads the origin's /.well-known/host-meta (RFC 6415's XRD document) and
// takes the href of its one link whose rel is "restconf", rather than
// assuming /restconf, since a server may put its root anywhere. A root that
// names another scheme, host or port is refused: the credential belongs to
// the device's own origin, and a host-meta document is the device's answer,
// not a trusted setting.
package restconf

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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

// DiscoverRoot reads the origin's host-meta and returns the RESTCONF root
// its restconf link names, as an escaped path with no trailing slash ("" for
// a root at the origin itself), which later reads are made under. A missing
// document, one that is not XRD, one naming no restconf link or two
// different ones, and a root off the device's origin are each an error.
func (c *Client) DiscoverRoot(ctx context.Context) (string, error) {
	a, err := c.get(ctx, hostMetaPath, "application/xrd+xml", MaxHostMetaBytes)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, err)
	}
	if a.status != http.StatusOK {
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, statusError(a))
	}
	href, err := restconfLink(a.body)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, err)
	}
	root, err := resolveRoot(c.base, href)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", hostMetaPath, err)
	}
	c.root, c.rootFound = root, true
	return root, nil
}

// Root returns the root DiscoverRoot found, and whether it has.
func (c *Client) Root() (string, bool) {
	return c.root, c.rootFound
}

// restconfLink is the href of body's one restconf link. Two links naming the
// same href are one; two naming different hrefs are refused, since the
// client could not say which root the device means.
func restconfLink(body []byte) (string, error) {
	var doc struct {
		XMLName xml.Name
		Links   []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
		} `xml:"Link"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
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
			return "", fmt.Errorf("the restconf root %q leaves the device's own origin %s, and is refused", href, base.String())
		}
	case !strings.HasPrefix(u.Path, "/"):
		return "", fmt.Errorf("the restconf root %q is not an absolute path", href)
	}
	root := strings.TrimRight(u.EscapedPath(), "/")
	// The root is checked as every request under it is joined, so a dot
	// segment or an escaped control character is refused here, once.
	probe := root + "/data"
	if _, err := httpapi.Join(base, probe); err != nil {
		return "", fmt.Errorf("the restconf root %q: %w", href, err)
	}
	return root, nil
}
