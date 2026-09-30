// This file covers ScheduleHandler: the write path's pointer-field
// semantics, the preview endpoint's dual local/UTC rendering, the zone
// allowlist the picker is served from, and the status codes a validation
// failure maps onto.
//
// Routes are built from apispec's own Endpoint values, so a drift between
// the declared scope or pattern and what these tests exercise fails here.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

// stubScheduleStore is an in-memory api.ScheduleStore.
//
// A stub rather than the real ent store because what is under test here is
// the HTTP layer: body decoding, the pointer-field PATCH semantics, status
// mapping. The store's own behaviour -- tenancy, the unique index, next_run
// computation -- is tested against a real database in internal/schedule,
// which is where it lives and where a fake would prove nothing.
type stubScheduleStore struct {
	items map[string]schedule.Schedule

	gotCreate schedule.Schedule
	gotUpdate schedule.Schedule

	// gotReach is the reach the handler passed, so a test can prove the
	// caller's own permissions reach the store rather than being dropped on
	// the way.
	gotReach launchable.Reach

	createErr error
	getErr    error
	listErr   error
	updateErr error
	deleteErr error
	occErr    error

	occurrences []schedule.Occurrence
}

func newStubScheduleStore(items ...schedule.Schedule) *stubScheduleStore {
	s := &stubScheduleStore{items: map[string]schedule.Schedule{}}
	for _, item := range items {
		s.items[item.ScheduleID] = item
	}
	return s
}

func (s *stubScheduleStore) Create(_ context.Context, in schedule.Schedule, reach launchable.Reach) (schedule.Schedule, error) {
	s.gotCreate = in
	s.gotReach = reach
	if s.createErr != nil {
		return schedule.Schedule{}, s.createErr
	}
	in.ScheduleID = "sched-created"
	in.OrganizationID = 7
	next := time.Date(2024, 3, 9, 14, 0, 0, 0, time.UTC)
	in.NextRun = &next
	s.items[in.ScheduleID] = in
	return in, nil
}

func (s *stubScheduleStore) Update(_ context.Context, in schedule.Schedule, reach launchable.Reach) (schedule.Schedule, error) {
	s.gotUpdate = in
	s.gotReach = reach
	if s.updateErr != nil {
		return schedule.Schedule{}, s.updateErr
	}
	s.items[in.ScheduleID] = in
	return in, nil
}

func (s *stubScheduleStore) Get(_ context.Context, _ int, id string) (schedule.Schedule, error) {
	if s.getErr != nil {
		return schedule.Schedule{}, s.getErr
	}
	item, ok := s.items[id]
	if !ok {
		return schedule.Schedule{}, schedule.ErrNotFound
	}
	return item, nil
}

func (s *stubScheduleStore) List(_ context.Context, _ int, _ string, _ int) ([]schedule.Schedule, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]schedule.Schedule, 0, len(s.items))
	for _, item := range s.items {
		out = append(out, item)
	}
	return out, nil
}

