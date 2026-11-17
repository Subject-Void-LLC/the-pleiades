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
// resource the server says it does not have (404), or holds nothing of
// (204), is found false with no error; any other status but 200 is a *StatusError.
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
	case http.StatusNotFound, http.StatusNoContent:
		// 204 is a resource the schema has and no instance of: IOS XE
		// answers an NMDA operational read with nothing in it so
		// (measured 2026-11-15).
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
	// Resent is how many times the write was sent again after the server
	// refused it for too many sessions before this answer (sessions.go).
	Resent int
}

// Write sends method to the resource p in datastore with body in mediaType
// (none for DELETE). A status outside 2xx is a *StatusError, the server's
// errors masked; a refused credential a RefusedError; a write whose answer
// never came whole an *AnswerLostError.
func (c *Client) Write(ctx context.Context, method, datastore string, p Path, mediaType string, body []byte) (Written, error) {
	return c.WriteThen(ctx, method, datastore, p, mediaType, body, nil)
}

// WriteThen is Write, calling answered (when not nil) with the answer's
// status and a post's Location as soon as a 2xx status line arrives, before
// its body is read: a caller recording the undo of a create records it there,
// so it stands whatever happens to the rest of the answer. An error answered
// returns is the call's.
func (c *Client) WriteThen(ctx context.Context, method, datastore string, p Path, mediaType string, body []byte, answered func(Written) error) (Written, error) {
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
	var headers func(int, string) error
	if answered != nil {
		headers = func(status int, location string) error {
			if status < 200 || status > 299 {
				return nil
			}
			return answered(c.written(method, datastore, status, location))
		}
	}
	a, err := c.do(ctx, method, ref, mediaType, contentType, body, MaxDataBytes, true, headers)
	if err != nil {
		return Written{}, err
	}
	if a.status < 200 || a.status > 299 {
		return Written{}, c.statusError(a)
	}
	w := c.written(method, datastore, a.status, a.location)
	w.Resent = a.resent
	return w, nil
}

// written is a write's answer as Written.
func (c *Client) written(method, datastore string, status int, location string) Written {
	w := Written{Status: status}
	if method == MethodPost && location != "" {
		w.Location = c.locationPath(location, datastore)
	}
	return w
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
