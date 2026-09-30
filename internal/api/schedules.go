// This file exposes Schedules over the API: when automation runs without
// somebody pressing launch.
//
// It is the administration surface only. Creating, editing and deleting a
// schedule live here under schedule:read and schedule:write; the loop that
// actually fires them lives in internal/schedule and is driven by
// cmd/controller behind the scheduler lease. Nothing in this file launches
// anything, which is why none of it holds a Dispatcher.
//
// Two of these endpoints exist purely so an operator can be sure before
// they commit. A recurrence rule is genuinely hard to read -- somebody can
// write "FREQ=MONTHLY;BYDAY=-1FR" meaning the last Friday and get it, or
// meaning the fourth Friday and not notice for a month -- and a wrong
// schedule is wrong silently, out of hours, repeatedly. Preview and
// zoneinfo turn that into something checkable at the moment of writing.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule/zoneinfo"
)

// defaultPreviewCount and maxPreviewCount bound a preview expansion.
//
// Ten is what fits on a form beside the fields being edited, and is the
// number the Release Gate itself is stated in terms of. The ceiling exists
// because a preview is unauthenticated work done on operator-supplied input:
// the recurrence engine bounds its own expansion, but there is no reason to
// let one request ask for a hundred thousand occurrences to marshal.
const (
	defaultPreviewCount = 10
	maxPreviewCount     = 100
)

// ScheduleStore is the slice of internal/schedule's store these handlers
// need. It is the administration half: the scanner's own methods
// (ListDue, ClaimOccurrence, ResolveOccurrence, RecordSkip, MarkFired) are
// deliberately absent, so nothing reachable from an HTTP request can claim
// or fire an occurrence directly.
type ScheduleStore interface {
	Create(ctx context.Context, s schedule.Schedule, reach launchable.Reach) (schedule.Schedule, error)
	Update(ctx context.Context, s schedule.Schedule, reach launchable.Reach) (schedule.Schedule, error)
	Get(ctx context.Context, orgID int, scheduleID string) (schedule.Schedule, error)
	List(ctx context.Context, orgID int, after string, limit int) ([]schedule.Schedule, error)
	Delete(ctx context.Context, orgID int, scheduleID string) error
	ListOccurrences(ctx context.Context, orgID int, scheduleID string, limit int) ([]schedule.Occurrence, error)
}

// ScheduleHandler serves the Schedule resource.
type ScheduleHandler struct {
	schedules ScheduleStore
	templates scheduleTemplateReader
	logger    *slog.Logger
}

// scheduleTemplateReader is what this handler needs in order to honor the
// deprecated "template" field: the launchable a template id stands for.
//
// It is internal/launch's store in a real controller, and it exists only for
// that alias. A caller using the current field names never reaches it.
type scheduleTemplateReader interface {
	Get(ctx context.Context, id int) (launch.Template, error)
}

// NewScheduleHandler builds the handlers over schedules. A nil logger falls
// back to the default, matching every other handler here.
//
// templates may be nil, and then the deprecated "template" field is refused
// with a message naming the field to use instead, rather than accepted and
// silently ignored.
func NewScheduleHandler(schedules ScheduleStore, templates scheduleTemplateReader, logger *slog.Logger) *ScheduleHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScheduleHandler{schedules: schedules, templates: templates, logger: logger}
}

