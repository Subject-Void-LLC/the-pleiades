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

// Register wires the dashboard over the live job store.
func Register(jobs dispatch.JobStore) error {
	agg := summary{jobs}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Dashboard",
		NavLabel: "DASHBOARD",
		// First in the sidebar: it is where somebody who has just signed
		// in wants to land, and the index redirects to the first view a
		// caller can reach.
		NavOrder: 10,
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
		Chart: &view.ChartSpec{
			Title: "Recent job outcomes",
			Caption: "Counts by state across the most recent " +
				"200 dispatches, newest first. Every state is listed even " +
				"when its count is zero.",
			Data: agg.data,
		},
		// Handlers with no List function. This is what makes the view a
		// chart and nothing else, and it is a finished shape rather than
		// an unfinished one.
		Handlers: &view.Handlers{},
	})
}
