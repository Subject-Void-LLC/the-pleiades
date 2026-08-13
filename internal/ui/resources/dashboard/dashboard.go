// Package dashboard is the Dashboard view resource: one chart, no table.
//
// It is the view that proves the descriptor really is a composition of
// optional sections rather than a table with decorations. It registers no
// list handler at all, so the shared template renders the chart and stops,
// with no per-view template, no bespoke handler and no route of its own.
//
// The figures are real. They are aggregated from the same dispatch.JobStore
// the Jobs view reads, so a number here and a row there cannot disagree --
// which is the whole difference between this and the hardcoded panels the
// previous UI shipped.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it (FAILURE_PATTERNS.md #52).
package dashboard

import (
	"context"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "dashboard"

// sampleSize bounds how many recent jobs the summary reads.
//
// A dashboard that scanned every job ever dispatched would get slower for
// exactly as long as the platform stayed in use, and would be answering a
// question nobody asked: "how are things going" means recently, not since
// installation. The caption says so out loud rather than letting a reader
// assume the figure is a lifetime total.
const sampleSize = 200

// buckets is the fixed set of states the chart reports, in the order it
// reports them.
//
// Fixed rather than derived from what the data happens to contain, so a
// state with no jobs renders a zero instead of vanishing. A category that
// disappears when it is empty is how somebody concludes nothing has failed
// when in fact nothing has run.
var buckets = []struct {
	state string
	label string
	class string
}{
	{"completed", "Completed", "badge-ok"},
	{"failed", "Failed", "badge-failed"},
	{"fanning_out", "Fanning out", "badge-changed"},
	{"pending", "Pending", "badge-skipped"},
}

// summary aggregates recent job states.
type summary struct{ jobs dispatch.JobStore }

func (s summary) data(ctx context.Context) (view.ChartData, error) {
	recent, err := s.jobs.List(ctx, "", sampleSize)
	if err != nil {
		return view.ChartData{}, err
	}

	counts := make(map[string]int, len(buckets))
	for _, job := range recent {
		if job != nil {
			counts[job.State]++
		}
	}

	out := view.ChartData{Buckets: make([]view.ChartBucket, 0, len(buckets))}
	for _, b := range buckets {
		out.Buckets = append(out.Buckets, view.ChartBucket{
			Label: b.label,
			Count: counts[b.state],
			Class: b.class,
		})
	}
	return out, nil
}

// announcementFields are the columns of the operator-notice section.
//
// The body is a column rather than a detail-only field, because the whole
// point of an announcement is that somebody reads it without clicking
// anything. A notice that needed a click to reveal its own text would be a
// notice nobody read.
var announcementFields = []view.Field{
	{Name: "level", Label: "LEVEL", Kind: view.KindBadge, InList: true, BadgeClass: levelBadge},
	{Name: "title", Label: "NOTICE", Kind: view.KindText, InList: true, MobilePrimary: true},
	{Name: "body", Label: "DETAIL", Kind: view.KindLongText, InList: true},
	{Name: "author", Label: "FROM", Kind: view.KindReadOnly, InList: true},
	{Name: "window", Label: "UNTIL", Kind: view.KindReadOnly, InList: true},
}

// levelBadge maps an announcement level onto the closed badge set, through
// the announce package's own mapping rather than a second copy of it.
func levelBadge(level string) string { return announce.ParseLevel(level).Class() }

// notices is the announcements section: what the administrators want every
// operator to know before they dispatch anything.
//
// Live-only. An operator reading this page must not be shown a change
// freeze that ended last month, and showing everything would make that the
// common case rather than the exceptional one.
func notices(announcements announce.Store) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Operator notices",
		Summary: "Messages from the administrators of this control plane.",
		Fields:  announcementFields,
		Empty:   "No notices are in effect.",
		Rows: func(ctx context.Context, _ string) ([]view.Row, error) {
			found, err := announcements.List(ctx, announce.Query{LiveAt: time.Now()})
			if err != nil {
				return nil, err
			}
			rows := make([]view.Row, 0, len(found))
			for _, a := range found {
				window := "no expiry"
				if a.EndsAt != nil {
					window = a.EndsAt.UTC().Format(time.RFC3339)
				}
				rows = append(rows, view.Row{ID: a.Title, Cells: view.Cells{
					"level":  string(a.Level),
					"title":  a.Title,
					"body":   a.Body,
					"author": a.Author,
					"window": window,
				}})
			}
			return rows, nil
		},
	}
}

// Register wires the dashboard over the live job store and announcements.
func Register(jobs dispatch.JobStore, announcements announce.Store) error {
	agg := summary{jobs}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Dashboard",
		NavLabel: "DASHBOARD",
		// First in the sidebar: it is where somebody who has just signed
		// in wants to land, and the index redirects to the first view a
		// caller can reach.
		NavOrder: 10,
		NavGroup: view.NavGroupViews,
		Summary:  "How recent runbook dispatches are going.",
		Status:   view.StatusImplemented,
		Fields:   nil,
		Ops: view.Ops{
			// The listing endpoint's scope is what gates this view, since
			// what it summarises is that endpoint's data. There is no Get:
			// a summary has no individual record to open, which is exactly
			// the shape Register allows as long as nothing renders a
			// detail link, and nothing here does.
			List: &apispec.ListJobs,
		},
		Sections: []view.Section{notices(announcements)},
		Chart: &view.ChartSpec{
			Title: "Recent job outcomes",
			Caption: "Counts by state across the most recent " +
				"200 dispatches, newest first. Every state is listed even " +
				"when its count is zero.",
			Data: agg.data,
		},
		// Handlers with no List function. This is what makes the view a
		// chart and its sections, rather than a table, and it is a
		// finished shape rather than an unfinished one.
		Handlers: &view.Handlers{},
	})
}