// scheduleDTO is the wire projection of one schedule.
type scheduleDTO struct {
	LinkSet
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Enabled      bool     `json:"enabled"`
	RRule        string   `json:"rrule"`
	Exclusions   []string `json:"exclusions,omitempty"`
	Timezone     string   `json:"timezone"`
	DTStart      string   `json:"dtstart"`
	DTEnd        string   `json:"dtend,omitempty"`
	NextRun      string   `json:"next_run,omitempty"`
	NextRunLocal string   `json:"next_run_local,omitempty"`
	LastFired    string   `json:"last_fired,omitempty"`

	// UnifiedJobTemplate is what this schedule launches: a launchable id,
	// unique across every sort of launchable thing. AWX's own field name, so
	// an imported schedule needs no translation.
	//
	// It replaces "template", which is deliberately no longer sent: a field
	// that would be absent for a project sync is a field every reader has to
	// special-case, and the two names beside each other would invite reading
	// the one that is sometimes empty.
	//
	// UnifiedJobTemplateType is what SORT of thing it points at
	// ("job_template", "project"), which is deliberately not called
	// "unified_job_type": that name means what sort of RUN something produced
	// ("job", "project_update") and it is what an occurrence carries. Two
	// different questions must not share a field name.
	UnifiedJobTemplate     int    `json:"unified_job_template"`
	UnifiedJobTemplateName string `json:"unified_job_template_name,omitempty"`
	UnifiedJobTemplateType string `json:"unified_job_template_type,omitempty"`

	SavedConfig  int    `json:"saved_config,omitempty"`
	Organization int    `json:"organization"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type scheduleListDTO struct {
	LinkSet
	Schedules []scheduleDTO `json:"schedules"`
}

type occurrenceDTO struct {
	OccurrenceAt    string `json:"occurrence_at"`
	Outcome         string `json:"outcome"`
	Reason          string `json:"reason,omitempty"`
	SuppressedCount int    `json:"suppressed_count,omitempty"`

	// Job is what this occurrence started, in the vocabulary of whatever it
	// launched: a job's id, or a sync attempt's. UnifiedJobType says which,
	// since the two are looked up in different places. AWX calls both a
	// unified job, which is why one field carries both.
	Job            string `json:"job,omitempty"`
	UnifiedJobType string `json:"unified_job_type,omitempty"`
}

type occurrenceListDTO struct {
	LinkSet
	Occurrences []occurrenceDTO `json:"occurrences"`
}

// scheduleWriteDTO is what a create or update accepts.
//
// Every field is a pointer so a PATCH can distinguish "not supplied" from
// "supplied as the zero value". That distinction is load-bearing for
// exactly one field and would be a silent defect without it: `enabled:
// false` and an absent `enabled` are opposite instructions, and a plain
// bool cannot tell them apart, so a PATCH changing only a name would
// re-enable a deliberately paused schedule.
type scheduleWriteDTO struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Enabled     *bool     `json:"enabled"`
	RRule       *string   `json:"rrule"`
	Exclusions  *[]string `json:"exclusions"`
	Timezone    *string   `json:"timezone"`
	DTStart     *string   `json:"dtstart"`
	DTEnd       *string   `json:"dtend"`

	// UnifiedJobTemplate is what to launch: a launchable id (AWX's field
	// name).
	UnifiedJobTemplate *int `json:"unified_job_template"`

	// Template is the deprecated spelling, a TEMPLATE id rather than a
	// launchable one, kept because it is what this API documented and
	// accepted before anything but a template could be scheduled. It is
	// resolved to that template's launchable, and sending both fields
	// disagreeing is refused rather than silently resolved one way.
	Template *int `json:"template"`

	SavedConfig *int `json:"saved_config"`
}

// previewRequestDTO is a preview's body.
type previewRequestDTO struct {
	RRule      string   `json:"rrule"`
	Exclusions []string `json:"exclusions"`
	Timezone   string   `json:"timezone"`
	DTStart    string   `json:"dtstart"`
	DTEnd      string   `json:"dtend"`
	From       string   `json:"from"`
	Count      int      `json:"count"`
}

type previewOccurrenceDTO struct {
	Local string `json:"local"`
	UTC   string `json:"utc"`
}

type previewResponseDTO struct {
	LinkSet
	Timezone    string                 `json:"timezone"`
	Occurrences []previewOccurrenceDTO `json:"occurrences"`
}

type zoneinfoDTO struct {
	LinkSet
	Zones  []string `json:"zones"`
	Common []string `json:"common"`
}

