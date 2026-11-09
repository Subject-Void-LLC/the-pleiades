// Package view: the one way a view builds the URL of a record.
//
// Every link to a record, a record's page or a row of one of its sections
// goes through RecordPath or RowPath, so ids are escaped here and nowhere
// else. Which ids can be a URL path segment at all is pkg/recordname's
// rule, shared with every write that chooses a device's name.
package view

import (
	"net/url"
	"path"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/recordname"
)

// RecordPath is the URL of record id of resource under prefix, followed by
// tail's literal segments (edit, logs, an action's registered name), or ""
// when id is not addressable (recordname.Addressable). id is escaped as one
// segment, so a "/" in it is never a second one.
//
// url.PathEscape leaves "." and ".." as they are, and path.Join then
// resolves them: a record named ".." would link to the page above its own
// list, and one named "." to the list. A record whose id is one of the three
// unaddressable names is listed without a link, and the handler answers a
// request naming one with 404.
func RecordPath(prefix, resource, id string, tail ...string) string {
	seg, ok := segment(id)
	if !ok {
		return ""
	}
	return path.Join(append([]string{prefix, resource, seg}, tail...)...)
}

// RowPath is the URL of one row's action on a record: the record's action
// path, then the row's own id as one more segment, or "" when either id is
// not addressable.
func RowPath(prefix, resource, id, action, row string) string {
	base := RecordPath(prefix, resource, id, action)
	seg, ok := segment(row)
	if base == "" || !ok {
		return ""
	}
	return base + "/" + seg
}

// segment is id escaped as one URL path segment, and false when it is not
// addressable. It is the only place this package and internal/ui/web build
// a path from an id, which TestNothingElseBuildsAPathFromAnID holds true.
func segment(id string) (string, bool) {
	if !recordname.Addressable(id) {
		return "", false
	}
	return url.PathEscape(id), true
}
