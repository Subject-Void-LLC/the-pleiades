// Package schedule is the scheduler: recurrence attached to something
// launchable, and the loop that turns "this is due" into a real job.
//
// The division of labour it rests on is the reason it stays small.
// Scheduling decides WHEN work becomes due; the existing durable dispatch
// plane decides HOW it runs. A due schedule is launched through the very
// same api.Dispatcher a person clicking Launch goes through, so a scheduled
// job inherits template resolution, credential binding, JetStream
// durability, fan-out fencing, per-device locking, runner pools, tracing
// and the activity stream without this package knowing about any of them.
// Nothing here reimplements dispatch, and nothing here reimplements
// election either: the Scanner takes an isLeader function, so this package
// does not import internal/election at all.
//
// Three properties are load-bearing and each has a test that would fail
// loudly if it regressed:
//
//   - A schedule fires at most once per occurrence, guaranteed by a unique
//     index on (schedule, occurrence_at) rather than by leader election,
//     which cannot promise it. See Scanner.fire.
//
//   - Occurrences missed while nothing was leading are COALESCED: one run
//     for the most recent, and a durable skipped row for each earlier one.
//     See Scanner.due.
//
//   - Recurrence matches AWX bug for bug, including across daylight saving
//     transitions, which internal/schedule/rrule proves against vectors
//     generated from the same library AWX uses.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/rrule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/zoneinfo"
)

// Errors this package returns. They are values rather than strings so an
// API handler can map them to status codes and a UI form can attach them
// to a field, neither by matching on error text.
var (
	// ErrNotFound is returned when no schedule has the given id.
	ErrNotFound = errors.New("schedule: no such schedule")

	// ErrAlreadyClaimed is returned when an occurrence has already been
	// claimed by another replica. It is not a failure: it is the
	// duplicate-fire guard doing its job, and the caller's correct
	// response is to move on to the next schedule.
	ErrAlreadyClaimed = errors.New("schedule: occurrence is already claimed")

	// ErrInvalid is returned when a schedule is not valid. It always wraps
	// a FieldError, so a caller can find out which field to blame.
	ErrInvalid = errors.New("schedule: schedule is not valid")

	// ErrNameTaken is returned when a schedule's name collides with an
	// existing one in the same organization.
	//
	// It is its own sentinel rather than a plain FieldError on the name
	// because it is the one validation failure that is a 409 rather than a
	// 400, and an API layer deciding that by matching on message text would
	// break the first time somebody reworded the message.
	ErrNameTaken = errors.New("schedule: that name is already taken in this organization")
)

// FieldError blames one named field for a validation failure.
//
// It carries the field name because every consumer needs it and none can
// derive it: the UI attaches the message to a control, the API puts it in a
// problem document, and the CLI prints it beside the input. This mirrors
// view.FieldFault, which serves the same purpose one layer out.
type FieldError struct {
	// Field is the schedule field at fault, named as the API and the form
	// name it, so no consumer has to translate.
	Field string

	// Message is written for the person who typed the value, not for a
	// log: it never carries a storage detail or a path.
	Message string

	// Cause is an optional sentinel a caller can test for when the field
	// alone does not say enough -- ErrNameTaken being the case that
	// exists, since it is the one validation failure an API answers with a
	// 409 rather than a 400.
	Cause error
}

// Error implements error.
func (e FieldError) Error() string { return e.Field + ": " + e.Message }

// Unwrap exposes Cause, so errors.Is finds a sentinel this error carries.
func (e FieldError) Unwrap() error { return e.Cause }

// Is reports every FieldError as an ErrInvalid, so callers can test for the
// category with errors.Is and recover the field with errors.As, whatever
// Cause it also carries.
func (e FieldError) Is(target error) bool { return target == ErrInvalid }

// Outcome is what happened at one occurrence.
type Outcome string

// The outcomes an occurrence can carry. They mirror the ent enum exactly;
// see internal/ent/schema/schedule_occurrence.go for why "claimed" is a
// real state rather than a transient.
const (
	OutcomeClaimed Outcome = "claimed"
	OutcomeFired   Outcome = "fired"
	OutcomeSkipped Outcome = "skipped"
)

