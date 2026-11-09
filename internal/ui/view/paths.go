// Package view: the one way a view builds the URL of a record.
//
// Every link to a record, a record's page or a row of one of its sections
// goes through RecordPath or RowPath, so ids are escaped here and nowhere
// else. Which ids can be a URL path segment at all is internal/recordname's
// rule, shared with every write that chooses a device's name.
package view

import (
	"path"

	"github.com/Subject-Void-LLC/the-pleiades/internal/recordname"
)

// NewSegment and ChartSegment are the fixed segments internal/ui/web's
// router matches beside a record's {id}: /{resource}/new, the create form,
// and /{resource}/chart.json, a chart's data. The router registers both
// routes from these constants, and its TestRecordSiblings_AreTheRoutersOwn
// derives the set from the built router's registrations and holds it to
// RecordSiblings, so a static route added there without one here fails.
const (
	NewSegment   = "new"
	ChartSegment = "chart.json"
)

// recordSiblings is NewSegment and ChartSegment as a set, for
// recordname.Segment.
var recordSiblings = map[string]bool{NewSegment: true, ChartSegment: true}

// RecordSiblings is every fixed segment the router matches beside a
// record's {id}.
func RecordSiblings() []string {
	return []string{NewSegment, ChartSegment}
}

// RecordPath is the URL of record id of resource under prefix, followed by
// tail's literal segments (edit, logs, an action's registered name), or ""
// when id is not addressable (recordname.Addressable). id is escaped as one
// segment, so a "/" in it is never a second one, and an id equal to a fixed
// segment beside {id} ("new", "chart.json") has its first byte encoded as
// well, so the device named "new" links to its own page (/ui/devices/%6Eew)
// rather than to the create form (recordname.Segment).
//
// url.PathEscape leaves "." and ".." as they are, and path.Join then
// resolves them: a record named ".." would link to the page above its own
// list, and one named "." to the list. A record whose id is one of the three
// unaddressable names is listed without a link, and the handler answers a
// request naming one with 404.
func RecordPath(prefix, resource, id string, tail ...string) string {
	seg, ok := segment(id, recordSiblings)
	if !ok {
		return ""
	}
	return path.Join(append([]string{prefix, resource, seg}, tail...)...)
}

// RowPath is the URL of one row's action on a record: the record's action
// path, then the row's own id as one more segment, or "" when either id is
// not addressable. Nothing is registered beside a row's segment, so no row
// id is shadowed.
func RowPath(prefix, resource, id, action, row string) string {
	base := RecordPath(prefix, resource, id, action)
	seg, ok := segment(row, nil)
	if base == "" || !ok {
		return ""
	}
	return base + "/" + seg
}

// segment is id escaped as one URL path segment, with its first byte
// encoded when it equals one of static, and false when it is not
// addressable. It is the only place this package and internal/ui/web build
// a path from an id, which TestNothingElseBuildsAPathFromAnID holds true.
func segment(id string, static map[string]bool) (string, bool) {
	if !recordname.Addressable(id) {
		return "", false
	}
	return recordname.Segment(id, static), true
}
