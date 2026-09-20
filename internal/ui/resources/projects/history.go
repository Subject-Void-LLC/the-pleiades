// This file is the Sync history tab: every attempt to fetch this project's
// source, including one still running.
//
// The project's own badge answers "is this usable right now". A history
// answers the different question an operator opens a project for when
// something is wrong: how long has this been failing, did it ever work, and
// what did it say the last time it did. Those need every attempt rather than
// the latest one, which is why internal/project keeps them as rows of their
// own rather than deriving them from a single column.
package projects

import (
	"context"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// historyTitle is the section heading.
const historyTitle = "Sync history"

// historySection lists a project's sync attempts, newest first.
func historySection(store project.Store) view.Section {
	return view.Section{
		Title:   historyTitle,
		Summary: "Every attempt to fetch this project's source, newest first, including one still running.",
		Status:  view.StatusImplemented,
		Fields: []view.Field{
			{
				Name: "outcome", Label: "OUTCOME", Kind: view.KindBadge, InList: true,
				MobilePrimary: true, BadgeClass: syncBadge,
			},
			{Name: "started", Label: "STARTED", Kind: view.KindText, InList: true},
			{
				Name: "actor", Label: "STARTED BY", Kind: view.KindText, InList: true,
				Help: "Who asked for this attempt: a person, or the scheduler and the schedule that fired it.",
			},
			{Name: "took", Label: "TOOK", Kind: view.KindText, InList: true},
			{
				Name: "revision", Label: "REVISION", Kind: view.KindText, InList: true,
				Help: "The commit this attempt ended at, abbreviated. Empty for one that failed.",
			},
			{
				Name: "detail", Label: "DETAIL", Kind: view.KindText, InList: true,
				Help: "Why a failed attempt failed. Credential material is stripped before this is stored.",
			},
		},
		Empty: "This project has never been synced.",
		Rows:  historyRows(store),
	}
}

// historyRows projects a project's sync attempts onto section rows.
func historyRows(store project.Store) func(context.Context, string) ([]view.Row, error) {
	return func(ctx context.Context, parentID string) ([]view.Row, error) {
		// Empty parent: an attempt belongs to a project, not to the
		// collection, so the section on the list page shows its empty state
		// rather than every sync in the system.
		if parentID == "" {
			return nil, nil
		}
		numeric, err := strconv.Atoi(parentID)
		if err != nil {
			return nil, nil
		}

		runs, err := store.ListSyncRuns(ctx, numeric, sectionLimit)
		if err != nil {
			return nil, err
		}

		rows := make([]view.Row, 0, len(runs))
		for _, r := range runs {
			// An attempt in flight has no total yet, so its duration is
			// labelled as what it is: how long it has been going.
			took := describeDuration(r.Took())
			if r.Running() {
				took += " so far"
			}

			// An attempt recorded before attribution existed has nobody to
			// name, which is said rather than left as a blank cell a reader
			// would read as a rendering fault.
			actor := r.Actor
			if actor == "" {
				actor = "not recorded"
			}

			rows = append(rows, view.Row{
				ID: strconv.Itoa(r.ID),
				Cells: view.Cells{
					"outcome":  string(r.Status),
					"started":  r.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
					"actor":    actor,
					"took":     took,
					"revision": shortRevision(r.Revision),
					"detail":   r.Err,
				},
			})
		}
		return rows, nil
	}
}

// describeDuration renders how long an attempt took, in units a reader can
// scan. A clone is seconds to minutes, so sub-second precision is noise and
// hours are worth spelling out.
func describeDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "less than a second"
	case d < time.Second:
		return "less than a second"
	case d < time.Minute:
		return strconv.Itoa(int(d.Round(time.Second).Seconds())) + "s"
	default:
		return d.Round(time.Second).String()
	}
}

// shortRevision abbreviates a commit to git's own seven characters, which is
// what the project's own page shows, so the two cannot disagree about how a
// revision is written.
func shortRevision(rev string) string {
	if len(rev) < 7 {
		return rev
	}
	return rev[:7]
}
