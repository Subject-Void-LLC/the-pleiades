// Package view: the one way a view builds the URL of a record.
//
// Every link to a record, a record's page or a row of one of its sections
// goes through RecordPath or RowPath, so the rule about which ids can be a
// URL path segment lives here and nowhere else.
package view

import (
	"net/url"
	"path"
)

// Addressable reports whether id can be a URL path segment naming a record.
//
// url.PathEscape leaves "." and ".." as they are, and path.Join then
// resolves them: a record named ".." would link to the page above its own
// list, and one named "." to the list. Every browser resolves them too, and
// does the same with "%2E" and "%2E%2E", so no escaping makes such an id
// reachable. An empty id has no segment at all. A record whose id is one of
// these three is listed without a link, and the handler answers a request
// naming one with 404.
func Addressable(id string) bool {
	return id != "" && id != "." && id != ".."
}

// RecordPath is the URL of record id of resource under prefix, followed by
// tail's literal segments (edit, logs, an action's registered name), or ""
// when id is not Addressable. id is escaped as one segment, so a "/" in it
// is never a second one.
func RecordPath(prefix, resource, id string, tail ...string) string {
	seg, ok := segment(id)
	if !ok {
		return ""
	}
	return path.Join(append([]string{prefix, resource, seg}, tail...)...)
}

// RowPath is the URL of one row's action on a record: the record's action
// path, then the row's own id as one more segment, or "" when either id is
// not Addressable.
func RowPath(prefix, resource, id, action, row string) string {
	base := RecordPath(prefix, resource, id, action)
	seg, ok := segment(row)
	if base == "" || !ok {
		return ""
	}
	return base + "/" + seg
}

// segment is id escaped as one URL path segment, and false when it is not
// Addressable. It is the only place this package and internal/ui/web escape
// an id into a path, which TestNothingElseEscapesAnIDIntoAPath holds true.
func segment(id string) (string, bool) {
	if !Addressable(id) {
		return "", false
	}
	return url.PathEscape(id), true
}