// List serves GET /schedules.
func (h *ScheduleHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, ok := intQuery(w, r, "limit")
	if !ok {
		return
	}
	after := r.URL.Query().Get("after")

	schedules, err := h.schedules.List(r.Context(), schedule.AnyOrganization, after, limit)
	if err != nil {
		// The store's not-found here is about the cursor, not the
		// collection: the schedule the previous page ended on is gone, so
		// its place in the order is unknown. A 404 would read as "no
		// schedules" to a client that asked for a list.
		if after != "" && errors.Is(err, schedule.ErrNotFound) {
			RespondError(w, r, http.StatusBadRequest, "after: the schedule this page continues from no longer exists; list again from the start")
			return
		}
		h.respondStoreError(w, r, "list", "", err)
		return
	}

	resp := scheduleListDTO{Schedules: make([]scheduleDTO, 0, len(schedules))}
	for _, s := range schedules {
		resp.Schedules = append(resp.Schedules, toScheduleDTO(s))
	}
	Respond(w, r, http.StatusOK, &resp)
}

// Get serves GET /schedules/{id}.
func (h *ScheduleHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s, err := h.schedules.Get(r.Context(), schedule.AnyOrganization, id)
	if err != nil {
		h.respondStoreError(w, r, "get", id, err)
		return
	}
	dto := toScheduleDTO(s)
	Respond(w, r, http.StatusOK, &dto)
}

// Create serves POST /schedules.
func (h *ScheduleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body scheduleWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}

	// The fields the schema declares required are checked here, on the
	// create path only. A PATCH legitimately omits them -- that is what
	// makes it a PATCH -- so the same check on Update would refuse every
	// partial edit.
	//
	// It is a check rather than a job left to the store, because "you did
	// not send dtstart" is a fact about the request, and answering it from
	// the persistence layer would make the status and the wording depend
	// on which store happened to be wired.
	if body.DTStart == nil {
		RespondError(w, r, http.StatusBadRequest, "dtstart is required and must be an RFC 3339 timestamp")
		return
	}
	if body.RRule == nil {
		RespondError(w, r, http.StatusBadRequest, "rrule is required")
		return
	}
	if body.Name == nil {
		RespondError(w, r, http.StatusBadRequest, "name is required")
		return
	}
	if body.UnifiedJobTemplate == nil && body.Template == nil {
		RespondError(w, r, http.StatusBadRequest, "unified_job_template is required: the id of the job template or project this schedule launches")
		return
	}

	// A create starts from the documented defaults rather than from a zero
	// value, so an omitted `enabled` means "on" and an omitted timezone
	// means UTC, which is what the schema says.
	s := schedule.Schedule{Enabled: true, Timezone: "UTC"}
	if !h.applyWrite(w, r, &s, body) {
		return
	}

	reach, ok := h.reachOf(w, r)
	if !ok {
		return
	}

	created, err := h.schedules.Create(r.Context(), s, reach)
	if err != nil {
		h.respondStoreError(w, r, "create", "", err)
		return
	}

	dto := toScheduleDTO(created)
	w.Header().Set("Location", APIVersionPrefix+"/schedules/"+created.ScheduleID)
	Respond(w, r, http.StatusCreated, &dto)
}

// Update serves PATCH /schedules/{id}.
func (h *ScheduleHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	existing, err := h.schedules.Get(r.Context(), schedule.AnyOrganization, id)
	if err != nil {
		h.respondStoreError(w, r, "get", id, err)
		return
	}

	var body scheduleWriteDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if !h.applyWrite(w, r, &existing, body) {
		return
	}

	reach, ok := h.reachOf(w, r)
	if !ok {
		return
	}

	updated, err := h.schedules.Update(r.Context(), existing, reach)
	if err != nil {
		h.respondStoreError(w, r, "update", id, err)
		return
	}
	dto := toScheduleDTO(updated)
	Respond(w, r, http.StatusOK, &dto)
}

