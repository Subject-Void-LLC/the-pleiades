// Package web: a request whose path holds a dot segment, or an empty one
// where a record id goes, names nothing here.
package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// refuseDotSegments answers 404 to a request whose path, as it was sent,
// holds a segment that decodes to "." or "..", or an empty segment anywhere
// but at the start or the end.
//
// No link the UI draws holds one: view.RecordPath withholds the link to a
// record whose id is "", "." or "..", since a browser resolves a dot
// segment however it is escaped. Only a client that sends paths unresolved
// can ask for one, and the page it would get names a record none of whose
// own links (edit, delete, its actions) can be built, so its forms would
// post to the page itself. An empty segment is how such a client names the
// empty id ("/ui/devices//edit"), which chi routes as {id} = "".
//
// It runs before authentication, and its answer is the plain 404 notFound
// gives everywhere, which needs no session and says nothing about any
// record.
//
// Each segment is judged as it was sent, segment by segment as chi routes
// it, so a%2F..%2Fb, one segment holding dots between escaped slashes, is
// passed on. httpapi.Join refuses that same segment on the way out: a
// request sent to a device cannot know whether the far server decodes %2F
// before it resolves dot segments, while here the router that receives it
// is this one.
func (h *Handler) refuseDotSegments(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The path as sent (api.SentPath), which chi splits, rather than
		// EscapedPath, which re-escapes a path holding a raw | and so
		// splits its escaped slashes.
		segs := strings.Split(api.SentPath(r.URL), "/")
		for i, seg := range segs {
			if seg == "" {
				if i != 0 && i != len(segs)-1 {
					h.notFound(w, r)
					return
				}
				continue
			}
			if decoded, err := url.PathUnescape(seg); err == nil && !recordname.Addressable(decoded) {
				h.notFound(w, r)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
