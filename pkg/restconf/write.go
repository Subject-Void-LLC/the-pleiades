// Package restconf: reading one resource in a chosen encoding, and writing
// one (RFC 8040 section 4: POST, PUT, PATCH, DELETE), in the conventional
// {+restconf}/data datastore or an NMDA datastore RFC 8527 addresses under
// {+restconf}/ds/.
package restconf

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// Write methods, as RFC 8040 section 4 names them.
const (
	MethodPost   = http.MethodPost
	MethodPut    = http.MethodPut
	MethodPatch  = http.MethodPatch
	MethodDelete = http.MethodDelete
)

// MaxWriteBytes bounds a document sent.
const MaxWriteBytes = 16 << 20

// datastoresPrefix is RFC 8527's datastore resources' module.
const datastoresPrefix = "ietf-datastores:"

// resourceRef is the escaped path of p under the root: under /data when
// datastore is empty, and under /ds/ietf-datastores:<datastore> otherwise.
func (c *Client) resourceRef(datastore string, p Path) (string, error) {
	if datastore == "" {
		return c.dataRef(p)
	}
	if !c.rootFound {
		return "", errors.New("the RESTCONF root has not been discovered")
	}
	if !yanglib.IsIdentifier(datastore) {
		return "", fmt.Errorf("datastore %q is not a YANG identifier", datastore)
	}
	escaped, err := p.Escaped()
	if err != nil {
		return "", err
	}
	ref := c.root + "/ds/" + datastoresPrefix + datastore
	if escaped != "" {
		ref += "/" + escaped
	}
	return ref, nil
}

// Read reads the resource p in datastore (empty for /data) as mediaType
// (MediaTypeJSON or MediaTypeXML) and nothing else, so two reads compare. A
// resource the server says it does not have (404) is found false with no
// error; any other status but 200 is a *StatusError.
func (c *Client) Read(ctx context.Context, datastore string, p Path, mediaType string) (body []byte, found bool, err error) {
	ref, err := c.resourceRef(datastore, p)
	if err != nil {
		return nil, false, err
	}
	a, err := c.get(ctx, ref, mediaType, MaxDataBytes, true)
	if err != nil {
		return nil, false, err
	}
	switch a.status {
	case http.StatusOK:
		return a.body, true, nil
	case http.StatusNotFound:
		return nil, false, nil
	}
	return nil, false, c.statusError(a)
}

// Written is what a write's answer said.
type Written struct {
	// Status is the answer's status, a 2xx.
	Status int
	// Location is a POST's created resource as a path under the datastore
	// written, nil when the server named none or one this client could not
	// read as such.
	Location Path
}

// Write sends method to the resource p in datastore with body in mediaType
// (none for DELETE). A status outside 2xx is a *StatusError, the server's
// errors masked; a refused credential a RefusedError.
func (c *Client) Write(ctx context.Context, method, datastore string, p Path, mediaType string, body []byte) (Written, error) {
	switch method {
	case MethodPost, MethodPut, MethodPatch, MethodDelete:
	default:
		return Written{}, fmt.Errorf("%q is not a RESTCONF write method", method)
	}
	if len(body) > MaxWriteBytes {
		return Written{}, fmt.Errorf("the document is larger than %d bytes", MaxWriteBytes)
	}
	ref, err := c.resourceRef(datastore, p)
	if err != nil {
		return Written{}, err
	}
	contentType := mediaType
	if method == MethodDelete {
		contentType, body = "", nil
	}
	a, err := c.do(ctx, method, ref, mediaType, contentType, body, MaxDataBytes, true)
	if err != nil {
		return Written{}, err
	}
	if a.status < 200 || a.status > 299 {
		return Written{}, c.statusError(a)
	}
	w := Written{Status: a.status}
	if method == MethodPost && a.location != "" {
		w.Location = c.locationPath(a.location, datastore)
	}
	return w, nil
}

// locationPath is a created resource's Location as a path under datastore's
// resource, or nil when it is not one: a URL on another origin, outside the
// root, or not an api-path this client parses.
func (c *Client) locationPath(location, datastore string) Path {
	u, err := url.Parse(location)
	if err != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil
	}
	if u.IsAbs() || u.Host != "" {
		if !httpapi.SameOrigin(c.base, u) {
			return nil
		}
	}
	prefix, err := c.resourceRef(datastore, nil)
	if err != nil {
		return nil
	}
	rest, ok := strings.CutPrefix(u.EscapedPath(), prefix+"/")
	if !ok {
		return nil
	}
	p, err := ParsePath(rest)
	if err != nil {
		return nil
	}
	return p
}
