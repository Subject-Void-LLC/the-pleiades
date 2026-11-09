// Package web: the one way a UI handler reads a value the router recorded.
package web

import (
	"net/http"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// param is the route parameter key of r, decoded once by api.PathParam, the
// one place a value chi routed is read. chi records a value still escaped
// whenever the request has a RawPath, so the page for the device "sw/1",
// linked as /ui/devices/sw%2F1, would otherwise look up the record named
// "sw%2F1".
//
// An escape that does not decode is answered here with 400, for the reason
// api.PathParam gives: a 404 would say no such record exists, which only a
// lookup may say. The value is never echoed.
func (h *Handler) param(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	value, err := api.PathParam(r, key)
	if err != nil {
		http.Error(w, "malformed path", http.StatusBadRequest)
		return "", false
	}
	return value, true
}

// resourceOf resolves the {resource} path parameter to a descriptor,
// answering the caller itself when it cannot: 400 for an escape that does
// not decode, 404 for a name no view is registered under.
//
// The parameter reaches a registry lookup and nothing else -- never a
// filesystem path, never a query fragment -- so an unregistered name is a
// map miss and a 404 rather than a traversal question.
func (h *Handler) resourceOf(w http.ResponseWriter, r *http.Request) (view.Descriptor, bool) {
	name, ok := h.param(w, r, "resource")
	if !ok {
		return view.Descriptor{}, false
	}
	d, found := view.Lookup(name)
	if !found {
		h.notFound(w, r)
		return view.Descriptor{}, false
	}
	return d, true
}
