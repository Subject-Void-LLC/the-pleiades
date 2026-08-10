package apispec

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
)

// Routes pairs every declared Endpoint with the real handler that serves
// it, refusing to build a route table if the two sets disagree in either
// direction.
//
// This closes the set-membership hole this package's own doc comment
// documented rather than implied away: cmd/controller used to name each
// Endpoint one at a time in a hand-written Routes literal, so an Endpoint
// added to Endpoints and never registered there built clean, vetted
// clean, and tripped no test, while the generated OpenAPI document
// advertised a route the server did not serve.
//
// Both directions are checked, and the second is not symmetry for its own
// sake. An Endpoint with no handler is a 404 the documentation promises.
// A handler with no Endpoint is the opposite mistake -- a route that
// exists, enforces a scope, and is invisible to every generated
// description of this API -- and it is the more dangerous of the two,
// because nothing about it looks wrong from the outside.
//
// The returned slice is in Endpoints order, so cmd/controller mounts
// routes in the same order the generated document lists them.
func Routes(handlers map[string]http.HandlerFunc) ([]api.Route, error) {
	routes := make([]api.Route, 0, len(Endpoints))
	var missing []string

	for _, e := range Endpoints {
		h, ok := handlers[e.Name]
		if !ok || h == nil {
			missing = append(missing, e.Name)
			continue
		}
		routes = append(routes, e.Route(h))
	}

	var unknown []string
	declared := make(map[string]bool, len(Endpoints))
	for _, e := range Endpoints {
		declared[e.Name] = true
	}
	for name := range handlers {
		if !declared[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)

	switch {
	case len(missing) > 0 && len(unknown) > 0:
		return nil, fmt.Errorf("endpoints with no handler: %s; handlers with no endpoint: %s",
			strings.Join(missing, ", "), strings.Join(unknown, ", "))
	case len(missing) > 0:
		return nil, fmt.Errorf("endpoints with no handler: %s", strings.Join(missing, ", "))
	case len(unknown) > 0:
		return nil, fmt.Errorf("handlers with no endpoint: %s", strings.Join(unknown, ", "))
	}

	return routes, nil
}
