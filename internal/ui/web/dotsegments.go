// Package web: a request whose path holds a dot segment names nothing here.
package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// refuseDotSegments answers 404 to a request whose path, as it was sent,
// holds a segment that decodes to "." or "..".
//
// No link the UI draws holds one: view.RecordPath withholds the link to a
// record whose id is "." or "..", since a browser resolves such a segment
// however it is escaped. Only a client that sends paths unresolved can ask
// for one, and the page it would get names a record none of whose own links
// (edit, delete, its actions) could be built, so its forms would post to
// the wrong route. Each segment is judged as it was sent, so a record named
// "a/../b", whose one segment is a%2F..%2Fb, is still served.
func (h *Handler) refuseDotSegments(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, seg := range strings.Split(r.URL.EscapedPath(), "/") {
			decoded, err := url.PathUnescape(seg)
			if err == nil && seg != "" && !view.Addressable(decoded) {
				h.notFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
