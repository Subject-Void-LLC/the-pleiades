package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entorg "github.com/Subject-Void-LLC/the-pleiades/internal/ent/organization"
	entschedule "github.com/Subject-Void-LLC/the-pleiades/internal/ent/schedule"
	entoccurrence "github.com/Subject-Void-LLC/the-pleiades/internal/ent/scheduleoccurrence"
	enttemplate "github.com/Subject-Void-LLC/the-pleiades/internal/ent/template"
)

// Page-size bounds, matching internal/launch's own so a caller moving
// between the two resources meets one convention.
const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// entStore is the ent-backed Store.
type entStore struct {
	client *ent.Client
}

// NewEntStore builds the ent-backed Store over client.
func NewEntStore(client *ent.Client) Store {
	return &entStore{client: client}
}

// Create validates, resolves the template's tenancy, and persists.
func (s *entStore) Create(ctx context.Context, sched Schedule) (Schedule, error) {
	if err := sched.Validate(); err != nil {
		return Schedule{}, err
	}

	orgID, err := s.templateOrg(ctx, sched.TemplateID)
	if err != nil {
		return Schedule{}, err
	}
	// The tenancy check ent cannot express. A schedule whose template
	// belongs to another organization would let one tenant launch
	// another's work on a timer, with every individual step passing its
	// own check -- the shape internal/ent/schema/template.go already
	// records for the inventory edge.
	if sched.OrganizationID != 0 && sched.OrganizationID != orgID {
		return Schedule{}, FieldError{
			Field:   "template",
			Message: "That template belongs to a different organization.",
		}
	}
	sched.OrganizationID = orgID

	next, err := sched.ComputeNextRun(time.Now().UTC())
	if err != nil {
		return Schedule{}, err
	}

	create := s.client.Schedule.Create().
		SetName(sched.Name).
		SetDescription(sched.Description).
		SetEnabled(sched.Enabled).
		SetRrule(sched.RRule).
		SetExclusions(sched.Exclusions).
		SetTimezone(sched.Timezone).
		SetDtstart(sched.DTStart.UTC()).
		SetOrganizationID(orgID).
		SetTemplateID(sched.TemplateID)
	if sched.DTEnd != nil {
		create = create.SetDtend(sched.DTEnd.UTC())
	}
	if next != nil {
		create = create.SetNextRun(*next)
	}
	if sched.SavedConfigID != 0 {
		create = create.SetSavedConfigID(sched.SavedConfigID)
	}

	row, err := create.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return Schedule{}, FieldError{
				Field:   "name",
				Message: "A schedule with that name already exists in this organization.",
				Cause:   ErrNameTaken,
			}
		}
		return Schedule{}, fmt.Errorf("schedule: create: %w", err)
	}
	return s.Get(ctx, orgID, row.ScheduleID)
}

// Update replaces the mutable fields and recomputes next_run.
//
// next_run is always recomputed rather than carried from the caller,
// because every field an edit can touch changes what it should be: the
// rule, the exclusions, the zone, the anchor, the end, and enabled itself.
// Trusting a caller-supplied value here would let a stale form post pin a
// schedule to a time its own rule no longer produces.
func (s *entStore) Update(ctx context.Context, sched Schedule) (Schedule, error) {
	if err := sched.Validate(); err != nil {
		return Schedule{}, err
	}

	existing, err := s.Get(ctx, sched.OrganizationID, sched.ScheduleID)
	if err != nil {
		return Schedule{}, err
	}

	orgID, err := s.templateOrg(ctx, sched.TemplateID)
	if err != nil {
		return Schedule{}, err
	}
	if orgID != existing.OrganizationID {
		return Schedule{}, FieldError{
			Field:   "template",
			Message: "That template belongs to a different organization.",
		}
	}

	next, err := sched.ComputeNextRun(time.Now().UTC())
	if err != nil {
		return Schedule{}, err
	}

	update := s.client.Schedule.UpdateOneID(existing.ID).
		SetName(sched.Name).
		SetDescription(sched.Description).
		SetEnabled(sched.Enabled).
		SetRrule(sched.RRule).
		SetExclusions(sched.Exclusions).
		SetTimezone(sched.Timezone).
		SetDtstart(sched.DTStart.UTC()).
		SetTemplateID(sched.TemplateID).
		ClearNextRun().
		ClearDtend()
	if sched.DTEnd != nil {
		update = update.SetDtend(sched.DTEnd.UTC())
	}
	if next != nil {
		update = update.SetNextRun(*next)
	}
	if sched.SavedConfigID != 0 {
		update = update.SetSavedConfigID(sched.SavedConfigID)
	} else {
		update = update.ClearSavedConfig()
	}

	if _, err := update.Save(ctx); err != nil {
		if ent.IsConstraintError(err) {
			return Schedule{}, FieldError{
				Field:   "name",
				Message: "A schedule with that name already exists in this organization.",
				Cause:   ErrNameTaken,
			}
		}
		return Schedule{}, fmt.Errorf("schedule: update: %w", err)
	}
	return s.Get(ctx, existing.OrganizationID, existing.ScheduleID)
}