// The reasons a skipped occurrence carries. They are a closed vocabulary
// rather than free text so a reader can aggregate them, and so no reason
// string can ever be built by interpolating data.
const (
	// ReasonMissedWindow is an occurrence that passed while no controller
	// was leading, superseded by a later one that did run.
	ReasonMissedWindow = "missed_window"

	// ReasonMissedWindowTruncated stands for many such occurrences at
	// once, when writing one row each would be its own outage. The row
	// carries how many it represents.
	ReasonMissedWindowTruncated = "missed_window_truncated"

	// ReasonLaunchFailed is a claimed occurrence whose launch did not
	// succeed. The schedule stays enabled: the next occurrence is a fresh
	// attempt, because the usual cause is transient.
	ReasonLaunchFailed = "launch_failed"
)

// Schedule is a recurrence attached to a template.
//
// Times are UTC on this struct without exception, including DTStart, and
// Timezone is what gives them meaning rather than what they are stored in.
// That split is the one thing to get right about this type: an occurrence's
// instant is absolute, but which instant a rule produces depends entirely
// on the zone the rule is read in.
type Schedule struct {
	// ID is the internal row id. Zero on a Schedule not yet created.
	ID int

	// ScheduleID is the stable opaque identifier callers reference.
	ScheduleID string

	// OrganizationID is the tenancy boundary. Every read is filtered by it.
	OrganizationID int

	// TemplateID is what this launches. The template carries the kind, so
	// this one field is how a schedule reaches every launchable kind.
	TemplateID int

	// SavedConfigID is the saved launch-override bundle to run with, or
	// zero for the template's own defaults.
	SavedConfigID int

	Name        string
	Description string
	Enabled     bool

	// RRule is the RFC 5545 recurrence, within the constraint set
	// internal/schedule/rrule enforces.
	RRule string

	// Exclusions are EXRULE and EXDATE lines subtracted from RRule.
	Exclusions []string

	// Timezone is an IANA zone name from the generated allowlist.
	Timezone string

	// DTStart is the recurrence anchor, in UTC.
	DTStart time.Time

	// DTEnd bounds the schedule from outside the rule. Nil is open-ended.
	DTEnd *time.Time

	// NextRun is the cached next occurrence, in UTC. Nil means the
	// recurrence has no further occurrences, which is a settled state
	// rather than an error.
	NextRun *time.Time

	// LastFired is the occurrence time of the most recent firing, in UTC,
	// and the point the coalescing policy measures forward from.
	LastFired *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Occurrence is one moment a schedule came due and what happened at it.
type Occurrence struct {
	ID              int
	ScheduleID      string
	OccurrenceAt    time.Time
	Outcome         Outcome
	Reason          string
	SuppressedCount int
	JobID           string
	CreatedAt       time.Time
}

// Location resolves the schedule's zone through the allowlist.
func (s Schedule) Location() (*time.Location, error) {
	return zoneinfo.Load(s.Timezone)
}

// RuleSet parses the schedule's recurrence and exclusions together.
func (s Schedule) RuleSet() (rrule.RuleSet, error) {
	return rrule.ParseRuleSet(s.RRule, s.Exclusions)
}

// Validate checks everything that can be known without touching storage,
// and is the single gate every write path goes through.
//
// It runs at SAVE time deliberately. A recurrence is operator-supplied
// input that the scanner later expands inside its own loop, so a rule that
// cannot be expanded safely must never reach the database -- once there, it
// is not one broken schedule but a stall affecting every schedule in the
// deployment. This mirrors what launch.Injectors.Validate already does for
// templates: compile at save, never at run.
func (s Schedule) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return FieldError{Field: "name", Message: "A schedule needs a name."}
	}
	if s.TemplateID == 0 {
		return FieldError{Field: "template", Message: "Choose what this schedule runs."}
	}
	if s.DTStart.IsZero() {
		return FieldError{Field: "dtstart", Message: "A schedule needs a start date and time."}
	}

	loc, err := s.Location()
	if err != nil {
		return FieldError{
			Field:   "timezone",
			Message: fmt.Sprintf("%q is not a time zone this server knows.", s.Timezone),
		}
	}

	set, err := s.RuleSet()
	if err != nil {
		field := "rrule"
		if !errors.Is(err, rrule.ErrMalformed) && !errors.Is(err, rrule.ErrUnsupported) {
			field = "rrule"
		}
		if strings.Contains(err.Error(), "exclusion") {
			field = "exclusions"
		}
		return FieldError{Field: field, Message: recurrenceMessage(err)}
	}

	if s.DTEnd != nil && !s.DTEnd.After(s.DTStart) {
		return FieldError{Field: "dtend", Message: "The end must come after the start."}
	}

	// A rule that parses can still name no real instant, and a schedule
	// that will never fire is exactly the silent failure this refuses to
	// ship. Expanding one occurrence is the cheapest possible proof that
	// it can produce any.
	if _, err := set.Expand(s.DTStart.In(loc), s.DTStart.In(loc), loc, 1); err != nil {
		if errors.Is(err, rrule.ErrUnsatisfiable) {
			return FieldError{
				Field:   "rrule",
				Message: "This recurrence never happens. Check the day and month against the calendar.",
			}
		}
		return FieldError{Field: "rrule", Message: recurrenceMessage(err)}
	}
	return nil
}

