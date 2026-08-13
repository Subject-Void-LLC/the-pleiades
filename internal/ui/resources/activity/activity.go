// Package activity is the Activity Stream view: who changed which managed
// object, and when.
//
// It is AWX's own Activity Stream, under AWX's own name, and it is the one
// view here whose data the platform was already producing and discarding.
// internal/auth's Recorder wrote every admission decision to log/slog, and
// nothing at all recorded the administrative writes, so "who added that
// subject to the admin team" was a question with no answer in the product.
//
// Read-only, and not by convention. The descriptor carries no create,
// update or delete operation because internal/apispec mounts no route for
// any of them: an audit trail a caller can append to is forgeable and one a
// caller can delete from is erasable, which are the two things it exists to
// prevent.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it. A package nothing imports never registers, which is
// FAILURE_PATTERNS.md #52.
package activity

import (
	"context"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "activity"

// fields drive the table, the detail list and the mobile card layout from
// one declaration. None is writable, because nothing here is submitted.
var fields = []view.Field{
	{
		Name: "id", Label: "ENTRY", Kind: view.KindReadOnly,
		Help: "This entry's own identifier, which is what a later reference to it cites.",
	},
	{
		Name: "summary", Label: "CHANGE", Kind: view.KindText,
		InList: true, MobilePrimary: true,
		Help: "What happened, in one sentence.",
	},
	{
		Name: "actor", Label: "ACTOR", Kind: view.KindText,
		InList: true,
		Help:   "The authenticated subject that made the change, captured when it happened.",
	},
	{
		Name: "action", Label: "ACTION", Kind: view.KindBadge,
		InList: true, BadgeClass: actionBadge,
	},
	{
		Name: "object", Label: "OBJECT", Kind: view.KindText,
		InList: true,
		Help:   "The object as it was named at the time, which is not necessarily what it is called now.",
	},
	{
		Name: "at", Label: "WHEN", Kind: view.KindTimestamp,
		InList: true,
	},
}

// actionBadge maps an action onto the closed set of badge classes the
// stylesheet has proven contrast for.
//
// Deletion reads as a failure colour rather than a neutral one, and that is
// a deliberate use of the palette rather than a category error. These
// classes carry weight, not semantics -- the same badge-failed renders a
// critical announcement -- and a removal is the entry a reader scanning a
// page of changes most needs to catch.
func actionBadge(action string) string {
	switch activity.Action(action) {
	case activity.ActionCreated:
		return "badge-ok"
	case activity.ActionUpdated:
		return "badge-changed"
	case activity.ActionDeleted:
		return "badge-failed"
	case activity.ActionAttested:
		return "badge-ok"
	default:
		return "badge-neutral"
	}
}

// reader adapts the activity.Store port to the view's Reader.
type reader struct{ stream activity.Store }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[activity.Entry], error) {
	limit := q.PageSize()

	after := 0
	if q.Cursor != "" {
		parsed, err := strconv.Atoi(q.Cursor)
		if err != nil || parsed < 1 {
			// An unparseable cursor starts at the newest entry rather than
			// erroring. The cursor is ours, not the reader's, so a bad one
			// is a bug on our side and refusing to render the page would
			// turn it into an outage on theirs.
			parsed = 0
		}
		after = parsed
	}

	// One more than asked for, so a next page is observed rather than
	// inferred from a page that happened to come back full
	// (FAILURE_PATTERNS.md's paging entry, and the reason Query.PageSize
	// exists).
	found, err := r.stream.List(ctx, activity.Query{After: after, Limit: limit + 1})
	if err != nil {
		return view.Page[activity.Entry]{}, err
	}

	page := view.Page[activity.Entry]{Items: found}
	if len(found) > limit {
		page.Items = found[:limit]
		page.NextCursor = strconv.Itoa(found[limit-1].ID)
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (activity.Entry, error) {
	numeric, err := strconv.Atoi(id)
	if err != nil {
		return activity.Entry{}, activity.ErrNotFound
	}
	return r.stream.Get(ctx, numeric)
}

// Register wires the Activity Stream view over the live stream.
//
// The object column is rendered as text rather than as a reference to the
// object's own view, unlike every other cross-record column in this UI.
// Three reasons, and any one of them would be enough: the target is
// polymorphic across five views, so there is no single view to declare;
// the most interesting entries name objects that no longer exist, and a
// link to a deleted record is a 404 offered as an affordance; and a reader
// permitted to see the stream is not necessarily permitted to open every
// object in it.
func Register(stream activity.Store) error {
	projector := view.Projector[activity.Entry]{
		Row: func(e activity.Entry) view.Row {
			object := e.ObjectKind
			if e.ObjectName != "" {
				object += " " + e.ObjectName
			}
			return view.Row{ID: strconv.Itoa(e.ID), Cells: view.Cells{
				"id":      strconv.Itoa(e.ID),
				"summary": e.Describe(),
				"actor":   e.Actor,
				"action":  string(e.Action),
				"object":  object,
				"at":      formatTime(e.At),
			}}
		},
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Activity Stream",
		NavLabel: "ACTIVITY STREAM",
		// Last within Views: the dashboard is what is happening now, jobs
		// are what the platform did, and this is what people did to the
		// platform, which is the least frequently asked of the three and
		// the one somebody arrives at deliberately.
		NavOrder: 30,
		NavGroup: view.NavGroupViews,
		Summary:  "Who changed which managed object, and when. Append-only: nothing here can be edited or removed.",
		Status:   view.StatusImplemented,
		IDField:  "id",
		Fields:   fields,
		Ops: view.Ops{
			List: &apispec.ListActivity,
			Get:  &apispec.GetActivityEntry,
			// No Create, Update or Delete, and no route exists for any of
			// them. See this package's own doc comment.
		},
		Handlers: view.MustBind[activity.Entry](reader{stream}, nil, projector),
	})
}

// formatTime renders a timestamp in the one format this UI uses: RFC 3339
// in UTC, so an entry can be correlated against a log line from somewhere
// else. "3 minutes ago" is the one format that cannot be.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
