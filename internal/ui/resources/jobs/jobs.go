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
	"errors"
	"strconv"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// Name is this view's registration key and URL segment.
const Name = "jobs"

// fields drive the table, the dispatch form, the detail list, validation
// and the mobile card layout from one declaration.
//
// Only two are writable, and that is the entire dispatch API: a launch
// names a group and a runbook, and everything else on a job is something
// the platform decided rather than something a caller may assert.
var fields = []view.Field{
	{
		Name: "job_id", Label: "JOB", Kind: view.KindText,
		InList: true, MobilePrimary: true,
		Help: "The server-generated identifier this job is polled on.",
	},
	{
		Name: "runbook", Label: "RUNBOOK", Kind: view.KindText,
		Required: true, MaxLen: 253, Autocomplete: "off",
		Help:   "The runbook to dispatch, by id.",
		InList: true, InForm: true,
	},
	{
		Name: "group", Label: "GROUP", Kind: view.KindText,
		Required: true, MaxLen: 253, Autocomplete: "off",
		Help:   "The inventory group whose devices this runs against.",
		InList: true, InForm: true,
	},
	{
		Name: "state", Label: "STATE", Kind: view.KindBadge,
		InList: true, BadgeClass: stateBadge,
	},
	{Name: "dispatched", Label: "DISPATCHED", Kind: view.KindReadOnly, InList: true},
	{Name: "skipped", Label: "SKIPPED", Kind: view.KindReadOnly},
	{Name: "failed", Label: "FAILED", Kind: view.KindReadOnly},
	{Name: "actor", Label: "ACTOR", Kind: view.KindReadOnly},
	{Name: "created", Label: "CREATED", Kind: view.KindTimestamp, InList: true},
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
	case "fanning_out":
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

// launcher adapts the dispatch path to the view's Creator.
//
// It calls api.Dispatcher.Launch, the same method the JSON API's own
// handler calls, rather than reimplementing resolve-persist-publish. A view
// layer that reimplemented a write path would have two orderings to keep in
// agreement, and the one that drifts is always the one with fewer readers.
type launcher struct{ dispatcher *api.Dispatcher }

func (l launcher) Create(ctx context.Context, job *dispatch.Job) (string, error) {
	// The actor is read from the request's identity, never from the
	// submission. A caller who could name the actor could forge the audit
	// trail this field exists to be, so the form does not declare it and
	// this is the only place it is set.
	identity, ok := api.IdentityFromContext(ctx)
	if !ok || identity == nil {
		return "", errors.New("no identity on the request context")
	}

	id, err := l.dispatcher.Launch(ctx, identity.Subject, job.GroupName, job.RunbookID)
	if errors.Is(err, runbook.ErrNotFound) {
		// The submitter's mistake, not the platform's. Blaming the field
		// puts the message on the control that caused it instead of
		// answering a typo with an error page.
		return "", view.FieldFault{
			Field:   "runbook",
			Message: "No runbook with that id exists.",
		}
	}
	return id, err
}

// Register wires this view over the live job store and dispatcher.
func Register(jobs dispatch.JobStore, dispatcher *api.Dispatcher) error {
	projector := view.Projector[*dispatch.Job]{
		Row: func(j *dispatch.Job) view.Row {
			if j == nil {
				return view.Row{}
			}
			return view.Row{ID: j.JobID, Cells: view.Cells{
				"job_id":     j.JobID,
				"runbook":    j.RunbookID,
				"group":      j.GroupName,
				"state":      j.State,
				"dispatched": strconv.Itoa(j.DispatchedCount),
				"skipped":    strconv.Itoa(j.SkippedCount),
				"failed":     strconv.Itoa(j.FailedCount),
				"actor":      j.Actor,
				"created":    formatTime(j.CreatedAt),
			}}
		},
		Form: func(j *dispatch.Job) map[string]string {
			// Reached only by an edit form, which this resource does not
			// offer. It is supplied because Bind requires the pair, and
			// returning the launch parameters is the honest answer to
			// "what would prefill a form for this record".
			if j == nil {
				return map[string]string{}
			}
			return map[string]string{"runbook": j.RunbookID, "group": j.GroupName}
		},
		Bind: func(v view.Values) (*dispatch.Job, view.FieldErrors) {
			// Actor is filled in by the handler from the session, never
			// read off the submission: a caller who could name the actor
			// could forge the audit trail this field exists to be.
			return &dispatch.Job{
				RunbookID: v.Get("runbook"),
				GroupName: v.Get("group"),
			}, view.FieldErrors{}
		},
	}

	return view.Register(view.Descriptor{
		Name:     Name,
		Title:    "Jobs",
		NavLabel: "JOBS",
		NavOrder: 30,
		Summary:  "Every runbook dispatch this control plane has recorded.",
		Status:   view.StatusImplemented,
		IDField:  "job_id",
		Fields:   fields,
		Ops: view.Ops{
			List:   &apispec.ListJobs,
			Get:    &apispec.GetJob,
			Create: &apispec.DispatchRunbook,
			// No Update and no Delete. There is no job:write scope, no
			// JobStore.Cancel and no cancellation path anywhere in this
			// build, so offering either would be a button for a route
			// nobody mounted.
		},
		Stream: &view.StreamSpec{
			Title: "Live output",
			// Built from the API's own prefix and the endpoint's own
			// pattern, so a change to either moves this with it rather
			// than leaving a hardcoded path that still parses and no
			// longer resolves.
			PathPattern: api.APIVersionPrefix + apispec.StreamJobLogs.Pattern,
		},
		Handlers: view.MustBindCreatable(reader{jobs}, launcher{dispatcher}, projector),
	})
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