// Delete serves DELETE /schedules/{id}.
func (h *ScheduleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.schedules.Delete(r.Context(), schedule.AnyOrganization, id); err != nil {
		h.respondStoreError(w, r, "delete", id, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListOccurrences serves GET /schedules/{id}/occurrences.
func (h *ScheduleHandler) ListOccurrences(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit, ok := intQuery(w, r, "limit")
	if !ok {
		return
	}

	occurrences, err := h.schedules.ListOccurrences(r.Context(), schedule.AnyOrganization, id, limit)
	if err != nil {
		h.respondStoreError(w, r, "list occurrences for", id, err)
		return
	}

	resp := occurrenceListDTO{Occurrences: make([]occurrenceDTO, 0, len(occurrences))}
	for _, o := range occurrences {
		resp.Occurrences = append(resp.Occurrences, occurrenceDTO{
			OccurrenceAt:    o.OccurrenceAt.Format(time.RFC3339),
			Outcome:         string(o.Outcome),
			Reason:          o.Reason,
			SuppressedCount: o.SuppressedCount,
			Job:             o.JobID,
			UnifiedJobType:  string(o.UnifiedJobType),
		})
	}
	Respond(w, r, http.StatusOK, &resp)
}

// Preview serves POST /schedules/preview.
//
// It writes nothing. The method is POST because a recurrence, its exclusion
// lines and its anchor do not fit unambiguously in a query string, not
// because there is any state to change.
func (h *ScheduleHandler) Preview(w http.ResponseWriter, r *http.Request) {
	var body previewRequestDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Timezone == "" {
		body.Timezone = "UTC"
	}

	dtstart, ok := parseTimeField(w, r, "dtstart", body.DTStart, true)
	if !ok {
		return
	}
	dtend, ok := parseTimeField(w, r, "dtend", body.DTEnd, false)
	if !ok {
		return
	}
	from, ok := parseTimeField(w, r, "from", body.From, false)
	if !ok {
		return
	}
	if from.IsZero() {
		from = time.Now().UTC()
	}

	count := body.Count
	switch {
	case count <= 0:
		count = defaultPreviewCount
	case count > maxPreviewCount:
		count = maxPreviewCount
	}

	// Built as a real Schedule and validated through the same Validate the
	// write path uses, so a preview cannot accept a recurrence a save would
	// then refuse. A preview that disagreed with the save beside it would
	// be worse than no preview.
	candidate := schedule.Schedule{
		Name:         "preview",
		LaunchableID: -1, // stands in for the target a preview does not have
		Enabled:      true,
		RRule:        body.RRule,
		Exclusions:   body.Exclusions,
		Timezone:     body.Timezone,
		DTStart:      dtstart,
	}
	if !dtend.IsZero() {
		candidate.DTEnd = &dtend
	}
	if err := candidate.Validate(); err != nil {
		respondScheduleFieldError(w, r, err)
		return
	}

	// Defensive rather than reachable: Validate above already expanded one
	// occurrence successfully through the same rule, zone and anchor, so
	// anything that would fail here has already been answered. It is
	// checked anyway because dropping a returned error to claim a coverage
	// line would be the wrong trade.
	occurrences, err := candidate.Preview(from, count)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, "that recurrence could not be expanded")
		return
	}

	resp := previewResponseDTO{
		Timezone:    body.Timezone,
		Occurrences: make([]previewOccurrenceDTO, 0, len(occurrences)),
	}
	for _, o := range occurrences {
		resp.Occurrences = append(resp.Occurrences, previewOccurrenceDTO{
			Local: o.Format(time.RFC3339),
			UTC:   o.UTC().Format(time.RFC3339),
		})
	}
	Respond(w, r, http.StatusOK, &resp)
}

// Zoneinfo serves GET /zoneinfo.
func (h *ScheduleHandler) Zoneinfo(w http.ResponseWriter, r *http.Request) {
	resp := zoneinfoDTO{Zones: zoneinfo.Names(), Common: zoneinfo.Common()}
	Respond(w, r, http.StatusOK, &resp)
}

// reachOf builds the caller's reach: what they may launch, and where.
//
// Organization zero means "not narrowed to one tenant", which is what every
// request is today: no request carries a tenant (see schedule.AnyOrganization),
// so the tenancy half of the check compares the target's organization to the
// schedule's rather than to the caller's. The scope half is per launchable
// type and is enforced regardless.
func (h *ScheduleHandler) reachOf(w http.ResponseWriter, r *http.Request) (launchable.Reach, bool) {
	identity, ok := IdentityFromContext(r.Context())
	if !ok || identity == nil {
		RespondError(w, r, http.StatusUnauthorized, "unauthorized")
		return launchable.Reach{}, false
	}
	return launchable.ReachOf(identity, 0), true
}

// applyTarget resolves what a schedule launches from whichever of the two
// fields the caller sent.
//
// The deprecated "template" field names a TEMPLATE, and the current
// "unified_job_template" names a LAUNCHABLE, so the two are different id
// spaces over the same objects. Sending both is only accepted when they agree
// after resolution: silently preferring one would mean a caller who updated
// half their client could repoint a schedule at something they did not name.
func (h *ScheduleHandler) applyTarget(w http.ResponseWriter, r *http.Request, s *schedule.Schedule, body scheduleWriteDTO) bool {
	fromAlias := 0
	if body.Template != nil {
		if h.templates == nil {
			RespondError(w, r, http.StatusBadRequest,
				`the "template" field is not available on this controller; use "unified_job_template"`)
			return false
		}
		tmpl, err := h.templates.Get(r.Context(), *body.Template)
		if err != nil {
			RespondError(w, r, http.StatusNotFound, "no template with that id")
			return false
		}
		if tmpl.LaunchableID == 0 {
			// A template with no launchable row cannot be scheduled. It means
			// the launchable backfill did not run against this database, which
			// is an operator's problem rather than this caller's, so it is said
			// plainly instead of reported as a bad request.
			RespondError(w, r, http.StatusConflict,
				"that template has no launchable record, so it cannot be scheduled; check that the database migrations have all been applied")
			return false
		}
		fromAlias = tmpl.LaunchableID
	}

	switch {
	case body.UnifiedJobTemplate != nil && fromAlias != 0 && *body.UnifiedJobTemplate != fromAlias:
		RespondError(w, r, http.StatusBadRequest,
			`"unified_job_template" and the deprecated "template" field name different things; send only "unified_job_template"`)
		return false
	case body.UnifiedJobTemplate != nil:
		s.LaunchableID = *body.UnifiedJobTemplate
	case fromAlias != 0:
		s.LaunchableID = fromAlias
	}
	return true
}

// applyWrite folds a request body over a schedule, reporting a bad request
// itself and returning false when it did.
func (h *ScheduleHandler) applyWrite(w http.ResponseWriter, r *http.Request, s *schedule.Schedule, body scheduleWriteDTO) bool {
	if body.Name != nil {
		s.Name = *body.Name
	}
	if body.Description != nil {
		s.Description = *body.Description
	}
	if body.Enabled != nil {
		s.Enabled = *body.Enabled
	}
	if body.RRule != nil {
		s.RRule = *body.RRule
	}
	if body.Exclusions != nil {
		s.Exclusions = *body.Exclusions
	}
	if body.Timezone != nil {
		s.Timezone = *body.Timezone
	}
	if !h.applyTarget(w, r, s, body) {
		return false
	}
	if body.SavedConfig != nil {
		s.SavedConfigID = *body.SavedConfig
	}
	if body.DTStart != nil {
		t, ok := parseTimeField(w, r, "dtstart", *body.DTStart, true)
		if !ok {
			return false
		}
		s.DTStart = t
	}
	if body.DTEnd != nil {
		if *body.DTEnd == "" {
			// An explicit empty string clears it, which is how a form says
			// "make this open-ended again"; omitting the field leaves it.
			s.DTEnd = nil
		} else {
			t, ok := parseTimeField(w, r, "dtend", *body.DTEnd, true)
			if !ok {
				return false
			}
			s.DTEnd = &t
		}
	}
	return true
}

// parseTimeField parses an RFC 3339 timestamp, reporting a bad request
// itself. An empty value is the zero time unless required.
func parseTimeField(w http.ResponseWriter, r *http.Request, name, value string, required bool) (time.Time, bool) {
	if value == "" {
		if required {
			RespondError(w, r, http.StatusBadRequest, name+" is required and must be an RFC 3339 timestamp")
			return time.Time{}, false
		}
		return time.Time{}, true
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		RespondError(w, r, http.StatusBadRequest, name+" must be an RFC 3339 timestamp")
		return time.Time{}, false
	}
	return t.UTC(), true
}

// intQuery reads an optional positive integer query parameter, reporting a
// bad request itself. Zero means absent, which the store reads as its own
// default page size.
func intQuery(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		RespondError(w, r, http.StatusBadRequest, name+" must be a positive integer")
		return 0, false
	}
	return n, true
}

