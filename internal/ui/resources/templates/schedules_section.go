// Package templates' Schedules section: what launches a template without
// anybody pressing Launch.
//
// The answer to "a job appeared that nobody started", which was two views away
// until this existed.
package templates

import (
	"context"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// schedulesSection is what fires this template without anybody pressing
// launch.
//
// AWX's job template carries a Schedules tab and this one did not, which made
// the relationship one-way: the Schedules view names its template and links to
// it, and the template had no way to answer "what makes this run on its own".
// That is the question somebody asks when a job appears that nobody launched,
// and the answer was two views away.
//
// The filter happens here rather than in the store because schedule.Store
// lists by organization and pages by name, with no by-template query. Reading
// one page and selecting from it is honest at this size and wrong at a larger
// one, which is why the cap is stated in the empty text rather than hidden:
// a deployment that outgrows it wants a real ListForTemplate, not a bigger
// number here.
func schedulesSection(templates launch.Store, schedules schedule.Store) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Schedules",
		Summary: "The recurrences that launch this template unattended.",
		Empty: "No schedule fires this template. A template bound to a prompted credential, " +
			"or one whose survey asks for a password, cannot have one: neither value is stored, " +
			"so there would be nothing to replay.",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, MobilePrimary: true, References: "schedules"},
			{Name: "rrule", Label: "RECURRENCE", Kind: view.KindText, InList: true},
			{Name: "timezone", Label: "TIMEZONE", Kind: view.KindText, InList: true},
			{Name: "next_run", Label: "NEXT RUN", Kind: view.KindTimestamp, InList: true},
			{Name: "enabled", Label: "ENABLED", Kind: view.KindBadge, InList: true, BadgeClass: enabledBadge},
		},
		Rows: func(ctx context.Context, parentID string) ([]view.Row, error) {
			// A section under no record must not answer with every schedule
			// in the deployment, which is what an unfiltered read would do.
			id, err := strconv.Atoi(parentID)
			if err != nil || id < 1 {
				return nil, nil
			}

			// A schedule names the LAUNCHABLE it fires, not the template, so
			// the template's own launchable row is what its schedules point
			// at. Resolved here rather than compared by template id, which a
			// schedule no longer carries.
			tmpl, err := templates.Get(ctx, id)
			if err != nil {
				return nil, err
			}
			if tmpl.LaunchableID == 0 {
				// A template with no launchable row cannot be scheduled, so
				// nothing can point at it. Answering with no rows is the
				// truthful reading; it happens only on a database the
				// launchable migration has not reached.
				return nil, nil
			}

			// AnyOrganization because this platform does not derive a tenant
			// from a request yet: internal/schedule names that gap and every
			// caller outside the scheduler passes this sentinel, so a search
			// for it finds them all when a request grows a tenant.
			found, err := schedules.List(ctx, schedule.AnyOrganization, "", sectionLimit)
			if err != nil {
				return nil, err
			}

			rows := make([]view.Row, 0, 4)
			for _, s := range found {
				if s.LaunchableID != tmpl.LaunchableID {
					continue
				}
				rows = append(rows, view.Row{
					ID: s.ScheduleID,
					Cells: view.Cells{
						"name":     s.Name,
						"rrule":    s.RRule,
						"timezone": s.Timezone,
						"next_run": nextRun(s),
						"enabled":  enabledWord(s.Enabled),
					},
					Refs: map[string]string{"name": s.ScheduleID},
				})
			}
			return rows, nil
		},
	}
}

// nextRun renders when this schedule fires next, or says that it will not.
//
// A disabled schedule and one whose recurrence has run out both have no next
// run, and both would render as an empty cell. Saying so is the difference
// between "this is off" and "this column has not loaded".
func nextRun(s schedule.Schedule) string {
	if s.NextRun == nil {
		return "no further occurrences"
	}
	return s.NextRun.UTC().Format("2006-01-02 15:04 MST")
}

// enabledWord is the badge's text. The word, never a colour alone.
func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// enabledBadge keeps a schedule's state inside the validated badge set.
//
// A disabled schedule is neutral rather than failed: somebody turned it off,
// which is a setting and not a fault, and painting it red would make an
// intentional state read as a problem on a page somebody is scanning for one.
func enabledBadge(value string) string {
	if value == "enabled" {
		return "badge-ok"
	}
	return "badge-neutral"
}
