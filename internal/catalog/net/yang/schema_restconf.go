// Package yang: the RESTCONF source of net.yang.schema, the device's own
// client (pkg/restconf.ClientForContext: its base URL, its TLS, its
// credential, no proxy, no redirect followed) fetching the URL the device's
// YANG library names for each source, at the device's own origin only.
package yang

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// restconfSource fetches sources through one RESTCONF client.
type restconfSource struct {
	client *restconf.Client
	device string
	// said are the client's own warnings (a weakened TLS setting, a
	// credential over http://).
	said []string
}

// openRESTCONF builds device's client.
func openRESTCONF(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem) (source, error) {
	client, warnings, err := restconf.ClientForContext(ctx, device, device.Name(), rc.InjectSecrets())
	if err != nil {
		return nil, err
	}
	s := &restconfSource{client: client, device: device.Name(), said: warnings}
	if !client.Verified() {
		s.said = append(s.said, "the RESTCONF server's identity is not verified over http://, so the sources saved may not be the device's")
	}
	return s, nil
}

// list discovers the device and returns its library's schema entries. A
// server whose RFC 8525 library names no source's URL (IOS XE 17.15's names
// none) is asked for RFC 7895's modules-state, which names one for each,
// whose entries are then the listing; a server that does not serve it leaves
// the library's own, each refused at fetch for naming no URL.
func (s *restconfSource) list(ctx context.Context) (listing, error) {
	d, err := s.client.Discover(ctx)
	if err != nil {
		return listing{}, err
	}
	l := listing{entries: d.Schemas, completeness: d.Completeness, libraryError: d.LibraryError}
	if namesLocation(l.entries) {
		return l, nil
	}
	state, err := s.client.StateSchemas(ctx)
	var status *restconf.StatusError
	switch {
	case errors.As(err, &status) && (status.Status == http.StatusNotFound || status.Status == http.StatusBadRequest):
		// No modules-state (RFC 8040 section 7: a resource the server does
		// not have): the library's entries stand, naming no URL. Any other
		// refusal (a 403, a 500) is the task's failure, below.
		return l, nil
	case err != nil:
		return listing{}, fmt.Errorf("reading modules-state for the sources' URLs: %w", err)
	case len(state) > 0:
		l.entries = state
	}
	return l, nil
}

// namesLocation reports whether any entry, or a submodule of one, names a
// location.
func namesLocation(entries []yanglib.Schema) bool {
	for _, e := range entries {
		if len(e.Locations) > 0 || namesLocation(e.Submodules) {
			return true
		}
	}
	return false
}

// fetch fetches e from the first of its locations at the device's own
// origin. An entry naming none, or only locations elsewhere, is refused
// naming why; no path is guessed.
func (s *restconfSource) fetch(ctx context.Context, e yanglib.Schema, limit int) ([]byte, error) {
	if len(e.Locations) == 0 {
		return nil, fmt.Errorf("device %q's YANG library names no location for %s's source, which RFC 8040 section 3.7 has a server serving sources name: fetch it over NETCONF with %s: %s, if the device serves it", s.device, e.Key(), paramVia, viaNETCONF)
	}
	var refused error
	for _, location := range e.Locations {
		if _, err := s.client.SchemaRef(location); err != nil {
			if refused == nil {
				refused = err
			}
			continue
		}
		return s.client.GetSchema(ctx, location, int64(limit))
	}
	return nil, refused
}

// warnings are the client's: its record's, and how long it waited for the
// device's sessions when the device refused requests for too many of them
// (IOS XE counts each request as a session; pkg/restconf waits them out).
func (s *restconfSource) warnings() []string {
	return append(append([]string(nil), s.said...), s.client.SessionWarnings(s.device)...)
}

// close drops the client's idle connections.
func (s *restconfSource) close() {
	s.client.Close()
}