// Get returns one schedule, scoped to an organization.
func (s *entStore) Get(ctx context.Context, orgID int, scheduleID string) (Schedule, error) {
	row, err := scopeToOrg(s.client.Schedule.Query(), orgID).
		Where(entschedule.ScheduleIDEQ(scheduleID)).
		WithTemplate().
		WithSavedConfig().
		WithOrganization().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return Schedule{}, ErrNotFound
		}
		return Schedule{}, fmt.Errorf("schedule: get %s: %w", scheduleID, err)
	}
	return toDomain(row), nil
}

// List returns a page of an organization's schedules, ordered by name.
func (s *entStore) List(ctx context.Context, orgID int, after string, limit int) ([]Schedule, error) {
	limit = clampPageSize(limit)
	q := scopeToOrg(s.client.Schedule.Query(), orgID).
		WithTemplate().
		WithSavedConfig().
		WithOrganization().
		Order(ent.Asc(entschedule.FieldName), ent.Asc(entschedule.FieldScheduleID)).
		Limit(limit)
	if after != "" {
		q = q.Where(entschedule.ScheduleIDGT(after))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("schedule: list: %w", err)
	}
	out := make([]Schedule, 0, len(rows))
	for _, r := range rows {
		out = append(out, toDomain(r))
	}
	return out, nil
}

// Delete removes a schedule and, with it, its occurrence history.
func (s *entStore) Delete(ctx context.Context, orgID int, scheduleID string) error {
	existing, err := s.Get(ctx, orgID, scheduleID)
	if err != nil {
		return err
	}
	// The occurrence rows go first and explicitly rather than by cascade:
	// the edge is declared without one, so a database-level cascade would
	// be a second, invisible rule about the same relationship.
	if _, err := s.client.ScheduleOccurrence.Delete().
		Where(entoccurrence.HasScheduleWith(entschedule.IDEQ(existing.ID))).
		Exec(ctx); err != nil {
		return fmt.Errorf("schedule: delete occurrences for %s: %w", scheduleID, err)
	}
	if err := s.client.Schedule.DeleteOneID(existing.ID).Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("schedule: delete %s: %w", scheduleID, err)
	}
	return nil
}

// ListDue returns enabled, due schedules in keyset order.
//
// The predicate is the keyset half of PLAN.md Section 30.1's requirement,
// written out rather than expressed as an offset: strictly-later next_run,
// OR the same next_run with a strictly-later schedule id. That second
// clause is what makes the cursor total across schedules sharing a
// next_run, which is common -- everything created with the same recurrence
// comes due together.
func (s *entStore) ListDue(ctx context.Context, now time.Time, cursor DueCursor, limit int) ([]Schedule, error) {
	limit = clampPageSize(limit)
	q := s.client.Schedule.Query().
		Where(
			entschedule.EnabledEQ(true),
			entschedule.NextRunNotNil(),
			entschedule.NextRunLTE(now.UTC()),
		).
		WithTemplate().
		WithSavedConfig().
		WithOrganization().
		Order(ent.Asc(entschedule.FieldNextRun), ent.Asc(entschedule.FieldScheduleID)).
		Limit(limit)

	if !cursor.Zero() {
		q = q.Where(entschedule.Or(
			entschedule.NextRunGT(cursor.NextRun.UTC()),
			entschedule.And(
				entschedule.NextRunEQ(cursor.NextRun.UTC()),
				entschedule.ScheduleIDGT(cursor.ScheduleID),
			),
		))
	}

	rows, err := q.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("schedule: list due: %w", err)
	}
	out := make([]Schedule, 0, len(rows))
	for _, r := range rows {
		out = append(out, toDomain(r))
	}
	return out, nil
}