// toScheduleDTO projects a schedule onto the wire.
//
// next_run is rendered twice, in UTC and in the schedule's own zone, and
// that is not redundancy. UTC alone hides the reading an operator recognises
// and is the whole subject of a daylight saving question; the local reading
// alone is ambiguous across a fold. PLAN.md Section 30.1 asks for both by
// name for the preview, and a schedule's own next run deserves the same.
func toScheduleDTO(s schedule.Schedule) scheduleDTO {
	dto := scheduleDTO{
		ID:                     s.ScheduleID,
		Name:                   s.Name,
		Description:            s.Description,
		Enabled:                s.Enabled,
		RRule:                  s.RRule,
		Exclusions:             s.Exclusions,
		Timezone:               s.Timezone,
		DTStart:                s.DTStart.Format(time.RFC3339),
		UnifiedJobTemplate:     s.LaunchableID,
		UnifiedJobTemplateName: s.Launchable.Name,
		UnifiedJobTemplateType: s.Launchable.Type,
		SavedConfig:            s.SavedConfigID,
		Organization:           s.OrganizationID,
		CreatedAt:              s.CreatedAt.Format(time.RFC3339),
		UpdatedAt:              s.UpdatedAt.Format(time.RFC3339),
	}
	if s.DTEnd != nil {
		dto.DTEnd = s.DTEnd.Format(time.RFC3339)
	}
	if s.LastFired != nil {
		dto.LastFired = s.LastFired.Format(time.RFC3339)
	}
	if s.NextRun != nil {
		dto.NextRun = s.NextRun.Format(time.RFC3339)
		// A zone that no longer loads must not cost the caller the rest of
		// the record: the UTC reading is still true and still useful.
		if loc, err := s.Location(); err == nil {
			dto.NextRunLocal = s.NextRun.In(loc).Format(time.RFC3339)
		}
	}
	return dto
}

