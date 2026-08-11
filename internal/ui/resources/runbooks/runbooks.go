// Package runbooks is the Runbooks view resource: a catalog of what can be
// dispatched.
//
// It is read-only, and deliberately so rather than for want of time.
// Runbooks arrive from RUNBOOK_DIR and, eventually, from GitOps sync. A
// write path would open a new caller-controlled filesystem surface on the
// control plane in order to duplicate a job that version control already
// does better, so this resource supplies a Reader and no Writer at all --
// which is the entire reason those are separate interfaces.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it (FAILURE_PATTERNS.md #52).
package runbooks

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "runbooks"

var fields = []view.Field{
	{
		Name: "id", Label: "RUNBOOK", Kind: view.KindText,
		InList: true, MobilePrimary: true, Sortable: true,
		Help: "The id this runbook is dispatched by.",
	},
	{
		Name: "capabilities", Label: "REQUIRES", Kind: view.KindTags, InList: true,
		Help: "Every capability a device must have for this runbook to run against it.",
	},
	{
		Name: "interruptible", Label: "INTERRUPTIBLE", Kind: view.KindBadge,
		InList: true, BadgeClass: interruptibleBadge,
		Help: "Whether a run may be aborted partway, or must finish once started.",
	},
	{Name: "capability_count", Label: "REQUIREMENTS", Kind: view.KindReadOnly},
}

// interruptibleBadge colours the answer rather than only stating it. The
// word is rendered inside the badge too, so nothing here is encoded in
// colour alone.
func interruptibleBadge(value string) string {
	if value == "yes" {
		return "badge-ok"
	}
	return "badge-changed"
}

// reader adapts the runbook.Source port.
//
// Source.List returns ids rather than compiled runbooks, on the grounds
// that compiling every runbook to answer "what can I run" makes opening a
// list cost as much as running one. This reader honours that: it pages over
// the ids and compiles only the ones on the page being rendered.
type reader struct{ source runbook.Source }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[*runbook.Runbook], error) {
	ids, err := r.source.List(ctx)
	if err != nil {
		return view.Page[*runbook.Runbook]{}, err
	}
	sort.Strings(ids)

	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	// Keyset paging over a sorted, in-memory slice. The cursor is the last
	// id of the previous page rather than an index, for the same reason
	// every other list here pages that way: an offset into a directory
	// listing that gained a file between two requests silently skips one.
	start := 0
	if q.Cursor != "" {
		start = sort.SearchStrings(ids, q.Cursor)
		if start < len(ids) && ids[start] == q.Cursor {
			start++
		}
	}
	if start > len(ids) {
		start = len(ids)
	}

	end := min(start+limit, len(ids))

	var page view.Page[*runbook.Runbook]
	for _, id := range ids[start:end] {
		rb, err := r.source.Get(ctx, id)
		if err != nil {
			// One unreadable runbook must not blank the whole catalog. It
			// is skipped rather than propagated, because a directory
			// holding a file that does not compile is an ordinary state
			// for this source and the other entries are still true.
			continue
		}
		page.Items = append(page.Items, rb)
	}
	if end < len(ids) {
		page.NextCursor = ids[end-1]
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (*runbook.Runbook, error) {
	return r.source.Get(ctx, id)
}

// Register wires this view over the live runbook source.
func Register(source runbook.Source) error {
	projector := view.Projector[*runbook.Runbook]{
		Row: func(rb *runbook.Runbook) view.Row {
			if rb == nil {
				return view.Row{}
			}
			names := make([]string, 0, len(rb.Required))
			for _, c := range rb.Required {
				names = append(names, string(c))
			}
			return view.Row{ID: rb.ID, Cells: view.Cells{
				"id":               rb.ID,
				"capabilities":     strings.Join(names, ", "),
				"interruptible":    yesNo(rb.Interruptible),
				"capability_count": strconv.Itoa(len(names)),
			}}
		},
		// No Form and no Bind: with no Writer, Bind never reads either,
		// and supplying them would suggest a write path that does not
		// exist.
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Runbooks",
		NavLabel: "RUNBOOKS",
		NavOrder: 40,
		Summary:  "Every runbook this control plane can dispatch.",
		Status:   view.StatusImplemented,
		IDField:  "id",
		Fields:   fields,
		Ops: view.Ops{
			List: &apispec.ListRunbooks,
			Get:  &apispec.GetRunbook,
			// No Create, Update or Delete. Runbooks come from a directory
			// and from GitOps; a write path here would be a second,
			// unversioned way to change what this platform executes.
		},
		Handlers: view.MustBind[*runbook.Runbook](reader{source}, nil, projector),
	})
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}
