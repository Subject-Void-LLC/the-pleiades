// Package schedules is the Schedules view: when automation runs without
// somebody pressing launch.
//
// An RFC 5545 recurrence attached to anything launchable, which is why it
// waited on the Launchable abstraction rather than only on a parser: a
// schedule must attach to a template, a project sync or a workflow with one
// mechanism, or every kind grows its own scheduler. It attaches to a
// Template, and a Template carries its own kind, so one relationship
// reaches every kind that exists and every kind added later.
//
// The view exists to answer a question a table cannot: what will this
// actually do. A recurrence rule is easy to write and easy to misread --
// somebody can mean the last Friday, write BYDAY=-1FR, and not discover
// they got the fourth Friday until a month later -- and a wrong schedule is
// wrong silently, out of hours, repeatedly. So NEXT RUN is a real column
// rather than a detail-page afterthought, and the occurrence history
// records what did NOT run as rows rather than as gaps.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it. A package nothing imports never registers, which is
// FAILURE_PATTERNS.md #52.
package schedules

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/zoneinfo"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "schedules"

// timeFormat is the one format this view submits and renders datetimes in.
// It is the HTML datetime-local control's own wire format, so a browser
// round-trips a value unchanged.
const timeFormat = "2006-01-02T15:04"

// Store is the slice of internal/schedule's store this view needs.
//
// The scanner's methods are deliberately absent: nothing reachable from a
// browser can claim, fire or resolve an occurrence, only describe when one
// should happen.
type Store interface {
	Create(ctx context.Context, s schedule.Schedule, reach launchable.Reach) (schedule.Schedule, error)
	Update(ctx context.Context, s schedule.Schedule, reach launchable.Reach) (schedule.Schedule, error)
	Get(ctx context.Context, orgID int, scheduleID string) (schedule.Schedule, error)
	List(ctx context.Context, orgID int, after string, limit int) ([]schedule.Schedule, error)
	Delete(ctx context.Context, orgID int, scheduleID string) error
}

// The RUNS picker's own port lives in runs.go, beside the filtering it does.

// fields drive the table, the form, the detail list, validation and the
// mobile card layout from one declaration.
var fields = []view.Field{
	{
		Name: "name", Label: "NAME", Kind: view.KindText,
		Required: true, MaxLen: 120,
		InForm: true, InList: true, MobilePrimary: true,
		Help: "What this schedule is called. Unique within its organization.",
	},
	{
		Name: "runs", Label: "RUNS", Kind: view.KindSelect,
		Required: true, InForm: true, InList: true,
		Help: "What this launches: a job template, or a project whose run is a sync. " +
			"Its organization becomes the schedule's, and only things you may launch are offered.",
	},
	{
		Name: "rrule", Label: "RECURRENCE", Kind: view.KindText,
		Required: true, MaxLen: 512, InForm: true, InList: true,
		// The help text carries a worked example rather than a grammar,
		// because the grammar is in the reference documentation and what
		// somebody standing at this control needs is a shape to copy.
		Help: "An RFC 5545 rule, for example FREQ=DAILY;BYHOUR=2 for 2am daily, " +
			"or FREQ=MONTHLY;BYDAY=-1FR for the last Friday of each month. " +
			"Saved only if it parses and names at least one real time.",
	},
	{
		Name: "exclusions", Label: "EXCLUSIONS", Kind: view.KindLongText,
		MaxLen: 2048, InForm: true,
		Help: "One rule per line, subtracted from the recurrence. " +
			"FREQ=WEEKLY;BYDAY=SA,SU skips weekends; " +
			"EXDATE:20241225T000000Z skips one date.",
	},
	{
		Name: "timezone", Label: "TIMEZONE", Kind: view.KindSelect,
		Required: true, InForm: true, InList: true,
		Options: zoneOptions,
		// This is the field most likely to be set carelessly and least
		// likely to look wrong afterwards, so the help says what it
		// actually changes rather than what it is.
		Help: "The zone the recurrence is read in. It decides what the rule means: " +
			"a daily rule keeps its local hour across a daylight saving change, " +
			"so two runs can be 23 or 25 hours apart.",
	},
	{
		Name: "dtstart", Label: "STARTS", Kind: view.KindText,
		Required: true, InForm: true, MaxLen: 32,
		Help: "When the recurrence is anchored, as YYYY-MM-DDTHH:MM. " +
			"The rule takes from it anything it does not state itself, including the time of day.",
	},
	{
		Name: "dtend", Label: "ENDS", Kind: view.KindText,
		MaxLen: 32, InForm: true,
		Help: "Optional. Leave blank to run indefinitely.",
	},
	{
		Name: "enabled", Label: "ENABLED", Kind: view.KindBool,
		InForm: true, InList: true, BadgeClass: enabledBadge,
		Help: "Turn a schedule off without deleting it, so its history survives.",
	},
	{
		Name: "next_run", Label: "NEXT RUN", Kind: view.KindTimestamp,
		InList: true,
		// KindTimestamp is never submitted, which is what makes this
		// read-only: it is computed from the rule on every write. Shown in
		// the schedule's own zone because that is the reading somebody
		// recognises; the API carries both.
		Help: "The next time this will run, in the schedule's own zone. Computed from the recurrence.",
	},
	{
		Name: "last_fired", Label: "LAST RUN", Kind: view.KindTimestamp,
		Help: "The occurrence time of the most recent run.",
	},
}