// respondStoreError maps a store failure onto a status code.
func (h *ScheduleHandler) respondStoreError(w http.ResponseWriter, r *http.Request, action, id string, err error) {
	switch {
	case errors.Is(err, schedule.ErrNotFound):
		RespondError(w, r, http.StatusNotFound, "schedule not found")
	case errors.Is(err, schedule.ErrInvalid):
		respondScheduleFieldError(w, r, err)
	default:
		h.logger.ErrorContext(r.Context(), "failed to "+action+" schedule",
			slog.String("schedule_id", id),
			slog.String("error", err.Error()))
		RespondError(w, r, http.StatusInternalServerError, "internal error")
	}
}

// respondScheduleFieldError turns a validation failure into the status that
// names it, with the field at fault in the message.
//
// The field name travels in the message rather than being dropped, because
// the caller is usually a form and "name: A schedule with that name already
// exists" is actionable where "invalid schedule" is not.
//
// Four statuses, decided by the sentinel the store attached rather than by
// reading the message, so rewording a message cannot silently change a status.
// The three beyond 400 are the ones a caller has to be able to tell apart: a
// name already taken is a conflict they can resolve; a target they may not
// launch, or that belongs to another organization, is a permission answer and
// not a malformed request; and a target that does not exist is a 404 about the
// thing they named rather than about the schedule.
func respondScheduleFieldError(w http.ResponseWriter, r *http.Request, err error) {
	var fe schedule.FieldError
	if !errors.As(err, &fe) {
		RespondError(w, r, http.StatusBadRequest, "that schedule is not valid")
		return
	}
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, schedule.ErrNameTaken):
		status = http.StatusConflict
	case errors.Is(err, launchable.ErrNotPermitted), errors.Is(err, launchable.ErrCrossTenant):
		status = http.StatusForbidden
	case errors.Is(err, launchable.ErrNotFound), errors.Is(err, launchable.ErrUnknownType):
		status = http.StatusNotFound
	}
	RespondError(w, r, status, fe.Field+": "+fe.Message)
}