func (s *stubScheduleStore) Delete(_ context.Context, _ int, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if _, ok := s.items[id]; !ok {
		return schedule.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func (s *stubScheduleStore) ListOccurrences(_ context.Context, _ int, id string, _ int) ([]schedule.Occurrence, error) {
	if s.occErr != nil {
		return nil, s.occErr
	}
	if _, ok := s.items[id]; !ok {
		return nil, schedule.ErrNotFound
	}
	return s.occurrences, nil
}

// stubScheduleTemplates resolves a template id to its launchable, which is all
// the handler needs for the deprecated "template" field. Template 42 stands for
// launchable 44 here, deliberately different numbers so a test cannot pass by
// confusing the two id spaces.
type stubScheduleTemplates struct{ err error }

func (s stubScheduleTemplates) Get(_ context.Context, id int) (launch.Template, error) {
	if s.err != nil {
		return launch.Template{}, s.err
	}
	if id != 42 {
		return launch.Template{}, launch.ErrNotFound
	}
	return launch.Template{ID: 42, Name: "nightly build", LaunchableID: 44}, nil
}

func scheduleRouter(t *testing.T, store api.ScheduleStore) http.Handler {
	t.Helper()
	handler := api.NewScheduleHandler(store, stubScheduleTemplates{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListSchedules.Route(handler.List),
			apispec.GetSchedule.Route(handler.Get),
			apispec.CreateSchedule.Route(handler.Create),
			apispec.UpdateSchedule.Route(handler.Update),
			apispec.DeleteSchedule.Route(handler.Delete),
			apispec.ListScheduleOccurrences.Route(handler.ListOccurrences),
			apispec.PreviewSchedule.Route(handler.Preview),
			apispec.ListZoneinfo.Route(handler.Zoneinfo),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

func testSchedule(id string) schedule.Schedule {
	next := time.Date(2024, 3, 11, 13, 0, 0, 0, time.UTC)
	return schedule.Schedule{
		ID:             1,
		ScheduleID:     id,
		OrganizationID: 7,
		LaunchableID:   44,
		Launchable: launchable.Target{
			ID: 44, Type: launchable.TypeJobTemplate,
			Name: "nightly build", OrganizationID: 7,
		},
		Name:     "nightly",
		Enabled:  true,
		RRule:    "FREQ=DAILY",
		Timezone: "America/New_York",
		DTStart:  time.Date(2024, 3, 8, 14, 0, 0, 0, time.UTC),
		NextRun:  &next,
	}
}

func doScheduleRequest(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// TestScheduleGetRendersNextRunInBothZones is the projection decision worth
// asserting: UTC alone hides the reading an operator recognises, and the
// local reading alone is ambiguous across a daylight saving fold.
func TestScheduleGetRendersNextRunInBothZones(t *testing.T) {
	store := newStubScheduleStore(testSchedule("sched-1"))
	router := scheduleRouter(t, store)

	w := doScheduleRequest(t, router, http.MethodGet, api.APIVersionPrefix+"/schedules/sched-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200: %s", w.Code, w.Body.String())
	}

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got["next_run"] != "2024-03-11T13:00:00Z" {
		t.Errorf("next_run = %v, want the UTC reading", got["next_run"])
	}
	// 13:00Z in New York on 11 March 2024 is 09:00 EDT: after the
	// transition, so the offset is -04:00 rather than -05:00.
	if got["next_run_local"] != "2024-03-11T09:00:00-04:00" {
		t.Errorf("next_run_local = %v, want the post-transition local reading", got["next_run_local"])
	}
}

func TestScheduleGetNotFound(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())
	w := doScheduleRequest(t, router, http.MethodGet, api.APIVersionPrefix+"/schedules/missing", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("GET = %d, want 404", w.Code)
	}
}

func TestScheduleListReturnsACollection(t *testing.T) {
	store := newStubScheduleStore(testSchedule("sched-1"))
	router := scheduleRouter(t, store)

	w := doScheduleRequest(t, router, http.MethodGet, api.APIVersionPrefix+"/schedules", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got struct {
		Schedules []map[string]any `json:"schedules"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Schedules) != 1 {
		t.Fatalf("listed %d schedules, want 1", len(got.Schedules))
	}
}

func TestScheduleListRefusesABadLimit(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())
	w := doScheduleRequest(t, router, http.MethodGet, api.APIVersionPrefix+"/schedules?limit=nope", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("GET with a bad limit = %d, want 400", w.Code)
	}
}

// TestScheduleListRefusesACursorWhoseScheduleIsGone proves a page that
// continues from a deleted schedule is a 400 naming the cursor, not a 404
// that reads as an empty collection.
func TestScheduleListRefusesACursorWhoseScheduleIsGone(t *testing.T) {
	store := newStubScheduleStore()
	store.listErr = fmt.Errorf("schedule: list after gone: %w", schedule.ErrNotFound)
	w := doScheduleRequest(t, scheduleRouter(t, store), http.MethodGet, api.APIVersionPrefix+"/schedules?after=gone", "")
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "list again from the start") {
		t.Fatalf("GET after a deleted schedule = %d %s, want 400 saying to list again", w.Code, w.Body.String())
	}
}

// TestScheduleCreateAppliesDocumentedDefaults proves an omitted enabled
// means on and an omitted timezone means UTC, which is what the schema
// promises and what a zero-valued struct would silently get wrong.
func TestScheduleCreateAppliesDocumentedDefaults(t *testing.T) {
	store := newStubScheduleStore()
	router := scheduleRouter(t, store)

	body := `{"name":"nightly","template":42,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST = %d, want 201: %s", w.Code, w.Body.String())
	}
	if !store.gotCreate.Enabled {
		t.Error("an omitted enabled did not default to true")
	}
	if store.gotCreate.Timezone != "UTC" {
		t.Errorf("timezone = %q, want the documented default of UTC", store.gotCreate.Timezone)
	}
	if loc := w.Header().Get("Location"); loc != api.APIVersionPrefix+"/schedules/sched-created" {
		t.Errorf("Location = %q", loc)
	}
}

func TestScheduleCreateRefusesAMissingDTStart(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())
	body := `{"name":"nightly","template":42,"rrule":"FREQ=DAILY"}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST without dtstart = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestScheduleCreateMapsANameCollisionTo409 proves the status is chosen by
// the typed sentinel rather than by reading the message, so rewording the
// message cannot silently turn a 409 into a 400.
func TestScheduleCreateMapsANameCollisionTo409(t *testing.T) {
	store := newStubScheduleStore()
	store.createErr = schedule.FieldError{
		Field:   "name",
		Message: "A schedule with that name already exists in this organization.",
		Cause:   schedule.ErrNameTaken,
	}
	router := scheduleRouter(t, store)

	body := `{"name":"nightly","template":42,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules", body)
	if w.Code != http.StatusConflict {
		t.Fatalf("POST with a duplicate name = %d, want 409: %s", w.Code, w.Body.String())
	}

	// A field error that is NOT a collision stays a 400.
	store.createErr = schedule.FieldError{Field: "rrule", Message: "That recurrence could not be read."}
	w = doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST with a bad recurrence = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "rrule") {
		t.Errorf("the error does not name the field at fault: %s", w.Body.String())
	}
}

func TestScheduleCreateMapsAnUnknownErrorTo500(t *testing.T) {
	store := newStubScheduleStore()
	store.createErr = errors.New("the database is on fire")
	router := scheduleRouter(t, store)

	body := `{"name":"nightly","template":42,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules", body)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("POST = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "on fire") {
		t.Errorf("the store's own error text reached the caller: %s", w.Body.String())
	}
}

// TestScheduleUpdateDistinguishesAbsentFromFalse is the reason every write
// field is a pointer, and it is the defect that shape exists to prevent: a
// PATCH changing only the name must not re-enable a deliberately paused
// schedule.
func TestScheduleUpdateDistinguishesAbsentFromFalse(t *testing.T) {
	paused := testSchedule("sched-1")
	paused.Enabled = false
	store := newStubScheduleStore(paused)
	router := scheduleRouter(t, store)

	w := doScheduleRequest(t, router, http.MethodPatch,
		api.APIVersionPrefix+"/schedules/sched-1", `{"name":"renamed"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200: %s", w.Code, w.Body.String())
	}
	if store.gotUpdate.Enabled {
		t.Error("a PATCH that did not mention enabled re-enabled a paused schedule")
	}
	if store.gotUpdate.Name != "renamed" {
		t.Errorf("name = %q, want the submitted one", store.gotUpdate.Name)
	}
	// Everything not mentioned survives.
	if store.gotUpdate.RRule != paused.RRule {
		t.Errorf("rrule = %q, want the stored %q", store.gotUpdate.RRule, paused.RRule)
	}
}

// TestScheduleUpdateClearsDTEndOnAnExplicitEmptyString covers the one place
// an empty value is an instruction rather than an absence.
func TestScheduleUpdateClearsDTEndOnAnExplicitEmptyString(t *testing.T) {
	bounded := testSchedule("sched-1")
	end := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	bounded.DTEnd = &end
	store := newStubScheduleStore(bounded)
	router := scheduleRouter(t, store)

	w := doScheduleRequest(t, router, http.MethodPatch,
		api.APIVersionPrefix+"/schedules/sched-1", `{"dtend":""}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200: %s", w.Code, w.Body.String())
	}
	if store.gotUpdate.DTEnd != nil {
		t.Errorf("dtend = %v, want it cleared by the explicit empty string", store.gotUpdate.DTEnd)
	}

	// An omitted dtend leaves it alone, which is the other half.
	store2 := newStubScheduleStore(bounded)
	router2 := scheduleRouter(t, store2)
	w = doScheduleRequest(t, router2, http.MethodPatch,
		api.APIVersionPrefix+"/schedules/sched-1", `{"name":"renamed"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH = %d, want 200", w.Code)
	}
	if store2.gotUpdate.DTEnd == nil {
		t.Error("a PATCH that did not mention dtend cleared it")
	}
}

func TestScheduleDelete(t *testing.T) {
	store := newStubScheduleStore(testSchedule("sched-1"))
	router := scheduleRouter(t, store)

	w := doScheduleRequest(t, router, http.MethodDelete, api.APIVersionPrefix+"/schedules/sched-1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204: %s", w.Code, w.Body.String())
	}
	w = doScheduleRequest(t, router, http.MethodDelete, api.APIVersionPrefix+"/schedules/sched-1", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("a second DELETE = %d, want 404", w.Code)
	}
}

// TestScheduleOccurrencesRenderSkipsAsRows is the audit property: a run that
// did not happen must be a row carrying a reason, not an absence.
func TestScheduleOccurrencesRenderSkipsAsRows(t *testing.T) {
	store := newStubScheduleStore(testSchedule("sched-1"))
	store.occurrences = []schedule.Occurrence{
		{OccurrenceAt: time.Date(2024, 3, 11, 13, 0, 0, 0, time.UTC), Outcome: schedule.OutcomeFired, JobID: "job-1"},
		{OccurrenceAt: time.Date(2024, 3, 10, 13, 0, 0, 0, time.UTC), Outcome: schedule.OutcomeSkipped, Reason: schedule.ReasonMissedWindow},
		{OccurrenceAt: time.Date(2024, 3, 9, 13, 0, 0, 0, time.UTC), Outcome: schedule.OutcomeSkipped, Reason: schedule.ReasonMissedWindowTruncated, SuppressedCount: 400},
	}
	router := scheduleRouter(t, store)

	w := doScheduleRequest(t, router, http.MethodGet,
		api.APIVersionPrefix+"/schedules/sched-1/occurrences", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got struct {
		Occurrences []struct {
			Outcome         string `json:"outcome"`
			Reason          string `json:"reason"`
			SuppressedCount int    `json:"suppressed_count"`
			Job             string `json:"job"`
		} `json:"occurrences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Occurrences) != 3 {
		t.Fatalf("got %d occurrences, want 3", len(got.Occurrences))
	}
	if got.Occurrences[0].Job != "job-1" {
		t.Errorf("the fired occurrence does not name its job: %+v", got.Occurrences[0])
	}
	if got.Occurrences[1].Reason != schedule.ReasonMissedWindow {
		t.Errorf("the skipped occurrence carries no reason: %+v", got.Occurrences[1])
	}
	if got.Occurrences[2].SuppressedCount != 400 {
		t.Errorf("the truncated row does not carry its count: %+v", got.Occurrences[2])
	}
}

// TestSchedulePreviewReturnsBothReadingsAcrossADSTBoundary is the endpoint's
// whole reason for existing: the local hour holds while the UTC hour moves,
// and an operator confirming a schedule needs to see exactly that.
func TestSchedulePreviewReturnsBothReadingsAcrossADSTBoundary(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())

	body := `{"rrule":"FREQ=DAILY","timezone":"America/New_York",
	          "dtstart":"2024-03-08T09:00:00-05:00","from":"2024-03-08T00:00:00-05:00","count":5}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules/preview", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, want 200: %s", w.Code, w.Body.String())
	}

	var got struct {
		Timezone    string `json:"timezone"`
		Occurrences []struct {
			Local string `json:"local"`
			UTC   string `json:"utc"`
		} `json:"occurrences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Timezone != "America/New_York" {
		t.Errorf("timezone = %q", got.Timezone)
	}
	if len(got.Occurrences) != 5 {
		t.Fatalf("got %d occurrences, want 5", len(got.Occurrences))
	}

	// Every local reading keeps 09:00; the UTC readings differ across the
	// 10 March transition. Both halves are asserted, because either alone
	// would pass for a wrong implementation.
	utcHours := map[string]bool{}
	for _, o := range got.Occurrences {
		if !strings.Contains(o.Local, "T09:00:00") {
			t.Errorf("local reading %q did not keep the 09:00 wall clock", o.Local)
		}
		utcHours[strings.Split(o.UTC, "T")[1]] = true
	}
	if len(utcHours) != 2 {
		t.Errorf("UTC readings covered %d distinct times, want 2 across the transition: %v", len(utcHours), utcHours)
	}
}

func TestSchedulePreviewRefusesBadInput(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())

	cases := []struct {
		name string
		body string
	}{
		{"unsupported recurrence", `{"rrule":"FREQ=SECONDLY","dtstart":"2024-03-08T09:00:00Z"}`},
		{"malformed recurrence", `{"rrule":"NOT A RULE","dtstart":"2024-03-08T09:00:00Z"}`},
		{"unknown zone", `{"rrule":"FREQ=DAILY","timezone":"Mars/Olympus","dtstart":"2024-03-08T09:00:00Z"}`},
		{"hostile zone", `{"rrule":"FREQ=DAILY","timezone":"../../etc/passwd","dtstart":"2024-03-08T09:00:00Z"}`},
		{"recurrence that never happens", `{"rrule":"FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=30","dtstart":"2024-03-08T09:00:00Z"}`},
		{"missing dtstart", `{"rrule":"FREQ=DAILY"}`},
		{"bad dtstart", `{"rrule":"FREQ=DAILY","dtstart":"the eighth of March"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules/preview", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("POST = %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestSchedulePreviewCapsTheCount proves an operator cannot ask the server
// to marshal an unbounded expansion.
func TestSchedulePreviewCapsTheCount(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())
	body := `{"rrule":"FREQ=MINUTELY","dtstart":"2024-03-08T09:00:00Z","count":100000}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules/preview", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got struct {
		Occurrences []map[string]string `json:"occurrences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Occurrences) > 100 {
		t.Errorf("preview returned %d occurrences; the cap did not apply", len(got.Occurrences))
	}
}

// TestZoneinfoOffersOnlyLoadableZones is the property the generated list
// exists for: a picker that offered a zone the server then refused would be
// worse than no picker.
func TestZoneinfoOffersOnlyLoadableZones(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())

	w := doScheduleRequest(t, router, http.MethodGet, api.APIVersionPrefix+"/zoneinfo", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got struct {
		Zones  []string `json:"zones"`
		Common []string `json:"common"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Zones) < 100 {
		t.Fatalf("only %d zones offered; the generated list looks truncated", len(got.Zones))
	}
	if len(got.Common) == 0 || got.Common[0] != "UTC" {
		t.Errorf("common zones = %v, want UTC first", got.Common)
	}
	for _, z := range got.Zones {
		if _, err := time.LoadLocation(z); err != nil {
			t.Fatalf("offered zone %q does not load: %v", z, err)
		}
	}
}

// TestScheduleWriteRefusesAMalformedBody covers the decode guard on both
// write paths.
func TestScheduleWriteRefusesAMalformedBody(t *testing.T) {
	store := newStubScheduleStore(testSchedule("sched-1"))
	router := scheduleRouter(t, store)

	for _, tc := range []struct{ method, target string }{
		{http.MethodPost, api.APIVersionPrefix + "/schedules"},
		{http.MethodPatch, api.APIVersionPrefix + "/schedules/sched-1"},
		{http.MethodPost, api.APIVersionPrefix + "/schedules/preview"},
	} {
		w := doScheduleRequest(t, router, tc.method, tc.target, `{"name":`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s with a truncated body = %d, want 400", tc.method, tc.target, w.Code)
		}
	}
}

// TestScheduleCreateRefusesEachMissingRequiredField covers the create-only
// required-field guard field by field, so a later refactor cannot drop one
// of the four and leave the others passing.
func TestScheduleCreateRefusesEachMissingRequiredField(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())

	cases := map[string]string{
		"no name":     `{"template":42,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`,
		"no template": `{"name":"n","rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`,
		"no rrule":    `{"name":"n","template":42,"dtstart":"2024-03-08T09:00:00Z"}`,
		"no dtstart":  `{"name":"n","template":42,"rrule":"FREQ=DAILY"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules", body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("POST = %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}

// TestScheduleWriteRefusesUnparseableTimestamps covers both datetime fields
// on both write paths, including the create-time dtend that only the write
// helper reaches.
func TestScheduleWriteRefusesUnparseableTimestamps(t *testing.T) {
	store := newStubScheduleStore(testSchedule("sched-1"))
	router := scheduleRouter(t, store)

	bad := []struct {
		name, method, target, body string
	}{
		{"create bad dtstart", http.MethodPost, api.APIVersionPrefix + "/schedules",
			`{"name":"n","template":42,"rrule":"FREQ=DAILY","dtstart":"soon"}`},
		{"create bad dtend", http.MethodPost, api.APIVersionPrefix + "/schedules",
			`{"name":"n","template":42,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z","dtend":"later"}`},
		{"patch bad dtstart", http.MethodPatch, api.APIVersionPrefix + "/schedules/sched-1",
			`{"dtstart":"soon"}`},
		{"patch bad dtend", http.MethodPatch, api.APIVersionPrefix + "/schedules/sched-1",
			`{"dtend":"later"}`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			w := doScheduleRequest(t, router, tc.method, tc.target, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s = %d, want 400: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

// TestScheduleWriteAppliesEveryOptionalField proves each pointer field is
// actually read, rather than declared and then forgotten in the fold.
func TestScheduleWriteAppliesEveryOptionalField(t *testing.T) {
	store := newStubScheduleStore()
	router := scheduleRouter(t, store)

	body := `{"name":"full","description":"every field","enabled":false,
	          "rrule":"FREQ=WEEKLY;BYDAY=MO","exclusions":["FREQ=MONTHLY;BYMONTHDAY=1"],
	          "timezone":"Europe/London","dtstart":"2024-03-08T09:00:00Z",
	          "dtend":"2025-01-01T00:00:00Z","template":42,"saved_config":9}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST = %d, want 201: %s", w.Code, w.Body.String())
	}

	got := store.gotCreate
	if got.Description != "every field" {
		t.Errorf("description = %q", got.Description)
	}
	if got.Enabled {
		t.Error("an explicit enabled:false was not applied")
	}
	if len(got.Exclusions) != 1 {
		t.Errorf("exclusions = %v", got.Exclusions)
	}
	if got.Timezone != "Europe/London" {
		t.Errorf("timezone = %q", got.Timezone)
	}
	if got.SavedConfigID != 9 {
		t.Errorf("saved_config = %d", got.SavedConfigID)
	}
	if got.DTEnd == nil {
		t.Fatal("dtend was not applied")
	}

	// The response must carry the optional fields back, including the
	// dtend and last_fired branches of the projection.
	stored := got
	stored.ScheduleID = "sched-full"
	lastFired := time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)
	stored.LastFired = &lastFired
	readRouter := scheduleRouter(t, newStubScheduleStore(stored))
	w = doScheduleRequest(t, readRouter, http.MethodGet, api.APIVersionPrefix+"/schedules/sched-full", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", w.Code, w.Body.String())
	}
	var dto map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto["dtend"] == nil {
		t.Error("dtend is absent from the response")
	}
	if dto["last_fired"] != "2024-03-01T09:00:00Z" {
		t.Errorf("last_fired = %v", dto["last_fired"])
	}
}

// TestScheduleProjectionSurvivesAnUnloadableZone proves a zone that no
// longer loads costs the caller only the local reading, not the record: the
// UTC time is still true and still useful.
func TestScheduleProjectionSurvivesAnUnloadableZone(t *testing.T) {
	broken := testSchedule("sched-1")
	broken.Timezone = "Mars/Olympus"
	router := scheduleRouter(t, newStubScheduleStore(broken))

	w := doScheduleRequest(t, router, http.MethodGet, api.APIVersionPrefix+"/schedules/sched-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200: %s", w.Code, w.Body.String())
	}
	var dto map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto["next_run"] != "2024-03-11T13:00:00Z" {
		t.Errorf("next_run = %v, want the UTC reading to survive", dto["next_run"])
	}
	if _, present := dto["next_run_local"]; present {
		t.Error("a local reading was rendered for a zone that does not load")
	}
}

// TestScheduleStoreFailuresMapToStatuses covers each handler's error branch
// against both an unknown failure and a not-found.
func TestScheduleStoreFailuresMapToStatuses(t *testing.T) {
	boom := errors.New("the database is on fire")

	t.Run("list", func(t *testing.T) {
		store := newStubScheduleStore()
		store.listErr = boom
		w := doScheduleRequest(t, scheduleRouter(t, store), http.MethodGet, api.APIVersionPrefix+"/schedules", "")
		if w.Code != http.StatusInternalServerError {
			t.Errorf("= %d, want 500", w.Code)
		}
	})

	t.Run("update on a missing schedule", func(t *testing.T) {
		w := doScheduleRequest(t, scheduleRouter(t, newStubScheduleStore()), http.MethodPatch,
			api.APIVersionPrefix+"/schedules/missing", `{"name":"x"}`)
		if w.Code != http.StatusNotFound {
			t.Errorf("= %d, want 404", w.Code)
		}
	})

	t.Run("update failure", func(t *testing.T) {
		store := newStubScheduleStore(testSchedule("sched-1"))
		store.updateErr = boom
		w := doScheduleRequest(t, scheduleRouter(t, store), http.MethodPatch,
			api.APIVersionPrefix+"/schedules/sched-1", `{"name":"x"}`)
		if w.Code != http.StatusInternalServerError {
			t.Errorf("= %d, want 500", w.Code)
		}
	})

	t.Run("delete failure", func(t *testing.T) {
		store := newStubScheduleStore(testSchedule("sched-1"))
		store.deleteErr = boom
		w := doScheduleRequest(t, scheduleRouter(t, store), http.MethodDelete,
			api.APIVersionPrefix+"/schedules/sched-1", "")
		if w.Code != http.StatusInternalServerError {
			t.Errorf("= %d, want 500", w.Code)
		}
	})

	t.Run("occurrences on a missing schedule", func(t *testing.T) {
		w := doScheduleRequest(t, scheduleRouter(t, newStubScheduleStore()), http.MethodGet,
			api.APIVersionPrefix+"/schedules/missing/occurrences", "")
		if w.Code != http.StatusNotFound {
			t.Errorf("= %d, want 404", w.Code)
		}
	})

	t.Run("occurrences failure", func(t *testing.T) {
		store := newStubScheduleStore(testSchedule("sched-1"))
		store.occErr = boom
		w := doScheduleRequest(t, scheduleRouter(t, store), http.MethodGet,
			api.APIVersionPrefix+"/schedules/sched-1/occurrences", "")
		if w.Code != http.StatusInternalServerError {
			t.Errorf("= %d, want 500", w.Code)
		}
	})

	t.Run("occurrences bad limit", func(t *testing.T) {
		store := newStubScheduleStore(testSchedule("sched-1"))
		w := doScheduleRequest(t, scheduleRouter(t, store), http.MethodGet,
			api.APIVersionPrefix+"/schedules/sched-1/occurrences?limit=0", "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("= %d, want 400", w.Code)
		}
	})

	t.Run("an untyped invalid error is still a 400", func(t *testing.T) {
		store := newStubScheduleStore()
		// ErrInvalid without a FieldError under it: the handler must still
		// answer 400 rather than fall through to a 500.
		store.createErr = schedule.ErrInvalid
		body := `{"name":"n","template":42,"rrule":"FREQ=DAILY","dtstart":"2024-03-08T09:00:00Z"}`
		w := doScheduleRequest(t, scheduleRouter(t, store), http.MethodPost,
			api.APIVersionPrefix+"/schedules", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("= %d, want 400: %s", w.Code, w.Body.String())
		}
	})
}

// TestNewScheduleHandlerDefaultsItsLogger covers the nil-logger fallback
// every other handler here has.
func TestNewScheduleHandlerDefaultsItsLogger(t *testing.T) {
	if h := api.NewScheduleHandler(newStubScheduleStore(), stubScheduleTemplates{}, nil); h == nil {
		t.Fatal("NewScheduleHandler returned nil")
	}
}

// TestSchedulePreviewDefaultsAndBounds covers the from/count defaulting and
// the dtend truncation branch.
func TestSchedulePreviewDefaultsAndBounds(t *testing.T) {
	router := scheduleRouter(t, newStubScheduleStore())

	// No `from` and no `count`: from defaults to now, count to ten. An
	// anchor in the past with an unbounded daily rule always has upcoming
	// occurrences, so this must come back full.
	body := `{"rrule":"FREQ=DAILY","dtstart":"2020-01-01T09:00:00Z"}`
	w := doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules/preview", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, want 200: %s", w.Code, w.Body.String())
	}
	var got struct {
		Occurrences []map[string]string `json:"occurrences"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Occurrences) != 10 {
		t.Errorf("got %d occurrences, want the default of 10", len(got.Occurrences))
	}

	// dtend truncates the preview rather than being ignored.
	body = `{"rrule":"FREQ=DAILY","dtstart":"2024-03-01T09:00:00Z","from":"2024-03-01T00:00:00Z",
	         "dtend":"2024-03-04T00:00:00Z","count":10}`
	w = doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules/preview", body)
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d, want 200: %s", w.Code, w.Body.String())
	}
	got.Occurrences = nil
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Occurrences) != 3 {
		t.Errorf("got %d occurrences, want 3 bounded by dtend", len(got.Occurrences))
	}

	// A bad `from` is refused rather than silently replaced by now.
	body = `{"rrule":"FREQ=DAILY","dtstart":"2024-03-01T09:00:00Z","from":"whenever"}`
	w = doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules/preview", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a bad from = %d, want 400", w.Code)
	}

	// So is a bad `dtend`, which is its own parse and its own branch.
	body = `{"rrule":"FREQ=DAILY","dtstart":"2024-03-01T09:00:00Z","dtend":"eventually"}`
	w = doScheduleRequest(t, router, http.MethodPost, api.APIVersionPrefix+"/schedules/preview", body)
	if w.Code != http.StatusBadRequest {
		t.Errorf("a bad dtend = %d, want 400", w.Code)
	}
}

// TestScheduleListAcceptsAValidLimitAndCursor covers the accepting half of
// the query parsing, which the refusal tests alone leave unexercised.
func TestScheduleListAcceptsAValidLimitAndCursor(t *testing.T) {
	store := newStubScheduleStore(testSchedule("sched-1"))
	router := scheduleRouter(t, store)

	w := doScheduleRequest(t, router, http.MethodGet,
		api.APIVersionPrefix+"/schedules?limit=25&after=sched-0", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET = %d, want 200: %s", w.Code, w.Body.String())
	}

	w = doScheduleRequest(t, router, http.MethodGet,
		api.APIVersionPrefix+"/schedules/sched-1/occurrences?limit=5", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET occurrences = %d, want 200: %s", w.Code, w.Body.String())
	}
}

// TestTemplateDeleteReportsAScheduleHoldingIt covers the status mapping for
// the one edge Phase 23 added that is not cascaded. Without it, deleting a
// scheduled template answers 500, which reads as "the server is broken"
// rather than "something still needs this".
func TestTemplateDeleteReportsAScheduleHoldingIt(t *testing.T) {
	store := &stubTemplateStore{deleteErr: launch.ErrInUse}
	handler := api.NewTemplateHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.DeleteTemplate.Route(handler.Delete)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	w := doScheduleRequest(t, router, http.MethodDelete, api.APIVersionPrefix+"/templates/12", "")
	if w.Code != http.StatusConflict {
		t.Fatalf("DELETE a scheduled template = %d, want 409: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "schedule") {
		t.Errorf("the message does not say what is holding the template: %s", w.Body.String())
	}
}

// stubTemplateStore is the minimum api.TemplateStore this one status
// assertion needs.
type stubTemplateStore struct {
	deleteErr error
}

func (s *stubTemplateStore) Create(context.Context, launch.Template) (launch.Template, error) {
	return launch.Template{}, nil
}
func (s *stubTemplateStore) Get(context.Context, int) (launch.Template, error) {
	return launch.Template{}, nil
}
func (s *stubTemplateStore) List(context.Context, launch.Query) ([]launch.Template, error) {
	return nil, nil
}
func (s *stubTemplateStore) Update(context.Context, launch.Template) error { return nil }
func (s *stubTemplateStore) Delete(context.Context, int) error             { return s.deleteErr }
func (s *stubTemplateStore) SavedConfigs(context.Context, int) ([]launch.SavedConfig, error) {
	return nil, nil
}
func (s *stubTemplateStore) SaveConfig(_ context.Context, cfg launch.SavedConfig) (launch.SavedConfig, error) {
	return cfg, nil
}