// ClaimOccurrence is the duplicate-fire guard.
//
// It is one insert, and it must stay one insert. The unique index on
// (schedule, occurrence_at) is what decides the race; a read-then-write
// would let two replicas both observe "unclaimed" and both proceed, which
// is precisely the failure this exists to prevent. A constraint violation
// here is the expected, healthy outcome for the losing replica, not an
// error to report.
func (s *entStore) ClaimOccurrence(ctx context.Context, scheduleID string, occurrenceAt time.Time) (Occurrence, error) {
	sched, err := s.byScheduleID(ctx, scheduleID)
	if err != nil {
		return Occurrence{}, err
	}
	row, err := s.client.ScheduleOccurrence.Create().
		SetOccurrenceAt(occurrenceAt.UTC()).
		SetOutcome(entoccurrence.OutcomeClaimed).
		SetScheduleID(sched.ID).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return Occurrence{}, ErrAlreadyClaimed
		}
		return Occurrence{}, fmt.Errorf("schedule: claim occurrence: %w", err)
	}
	return occurrenceToDomain(row, scheduleID), nil
}

// ResolveOccurrence records what happened to a claimed occurrence.
func (s *entStore) ResolveOccurrence(ctx context.Context, occurrenceID int, outcome Outcome, reason, jobID string) error {
	entOutcome, err := toEntOutcome(outcome)
	if err != nil {
		return err
	}
	update := s.client.ScheduleOccurrence.UpdateOneID(occurrenceID).
		SetOutcome(entOutcome).
		SetReason(reason)
	if jobID != "" {
		update = update.SetJobID(jobID)
	}
	if err := update.Exec(ctx); err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("schedule: resolve occurrence %d: %w", occurrenceID, err)
	}
	return nil
}

// RecordSkip writes a skipped occurrence that was never claimed.
//
// A constraint violation is swallowed rather than reported: it means the
// occurrence already has a row, so the history already answers the
// question this call was trying to answer. Failing here would abort a
// recovery over a row that already says the right thing.
func (s *entStore) RecordSkip(ctx context.Context, scheduleID string, occurrenceAt time.Time, reason string, suppressed int) error {
	sched, err := s.byScheduleID(ctx, scheduleID)
	if err != nil {
		return err
	}
	err = s.client.ScheduleOccurrence.Create().
		SetOccurrenceAt(occurrenceAt.UTC()).
		SetOutcome(entoccurrence.OutcomeSkipped).
		SetReason(reason).
		SetSuppressedCount(suppressed).
		SetScheduleID(sched.ID).
		Exec(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil
		}
		return fmt.Errorf("schedule: record skip: %w", err)
	}
	return nil
}

// MarkFired advances last_fired and next_run together.
func (s *entStore) MarkFired(ctx context.Context, scheduleID string, firedAt time.Time, nextRun *time.Time) error {
	sched, err := s.byScheduleID(ctx, scheduleID)
	if err != nil {
		return err
	}
	update := s.client.Schedule.UpdateOneID(sched.ID).ClearNextRun()
	if !firedAt.IsZero() {
		update = update.SetLastFired(firedAt.UTC())
	}
	if nextRun != nil {
		update = update.SetNextRun(nextRun.UTC())
	}
	if err := update.Exec(ctx); err != nil {
		return fmt.Errorf("schedule: mark fired %s: %w", scheduleID, err)
	}
	return nil
}

// ListOccurrences returns a schedule's history, most recent first.
func (s *entStore) ListOccurrences(ctx context.Context, orgID int, scheduleID string, limit int) ([]Occurrence, error) {
	sched, err := s.Get(ctx, orgID, scheduleID)
	if err != nil {
		return nil, err
	}
	rows, err := s.client.ScheduleOccurrence.Query().
		Where(entoccurrence.HasScheduleWith(entschedule.IDEQ(sched.ID))).
		Order(ent.Desc(entoccurrence.FieldOccurrenceAt)).
		Limit(clampPageSize(limit)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("schedule: list occurrences: %w", err)
	}
	out := make([]Occurrence, 0, len(rows))
	for _, r := range rows {
		out = append(out, occurrenceToDomain(r, scheduleID))
	}
	return out, nil
}