// enabledBadge colours the ENABLED column.
//
// The returned names come from view.ValidBadgeClasses' closed set; anything
// outside it is refused at registration, which is what stops a view
// inventing a class the stylesheet has no rule for.
//
// A disabled schedule reads as "skipped" rather than as a failure, because
// that is what it is: somebody turned it off deliberately, and colouring it
// like an error would make a routine pause look like an incident.
func enabledBadge(value string) string {
	if value == "true" {
		return "badge-ok"
	}
	return "badge-skipped"
}

// zoneOptions supplies the TIMEZONE picker.
//
// The common zones are listed first, then every other zone alphabetically,
// because a flat alphabetical list of five hundred and fifty entries opens
// on Africa/Abidjan and buries every zone anybody actually wants. The full
// set is still present: a picker that offered only the common ones would
// make this platform unusable in a region somebody forgot to list.
func zoneOptions(context.Context) ([]view.Option, error) {
	common := zoneinfo.Common()
	seen := make(map[string]bool, len(common))

	opts := make([]view.Option, 0, len(zoneinfo.Names())+len(common))
	for _, z := range common {
		seen[z] = true
		opts = append(opts, view.Option{Value: z, Label: z})
	}
	rest := make([]string, 0)
	for _, z := range zoneinfo.Names() {
		if !seen[z] {
			rest = append(rest, z)
		}
	}
	sort.Strings(rest)
	for _, z := range rest {
		opts = append(opts, view.Option{Value: z, Label: z})
	}
	return opts, nil
}

// reader adapts the store to view.Reader.
type reader struct {
	store Store
}