// recurrenceMessage turns a parser error into something written for the
// operator, keeping the parser's own detail (which names the offending
// part) but not its package prefix.
func recurrenceMessage(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "rrule: ")
	switch {
	case errors.Is(err, rrule.ErrUnsupported):
		return "This scheduler does not support that recurrence: " + msg
	default:
		return "That recurrence could not be read: " + msg
	}
}

// ComputeNextRun returns the first occurrence strictly after `after`, in
// UTC, or nil when the schedule has no further occurrences.
//
// It is the one place next_run is derived, so the cached column and the
// preview endpoint can never disagree about what a rule means. A disabled
// schedule has no next run at all: leaving a stale one would show an
// operator a time nothing will happen at.
func (s Schedule) ComputeNextRun(after time.Time) (*time.Time, error) {
	if !s.Enabled {
		return nil, nil
	}
	loc, err := s.Location()
	if err != nil {
		return nil, err
	}
	set, err := s.RuleSet()
	if err != nil {
		return nil, err
	}

	occurrences, err := set.Expand(s.DTStart.In(loc), after.In(loc).Add(time.Nanosecond), loc, 1)
	if err != nil {
		if errors.Is(err, rrule.ErrUnsatisfiable) {
			return nil, nil
		}
		return nil, err
	}
	if len(occurrences) == 0 {
		return nil, nil
	}

	next := occurrences[0].UTC()
	// DTEnd bounds the schedule from outside the rule, so it is applied
	// here rather than folded into the recurrence: it is an operator
	// saying "stop after then", not part of what the author wrote.
	if s.DTEnd != nil && next.After(*s.DTEnd) {
		return nil, nil
	}
	return &next, nil
}

// Preview returns up to limit upcoming occurrences at or after `from`, in
// the schedule's own location.
//
// The occurrences come back zoned rather than in UTC because the caller is
// showing them to somebody: PLAN.md Section 30.1 requires the preview to
// give both local and UTC time, and only a zoned time can produce both. A
// UTC time cannot be turned back into the right local reading without the
// zone, which is precisely the information a preview exists to confirm.
func (s Schedule) Preview(from time.Time, limit int) ([]time.Time, error) {
	loc, err := s.Location()
	if err != nil {
		return nil, err
	}
	set, err := s.RuleSet()
	if err != nil {
		return nil, err
	}
	occurrences, err := set.Expand(s.DTStart.In(loc), from.In(loc), loc, limit)
	if err != nil {
		if errors.Is(err, rrule.ErrUnsatisfiable) {
			return nil, nil
		}
		return nil, err
	}
	if s.DTEnd == nil {
		return occurrences, nil
	}
	kept := occurrences[:0]
	for _, o := range occurrences {
		if o.After(*s.DTEnd) {
			break
		}
		kept = append(kept, o)
	}
	return kept, nil
}