// byScheduleID resolves an opaque id to a row WITHOUT a tenancy filter.
//
// It is unexported and used only by the scanner-facing methods, which run
// as the deployment rather than as a tenant: the scanner has already
// selected these schedules through ListDue and has no organization to
// filter by. Every caller-facing method goes through Get, which does
// filter. Keeping the two apart in separate methods is what stops the
// unscoped query being reachable by accident from a request path.
func (s *entStore) byScheduleID(ctx context.Context, scheduleID string) (*ent.Schedule, error) {
	row, err := s.client.Schedule.Query().
		Where(entschedule.ScheduleIDEQ(scheduleID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("schedule: lookup %s: %w", scheduleID, err)
	}
	return row, nil
}

// templateOrg returns the organization a template belongs to, which is
// where a schedule's own tenancy comes from: a template's organization edge
// is required, so a schedule that names one has a tenant by construction
// rather than by somebody remembering to set it.
func (s *entStore) templateOrg(ctx context.Context, templateID int) (int, error) {
	row, err := s.client.Template.Query().
		Where(enttemplate.IDEQ(templateID)).
		WithOrganization().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return 0, FieldError{Field: "template", Message: "That template does not exist."}
		}
		return 0, fmt.Errorf("schedule: resolve template %d: %w", templateID, err)
	}
	if row.Edges.Organization == nil {
		return 0, fmt.Errorf("schedule: template %d has no organization", templateID)
	}
	return row.Edges.Organization.ID, nil
}

// toDomain converts a persisted row to the domain type.
func toDomain(row *ent.Schedule) Schedule {
	s := Schedule{
		ID:          row.ID,
		ScheduleID:  row.ScheduleID,
		Name:        row.Name,
		Description: row.Description,
		Enabled:     row.Enabled,
		RRule:       row.Rrule,
		Exclusions:  row.Exclusions,
		Timezone:    row.Timezone,
		DTStart:     row.Dtstart.UTC(),
		DTEnd:       utcPtr(row.Dtend),
		NextRun:     utcPtr(row.NextRun),
		LastFired:   utcPtr(row.LastFired),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
	if row.Edges.Organization != nil {
		s.OrganizationID = row.Edges.Organization.ID
	}
	if row.Edges.Template != nil {
		s.TemplateID = row.Edges.Template.ID
	}
	if row.Edges.SavedConfig != nil {
		s.SavedConfigID = row.Edges.SavedConfig.ID
	}
	return s
}

// occurrenceToDomain converts a persisted occurrence row.
func occurrenceToDomain(row *ent.ScheduleOccurrence, scheduleID string) Occurrence {
	return Occurrence{
		ID:              row.ID,
		ScheduleID:      scheduleID,
		OccurrenceAt:    row.OccurrenceAt.UTC(),
		Outcome:         Outcome(row.Outcome),
		Reason:          row.Reason,
		SuppressedCount: row.SuppressedCount,
		JobID:           row.JobID,
		CreatedAt:       row.CreatedAt,
	}
}

// toEntOutcome maps a domain outcome onto the generated enum, refusing an
// unknown value rather than defaulting it: a default here would write a
// wrong audit row, which is worse than failing to write one.
func toEntOutcome(o Outcome) (entoccurrence.Outcome, error) {
	switch o {
	case OutcomeClaimed:
		return entoccurrence.OutcomeClaimed, nil
	case OutcomeFired:
		return entoccurrence.OutcomeFired, nil
	case OutcomeSkipped:
		return entoccurrence.OutcomeSkipped, nil
	default:
		return "", fmt.Errorf("schedule: %q is not a known occurrence outcome", o)
	}
}

// utcPtr normalises an optional timestamp to UTC, preserving nil.
func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// scopeToOrg applies the tenancy filter to a schedule query, written once
// so a new method cannot forget it or spell it slightly differently.
//
// AnyOrganization means no filter; see that constant for why the option
// exists and what it costs.
func scopeToOrg(q *ent.ScheduleQuery, orgID int) *ent.ScheduleQuery {
	if orgID == AnyOrganization {
		return q
	}
	return q.Where(entschedule.HasOrganizationWith(entorg.IDEQ(orgID)))
}

// clampPageSize applies the page-size bounds.
func clampPageSize(limit int) int {
	switch {
	case limit <= 0:
		return defaultPageSize
	case limit > maxPageSize:
		return maxPageSize
	default:
		return limit
	}
}