func (r reader) List(ctx context.Context, q view.Query) (view.Page[schedule.Schedule], error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	// One more than asked for, so a next page is observed rather than
	// inferred from a page that happened to come back full.
	found, err := r.store.List(ctx, schedule.AnyOrganization, q.Cursor, limit+1)
	if err != nil {
		return view.Page[schedule.Schedule]{}, err
	}
	page := view.Page[schedule.Schedule]{Items: found}
	if len(found) > limit {
		page.Items = found[:limit]
		page.NextCursor = found[limit-1].ScheduleID
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (schedule.Schedule, error) {
	return r.store.Get(ctx, schedule.AnyOrganization, id)
}

// writer adapts the store to view.Writer.
type writer struct {
	store Store
}

func (w writer) Create(ctx context.Context, s schedule.Schedule) (string, error) {
	created, err := w.store.Create(ctx, s, reachOf(ctx))
	if err != nil {
		return "", asFieldFault(err)
	}
	return created.ScheduleID, nil
}

func (w writer) Update(ctx context.Context, id string, s schedule.Schedule) error {
	existing, err := w.store.Get(ctx, schedule.AnyOrganization, id)
	if err != nil {
		return err
	}
	// The identity of the row being edited comes from storage, never from
	// the submission: a form that could restate them could move a schedule
	// between tenants.
	s.ID = existing.ID
	s.ScheduleID = existing.ScheduleID
	s.OrganizationID = existing.OrganizationID

	// The saved configuration is carried forward rather than cleared. This
	// form renders no control for it, so a submission never carries one, and
	// the store reads an absent value as "run the target's own defaults": an
	// edit that only renamed a schedule used to silently drop the overrides it
	// ran with (FAILURE_PATTERNS.md #269). Changing what a schedule launches
	// is the one case where carrying it forward would be wrong, so that is
	// refused instead, with a message naming the control.
	switch {
	case s.LaunchableID == existing.LaunchableID:
		s.SavedConfigID = existing.SavedConfigID
	case existing.SavedConfigID != 0:
		return view.FieldFault{
			Field: "runs",
			Message: "This schedule runs with a saved configuration, which belongs to what it launches now. " +
				"Clear the configuration before pointing it at something else.",
		}
	}

	if _, err := w.store.Update(ctx, s, reachOf(ctx)); err != nil {
		return asFieldFault(err)
	}
	return nil
}

// reachOf is the writing viewer's own reach: what they may launch.
//
// Organization zero means "not narrowed to one tenant", matching the API
// handler: no request in this build carries a tenant, so the tenancy half of
// the check compares the target's organization to the schedule's. The scope
// half is per launchable type and is enforced regardless, which is what makes
// this form unable to schedule something the viewer could not launch by hand.
func reachOf(ctx context.Context) launchable.Reach {
	identity, _ := api.IdentityFromContext(ctx)
	return launchable.ReachOf(identity, 0)
}

func (w writer) Delete(ctx context.Context, id string) error {
	return w.store.Delete(ctx, schedule.AnyOrganization, id)
}

// asFieldFault turns a store validation failure into the form-level fault
// the shared binder knows how to redisplay, so a bad recurrence lands under
// the recurrence box rather than on an error page.
func asFieldFault(err error) error {
	var fe schedule.FieldError
	if !asScheduleFieldError(err, &fe) {
		return err
	}
	return view.FieldFault{Field: fe.Field, Message: fe.Message}
}

// asScheduleFieldError recovers a schedule.FieldError from an error chain.
//
// A tiny wrapper over errors.As, but it is called from two places and
// naming it keeps the intent visible: the question being asked is "does
// this failure blame a field the form rendered", which is what decides
// between redisplaying the form and showing an error page.
func asScheduleFieldError(err error, target *schedule.FieldError) bool {
	return errors.As(err, target)
}

// Register adds the Schedules view over a real store.
func Register(store Store, launchables Launchables) error {
	projector := view.Projector[schedule.Schedule]{
		Row: func(s schedule.Schedule) view.Row {
			return view.Row{ID: s.ScheduleID, Cells: view.Cells{
				"name":       s.Name,
				"runs":       targetLabel(s),
				"rrule":      s.RRule,
				"exclusions": strings.Join(s.Exclusions, "\n"),
				"timezone":   s.Timezone,
				"dtstart":    formatIn(&s.DTStart, s),
				"dtend":      formatIn(s.DTEnd, s),
				"enabled":    strconv.FormatBool(s.Enabled),
				"next_run":   formatIn(s.NextRun, s),
				"last_fired": formatIn(s.LastFired, s),
			}}
		},
		Form: func(s schedule.Schedule) map[string]string {
			return map[string]string{
				"name":       s.Name,
				"runs":       strconv.Itoa(s.LaunchableID),
				"rrule":      s.RRule,
				"exclusions": strings.Join(s.Exclusions, "\n"),
				"timezone":   s.Timezone,
				"dtstart":    formatLocalInput(&s.DTStart, s),
				"dtend":      formatLocalInput(s.DTEnd, s),
				"enabled":    strconv.FormatBool(s.Enabled),
			}
		},
		Bind: func(v view.Values) (schedule.Schedule, view.FieldErrors) {
			errs := view.FieldErrors{}

			out := schedule.Schedule{
				Name:     strings.TrimSpace(v.Get("name")),
				RRule:    strings.TrimSpace(v.Get("rrule")),
				Timezone: strings.TrimSpace(v.Get("timezone")),
				Enabled:  v.Get("enabled") == "true",
			}
			if out.Timezone == "" {
				out.Timezone = "UTC"
			}
			out.Exclusions = splitLines(v.Get("exclusions"))

			launchableID, err := strconv.Atoi(strings.TrimSpace(v.Get("runs")))
			if err != nil || launchableID < 1 {
				errs.Add("runs", "Choose what this schedule runs.")
			}
			out.LaunchableID = launchableID

			// The two datetimes are read in the schedule's OWN zone, not
			// UTC and not the browser's. That is the only reading that
			// makes sense of the pair: somebody choosing America/New_York
			// and typing 02:00 means two in the morning in New York, and
			// interpreting it as UTC would silently move every occurrence
			// by the offset.
			loc, zoneErr := zoneinfo.Load(out.Timezone)
			if zoneErr != nil {
				errs.Add("timezone", "Choose a time zone from the list.")
				loc = time.UTC
			}

			if start, ok := parseLocal(v.Get("dtstart"), loc); ok {
				out.DTStart = start
			} else {
				errs.Add("dtstart", "Enter a start as YYYY-MM-DDTHH:MM.")
			}
			if raw := strings.TrimSpace(v.Get("dtend")); raw != "" {
				if end, ok := parseLocal(raw, loc); ok {
					out.DTEnd = &end
				} else {
					errs.Add("dtend", "Enter an end as YYYY-MM-DDTHH:MM, or leave it blank.")
				}
			}

			// The recurrence is validated here as well as in the store, so
			// a mistyped rule comes back attached to the box it was typed
			// into rather than as a failed save.
			if len(errs) == 0 {
				if err := out.Validate(); err != nil {
					var fe schedule.FieldError
					if asScheduleFieldError(err, &fe) {
						errs.Add(fe.Field, fe.Message)
					} else {
						errs.Add("rrule", "That recurrence could not be read.")
					}
				}
			}
			return out, errs
		},
	}

	// The RUNS picker is populated from the same store the schedule store
	// resolves its target against, and filtered by the same reach the store
	// checks on submit, so what the form offers and what a save accepts are
	// one list under one rule.
	descriptorFields := append([]view.Field(nil), fields...)
	for i := range descriptorFields {
		if descriptorFields[i].Name == "runs" {
			descriptorFields[i].Options = runsOptions(launchables)
		}
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Schedules",
		NavLabel: "SCHEDULES",
		NavOrder: 25,
		NavGroup: view.NavGroupViews,
		Summary:  "When automation runs without somebody pressing launch.",
		Status:   view.StatusImplemented,
		IDField:  "name",
		Fields:   descriptorFields,
		// Every affordance this view draws is backed by a route the
		// controller actually mounts. Declaring them here is what lets the
		// shared HATEOAS generator decide which controls an identity may
		// see, rather than this view guessing -- and what stops it drawing
		// a button for a route nobody registered.
		Ops: view.Ops{
			List:   &apispec.ListSchedules,
			Get:    &apispec.GetSchedule,
			Create: &apispec.CreateSchedule,
			Update: &apispec.UpdateSchedule,
			Delete: &apispec.DeleteSchedule,
		},
		Handlers: view.MustBind[schedule.Schedule](reader{store}, writer{store}, projector),
	})
}

// formatIn renders an optional instant in the schedule's own zone.
//
// Times are stored in UTC and displayed local, always. Showing UTC would be
// technically true and practically useless: the whole reason the zone is a
// field is that somebody reasons about this schedule in local hours.
func formatIn(t *time.Time, s schedule.Schedule) string {
	if t == nil || t.IsZero() {
		return ""
	}
	loc, err := s.Location()
	if err != nil {
		return t.UTC().Format("2006-01-02 15:04 MST")
	}
	return t.In(loc).Format("2006-01-02 15:04 MST")
}

// formatLocalInput renders an optional instant for a form control.
func formatLocalInput(t *time.Time, s schedule.Schedule) string {
	if t == nil || t.IsZero() {
		return ""
	}
	loc, err := s.Location()
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format(timeFormat)
}

// parseLocal reads a form datetime in loc, accepting the control's own
// format and the seconds-bearing variant a browser sometimes submits.
func parseLocal(raw string, loc *time.Location) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{timeFormat, "2006-01-02T15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// splitLines turns a textarea's contents into a list, dropping blank lines
// so a trailing newline does not become an empty exclusion the parser then
// has to refuse.
func splitLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}