// AnyOrganization disables the tenancy filter on a read.
//
// It exists because this platform does not yet derive a tenant from a
// request. auth.Identity carries a subject, a role and scopes, but no
// organization, and internal/auth's per-target ScopeResolver is not in any
// running chain -- its own doc comment records why: "this one needs a
// ScopeTarget an HTTP route has none to give it." The `organization` query
// parameter the Template surface accepts is therefore a filter a caller
// chooses, not a boundary the server enforces.
//
// Rather than pretend otherwise, this is named. Every Store method still
// takes an organization and still filters on it, so the UI and any future
// tenant-aware caller get real scoping for free; the API handlers pass this
// sentinel and are the one visible, greppable place the platform's existing
// gap shows up in this feature. When a request grows a tenant, the fix is
// to delete the uses of this constant, and a search for its name finds
// every one of them.
//
// It is deliberately NOT the same as a missing value being ignored: a
// caller passing a real organization gets a real filter, and the tests
// covering cross-tenant reads pass real ids.
const AnyOrganization = 0

// Store is the persistence port. internal/schedule/ent_store.go is its one
// adapter; a caller holds this interface so nothing above it imports ent.
type Store interface {
	// Create persists a new schedule and returns it with its assigned ids
	// and computed NextRun. It validates first and refuses an invalid one.
	Create(ctx context.Context, s Schedule) (Schedule, error)

	// Update replaces a schedule's mutable fields and recomputes NextRun.
	Update(ctx context.Context, s Schedule) (Schedule, error)

	// Get returns one schedule by its opaque id, scoped to an
	// organization. A schedule in another tenant reads as ErrNotFound
	// rather than as a permission error: telling a caller that an id they
	// cannot see exists is itself a disclosure.
	Get(ctx context.Context, orgID int, scheduleID string) (Schedule, error)

	// List returns a page of an organization's schedules, ordered by name,
	// resuming after a cursor.
	List(ctx context.Context, orgID int, after string, limit int) ([]Schedule, error)

	// Delete removes a schedule and its occurrence history.
	Delete(ctx context.Context, orgID int, scheduleID string) error

	// ListDue returns enabled schedules whose NextRun is at or before now,
	// in (next_run, schedule_id) order, resuming after cursor.
	//
	// The cursor is keyset rather than an offset, which PLAN.md Section
	// 30.1 requires by name: "a growing pending set with offset paging
	// degrades quadratically". It is unfiltered by organization on purpose
	// -- the scanner runs as the deployment, not as a tenant.
	ListDue(ctx context.Context, now time.Time, cursor DueCursor, limit int) ([]Schedule, error)

	// ClaimOccurrence atomically claims one occurrence, returning
	// ErrAlreadyClaimed if another replica already holds it.
	//
	// This is the duplicate-fire guard. It must be a single insert relying
	// on a unique constraint, never a read-then-write: two replicas that
	// both read "unclaimed" would both proceed.
	ClaimOccurrence(ctx context.Context, scheduleID string, occurrenceAt time.Time) (Occurrence, error)

	// ResolveOccurrence records what happened to a claimed occurrence.
	ResolveOccurrence(ctx context.Context, occurrenceID int, outcome Outcome, reason, jobID string) error

	// RecordSkip writes a skipped occurrence directly, for one that was
	// never claimed because it was never going to run.
	RecordSkip(ctx context.Context, scheduleID string, occurrenceAt time.Time, reason string, suppressed int) error

	// MarkFired advances a schedule's LastFired and NextRun together.
	//
	// One method rather than two, because the pair is what makes the scan
	// converge: advancing LastFired without NextRun would re-select the
	// same schedule on the next tick forever.
	MarkFired(ctx context.Context, scheduleID string, firedAt time.Time, nextRun *time.Time) error

	// ListOccurrences returns a schedule's history, most recent first.
	ListOccurrences(ctx context.Context, orgID int, scheduleID string, limit int) ([]Occurrence, error)
}

// DueCursor is a keyset position in the due-schedule scan.
//
// It is a pair rather than a single value because next_run is not unique:
// two schedules can come due in the same second, and a cursor on the
// timestamp alone would either skip one or repeat it at every page
// boundary. The schedule id breaks the tie and is unique, so the pair is
// total. This is the same reasoning inventory's device_id cursor records,
// which needed no tiebreaker only because it paged on a unique column.
type DueCursor struct {
	// NextRun is the last next_run yielded, zero at the start of a scan.
	NextRun time.Time

	// ScheduleID is the last schedule id yielded at that next_run.
	ScheduleID string
}

// Zero reports whether this cursor is the start of a scan.
func (c DueCursor) Zero() bool { return c.NextRun.IsZero() && c.ScheduleID == "" }
