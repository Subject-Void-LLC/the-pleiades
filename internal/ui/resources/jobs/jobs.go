// Package jobs is the Jobs view resource: dispatch a runbook, then watch
// what it did.
//
// It is the resource that proves the read/create split is worth having. A
// job can be brought into being and then only observed -- editing a running
// fan-out is meaningless, and deleting one would destroy the audit record
// the job exists to be -- so it binds through view.MustBindCreatable and
// carries no update or delete handler at all. No template renders a control
// for an operation that does not exist, so the absence is the whole
// enforcement.
//
// It is reachable only because internal/ui/resources/registrars.go names
// it. A package nothing imports never registers, which is
// FAILURE_PATTERNS.md #52.
package jobs

import (
	"context"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "jobs"

// JournalReader is the sliver of the run journal this view needs.
//
// A narrow port rather than *journal.EntStore, matching the Interface
// Segregation this package already applies to the Dispatcher: a view that
// took the store whole would gain the ability to WRITE journal entries as a
// side effect of being able to show them, and a journal a UI can write to
// is not an audit trail.
//
// It is optional at the composition root and a nil one draws no section at
// all, which is the honest rendering for a deployment that has not wired
// the journal rather than a tab that is permanently empty for everybody.
type JournalReader interface {
	// ForJob returns the job's entries oldest first per device, and
	// reports whether the read was capped. The bound belongs to the
	// implementation; this port only carries the answer.
	ForJob(ctx context.Context, jobID string, limit int) ([]engine.JournalEntry, bool, error)
}

// journalLimit is what the Tasks section asks for.
//
// Below internal/journal's own cap, deliberately: this is a table on a
// detail page, and a reader who needs twenty thousand rows is doing an
// export rather than reading a page. The section says when it has capped.
const journalLimit = 500

// fields drive the table, the dispatch form, the detail list, validation
// and the mobile card layout from one declaration.
//
// None is writable, and that is the whole of it as of Phase 21: a job is
// launched from a Template, on the Templates view, and everything here is
// something the platform decided rather than something a caller may
// assert. This view used to carry a two-field dispatch form naming a group
// and a runbook; that launch surface is gone.
var fields = []view.Field{
	{
		Name: "job_id", Label: "JOB", Kind: view.KindText,
		InList: true, MobilePrimary: true,
		Help: "The server-generated identifier this job is polled on.",
	},
	{
		Name: "runbook", Label: "RUNBOOK", Kind: view.KindText,
		Help:   "The runbook this job dispatched.",
		InList: true,
	},
	{
		Name: "template", Label: "TEMPLATE", Kind: view.KindText,
		// Deliberately not a link. The name is the one the template
		// carried at launch, captured rather than resolved, and a template
		// can be renamed or deleted afterwards: a link would either 404 or
		// take a reader to something that no longer matches the words they
		// clicked. The template's own page lists what it has run, which is
		// the same relationship read from the end that still exists.
		Help:   "The saved definition this job was launched from, named as it was at launch: a job's history outlives the template.",
		InList: true,
	},
	{
		Name: "kind", Label: "KIND", Kind: view.KindBadge,
		Help:   "Which registered launch kind ran, and therefore which execution adapter handled it.",
		InList: true, BadgeClass: kindBadge,
	},
	{
		Name: "mode", Label: "MODE", Kind: view.KindBadge,
		Help: "execute: a real run. check: every task was asked what it would change and nothing was changed, " +
			"so this job's changed counts describe what a real run would have done.",
		InList: true, BadgeClass: modeBadge,
	},
	{
		Name: "state", Label: "STATE", Kind: view.KindBadge,
		InList: true, BadgeClass: stateBadge,
	},
	{Name: "dispatched", Label: "DISPATCHED", Kind: view.KindReadOnly, InList: true},
	{Name: "skipped", Label: "SKIPPED", Kind: view.KindReadOnly},
	{Name: "failed", Label: "FAILED", Kind: view.KindReadOnly},
	// LAUNCHED BY rather than ACTOR, which is what
	// templates/sections.go already called the identical value on its own
	// Jobs tab. One concept with two names is a reader wondering whether
	// they are two concepts.
	{Name: "actor", Label: "LAUNCHED BY", Kind: view.KindReadOnly},
	{Name: "created", Label: "CREATED", Kind: view.KindTimestamp, InList: true},
}

// kindBadge paints the launch-kind indicator from the registered
// descriptor's own declared class, so a kind arriving in a file this
// package has never seen brings its own colour rather than falling into a
// default nothing chose. A job whose kind is no longer registered, or one
// launched before kinds existed, renders neutral rather than claiming a
// class.
func kindBadge(kind string) string {
	if d, ok := launch.Lookup(kind); ok {
		return d.BadgeClass
	}
	return "badge-neutral"
}

// modeBadge marks a check apart from a real run, so a list of jobs never
// shows a check that "completed" looking like a change that was made. A
// record whose mode could not be read (dispatch.Job.ModeLabel) reads as
// failed, which is what fan-out made of it.
func modeBadge(mode string) string {
	switch mode {
	case "check":
		return "badge-skipped"
	case "unreadable":
		return "badge-failed"
	default:
		return "badge-neutral"
	}
}

// stateBadge maps a job's lifecycle state onto the closed set of badge
// classes. A state this build does not recognise reads neutral rather than
// being interpolated into a class attribute.
func stateBadge(state string) string {
	switch state {
	case "completed":
		return "badge-ok"
	case "failed":
		return "badge-failed"
	case "canceled":
		// Neutral rather than badge-failed. A canceled run did not break,
		// somebody stopped it, and colouring the two alike would undo the
		// distinction the state exists to draw.
		return "badge-neutral"
	case "fanning_out", "running":
		return "badge-changed"
	case "pending":
		return "badge-skipped"
	default:
		return "badge-neutral"
	}
}

// reader adapts the dispatch.JobStore port to the view's Reader.
type reader struct{ jobs dispatch.JobStore }

func (r reader) List(ctx context.Context, q view.Query) (view.Page[*dispatch.Job], error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	// One more than asked for, so a next page is observed rather than
	// inferred from a page that happened to come back full.
	found, err := r.jobs.List(ctx, q.Cursor, limit+1)
	if err != nil {
		return view.Page[*dispatch.Job]{}, err
	}

	page := view.Page[*dispatch.Job]{Items: found}
	if len(found) > limit {
		page.Items = found[:limit]
		page.NextCursor = found[limit-1].JobID
	}
	return page, nil
}

func (r reader) Get(ctx context.Context, id string) (*dispatch.Job, error) {
	// The per-device task rows are deliberately discarded. A detail page
	// shows the job's own fields and links to the live stream for
	// everything else, and loading the task list for a job that fanned
	// out to a thousand devices would be a large read for something no
	// field renders.
	job, _, err := r.jobs.Get(ctx, id)
	return job, err
}

// taskBadge maps a per-device outcome onto the closed badge set.
func taskBadge(outcome string) string {
	switch outcome {
	case string(dispatch.OutcomeDispatched):
		return "badge-ok"
	case string(dispatch.OutcomeFailed):
		return "badge-failed"
	case string(dispatch.OutcomeSkipped):
		return "badge-skipped"
	default:
		return "badge-neutral"
	}
}

// taskFields are the columns of the per-device drill-down.
//
// Reason is included and is the point of the whole section: a job row
// saying "1 failed" tells an operator that something went wrong, and this
// is what tells them which device and why. The schema's own comment bounds
// what may appear there -- a device name, a lifecycle state, or a missing
// capability, never a device's properties -- so it is safe to render.
var taskFields = []view.Field{
	// The device is a link, because this row is where an investigation
	// stops being about a job and starts being about a machine. "42
	// dispatched, 3 failed" sends somebody here; "edge-mad-07 failed
	// because dpkg was locked" sends them to edge-mad-07, and until this
	// reference existed that was a name they had to copy into the Devices
	// list by hand.
	{Name: "device", Label: "DEVICE", Kind: view.KindText, InList: true, MobilePrimary: true, References: "devices"},
	{Name: "outcome", Label: "OUTCOME", Kind: view.KindBadge, InList: true, BadgeClass: taskBadge},
	// Result is a second column rather than more values in OUTCOME,
	// because the two answer different questions and an operator needs
	// both. Outcome says whether this device was handed to a Runner;
	// result says what the Runner made of it. A device reading
	// "dispatched" with an empty result has not reported back yet, which
	// is exactly what a job sitting in "running" is waiting for.
	{Name: "result", Label: "RESULT", Kind: view.KindBadge, InList: true, BadgeClass: resultBadge},
	{Name: "reason", Label: "REASON", Kind: view.KindText, InList: true},
}

// resultBadge colours what the Runner reported for one device.
//
// The empty value is the common case and reads neutral rather than
// failed: a device that has not reported yet has not gone wrong.
func resultBadge(result string) string {
	switch result {
	case string(dispatch.ResultSucceeded):
		return "badge-ok"
	case string(dispatch.ResultFailed):
		return "badge-failed"
	default:
		return "badge-neutral"
	}
}

// deviceOutcomes is the drill-down section: one row per device this job
// fanned out to.
//
// The data was always there -- dispatch.JobStore.Get returns every JobTask
// alongside the Job -- and the reader discarded it, so the detail page
// showed three counts and no way to find out which device they referred to.
func deviceOutcomes(jobs dispatch.JobStore) view.Section {
	return view.Section{
		Status:  view.StatusImplemented,
		Title:   "Device outcomes",
		Summary: "What happened on each device this job was dispatched to.",
		Fields:  taskFields,
		Empty:   "This job has not recorded any per-device outcomes yet.",
		Rows: func(ctx context.Context, jobID string) ([]view.Row, error) {
			_, tasks, err := jobs.Get(ctx, jobID)
			if err != nil {
				return nil, err
			}
			rows := make([]view.Row, 0, len(tasks))
			for _, t := range tasks {
				rows = append(rows, view.Row{
					ID: t.DeviceID,
					Cells: view.Cells{
						"device":  t.DeviceName,
						"outcome": t.Outcome.String(),
						"result":  t.Result.String(),
						// The Runner's own sentence when it has one,
						// falling back to the Controller's reason for a
						// device that never ran. One column, because a
						// reader wants to know why this device is in the
						// state it is in, and only one of the two is ever
						// populated for a given device.
						"reason": firstNonEmpty(t.ResultReason, t.Reason),
					},
					// The stored device id, which is what the link is
					// built from. The cell shows the name: a cell showing
					// a primary key has moved the join into the reader's
					// head, which is the thing the link exists to undo.
					Refs: map[string]string{"device": t.DeviceID},
				})
			}
			return rows, nil
		},
	}
}

// Register wires this view over the live job store.
//
// Read-only, deliberately. A job is a record of something that already
// happened, and the place to start one is the runbook you want to run --
// which is where AWX puts it too, and where an operator looks for it. A
// "new job" form here would ask somebody to type a runbook id they just
// came from a page listing.
func Register(jobs dispatch.JobStore, runner Relauncher, canceller Canceler, entries JournalReader, logs LogArchive) error {
	projector := view.Projector[*dispatch.Job]{
		Row: func(j *dispatch.Job) view.Row {
			if j == nil {
				return view.Row{}
			}
			return view.Row{ID: j.JobID, Cells: view.Cells{
				"job_id":     j.JobID,
				"runbook":    j.RunbookID,
				"template":   j.TemplateName,
				"kind":       j.Kind,
				"mode":       j.ModeLabel(),
				"state":      j.State,
				"dispatched": strconv.Itoa(j.DispatchedCount),
				"skipped":    strconv.Itoa(j.SkippedCount),
				"failed":     strconv.Itoa(j.FailedCount),
				"actor":      j.Actor,
				"created":    formatTime(j.CreatedAt),
			}}
		},
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Jobs",
		NavLabel: "JOBS",
		// Directly after the dashboard: the dashboard summarises these,
		// so the summary reads before the records it counts.
		NavOrder: 20,
		NavGroup: view.NavGroupViews,
		Summary:  "Every runbook dispatch this control plane has recorded.",
		Status:   view.StatusImplemented,
		// The one view where a snapshot is actively misleading. A fan-out
		// records its outcomes over seconds or minutes, so a jobs list
		// opened at the start of a change shows a page frozen at the moment
		// it loaded, with nothing to say it had. Five seconds is slow
		// enough to cost almost nothing and fast enough that a dispatch
		// appears while somebody is still looking for it.
		Refresh:          &view.RefreshSpec{Interval: 5 * time.Second, Active: stillRunning},
		IDField:          "job_id",
		StatusBadgeField: "state",
		Fields:           fields,
		Ops: view.Ops{
			List: &apispec.ListJobs,
			Get:  &apispec.GetJob,
			// No Create: dispatching is a runbook's action, offered on the
			// Runbooks view where an operator already has the runbook in
			// front of them. No Update and no Delete either: a job is a
			// historical record, and editing or erasing one would be
			// editing the audit trail. Stopping a running job is a
			// different thing entirely and is offered as an action below.
		},
		Actions: []view.RecordAction{cancelAction(canceller), relaunchAction(runner)},
		// Withdraws each control on the jobs it would fail on: Relaunch on
		// one still running, and on one that never came from a template;
		// Cancel on one that has already stopped. The two are exclusive by
		// construction, since both read the same terminalStates map from
		// opposite sides, so a record never offers both at once.
		Applies:   applies,
		Sections:  sections(jobs, entries),
		Downloads: downloads(entries, logs, jobs),
		// AWX's job page opens on Output, and this one does too. Somebody
		// opening a job has nearly always come to see what happened rather
		// than to re-read what it was asked to do, and the details are one
		// click away either way.
		DefaultTab: "Live output",
		Stream: &view.StreamSpec{
			Title: "Live output",
			// Built from the API's own prefix and the endpoint's own
			// pattern, so a change to either moves this with it rather
			// than leaving a hardcoded path that still parses and no
			// longer resolves.
			PathPattern: api.APIVersionPrefix + apispec.StreamJobLogs.Pattern,
		},
		Handlers: view.MustBind[*dispatch.Job](reader{jobs}, nil, projector),
	})
}

// firstNonEmpty returns the first of its arguments that is not empty, or
// the empty string. It exists so the reason column can prefer the Runner's
// own explanation without a caller writing the same conditional inline.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// formatTime renders a timestamp in the one format this UI uses.
//
// RFC 3339 in UTC, deliberately, rather than a localised or relative
// string. An operator reading a job list is usually correlating it against
// a log line or a change record from somewhere else, and "3 minutes ago" is
// the one format that cannot be correlated with anything.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// terminalStates are the job states after which nothing else will happen.
//
// Listed positively rather than as "not pending, not fanning out", so a state
// added later keeps refreshing until somebody decides it should not. Getting
// that default backwards would silently freeze a new state's record page.
var terminalStates = map[string]bool{
	"completed": true,
	"failed":    true,
	"canceled":  true,
}

// stillRunning reports whether a job's record page is worth refreshing.
//
// This is what makes a finished job free to leave open. A record page polls
// while its job is doing something and stops the moment it is not, because
// the refreshed fragment carries no trigger and there is nothing to cancel.
// An operator who opens a completed job from last week costs one request,
// not one every five seconds until they close the tab.
func stillRunning(row view.Row) bool {
	return !terminalStates[row.Cells["state"]]
}
