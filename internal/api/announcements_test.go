// This file covers AnnouncementHandler: the live-by-default listing rule,
// the identity-derived author, the window validation that refuses a notice
// which could never be seen, and the fields an edit must not be able to
// rewrite.
//
// Routes are built from apispec's own Endpoint values, so a drift between
// the declared scope or pattern and what these tests exercise fails here.
// Note there is no GetAnnouncement endpoint to mount: the handler's
// repository has a Get, but nothing exposes one over HTTP yet.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
)

// stubAnnounceStore is an in-memory api.AnnouncementRepository that records
// the query and the value it was handed, and can be programmed to fail one
// operation at a time.
type stubAnnounceStore struct {
	items  map[int]announce.Announcement
	nextID int

	gotQuery announce.Query
	gotItem  announce.Announcement

	getCalls    int
	getErrAfter int

	createErr error
	getErr    error
	listErr   error
	updateErr error
	deleteErr error
}

// newStubAnnounceStore builds a store preloaded with items, keyed by ID.
func newStubAnnounceStore(items ...announce.Announcement) *stubAnnounceStore {
	s := &stubAnnounceStore{items: map[int]announce.Announcement{}, nextID: 1}
	for _, a := range items {
		s.items[a.ID] = a
		if a.ID >= s.nextID {
			s.nextID = a.ID + 1
		}
	}
	return s
}

// Create stores a new announcement, assigning it the next free id.
func (s *stubAnnounceStore) Create(_ context.Context, a announce.Announcement) (announce.Announcement, error) {
	s.gotItem = a
	if s.createErr != nil {
		return announce.Announcement{}, s.createErr
	}
	a.ID = s.nextID
	s.nextID++
	s.items[a.ID] = a
	return a, nil
}

// Get returns one stored announcement, or announce.ErrNotFound.
//
// getErrAfter, when positive, fails every call past that many. Update reads
// twice (once to carry the author and organization forward, once to render
// what was stored), and the second read is the only way to reach the branch
// where a write lands but the response cannot be built.
func (s *stubAnnounceStore) Get(_ context.Context, id int) (announce.Announcement, error) {
	s.getCalls++
	if s.getErr != nil {
		return announce.Announcement{}, s.getErr
	}
	if s.getErrAfter > 0 && s.getCalls > s.getErrAfter {
		return announce.Announcement{}, errors.New("deliberate re-read failure")
	}
	a, ok := s.items[id]
	if !ok {
		return announce.Announcement{}, announce.ErrNotFound
	}
	return a, nil
}

// List returns every stored announcement in ascending id order.
func (s *stubAnnounceStore) List(_ context.Context, q announce.Query) ([]announce.Announcement, error) {
	s.gotQuery = q
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]announce.Announcement, 0, len(s.items))
	for id := 1; id < s.nextID; id++ {
		if a, ok := s.items[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// Update replaces a stored announcement.
func (s *stubAnnounceStore) Update(_ context.Context, a announce.Announcement) error {
	s.gotItem = a
	if s.updateErr != nil {
		return s.updateErr
	}
	s.items[a.ID] = a
	return nil
}

// Delete removes a stored announcement.
func (s *stubAnnounceStore) Delete(_ context.Context, id int) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	delete(s.items, id)
	return nil
}

// announcementRouter mounts every announcement endpoint through the real
// api.NewRouter.
func announcementRouter(t *testing.T, store api.AnnouncementRepository) http.Handler {
	t.Helper()
	return announcementRouterWithAuth(t, store, alwaysAuthenticated)
}

// announcementRouterWithAuth is announcementRouter with the identity
// middleware left to the caller, so a test can reach Create's unauthorized
// branch.
func announcementRouterWithAuth(t *testing.T, store api.AnnouncementRepository, authMW func(http.Handler) http.Handler) http.Handler {
	t.Helper()
	handler := api.NewAnnouncementHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      authMW,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListAnnouncements.Route(handler.List),
			apispec.CreateAnnouncement.Route(handler.Create),
			apispec.UpdateAnnouncement.Route(handler.Update),
			apispec.DeleteAnnouncement.Route(handler.Delete),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return router
}

// testAnnouncement builds a stored announcement with a closed window.
func testAnnouncement(id int) announce.Announcement {
	starts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ends := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	return announce.Announcement{
		ID:             id,
		Title:          "change freeze",
		Body:           "no production dispatches until further notice",
		Level:          announce.LevelCritical,
		Author:         "admin@example.com",
		OrganizationID: 7,
		StartsAt:       &starts,
		EndsAt:         &ends,
	}
}

// decodedAnnouncement is the shape these tests read back off the wire,
// declared separately from announcementDTO so a wrong JSON tag fails.
type decodedAnnouncement struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Body         string `json:"body"`
	Level        string `json:"level"`
	Author       string `json:"author"`
	Organization int    `json:"organization"`
	StartsAt     string `json:"starts_at"`
	EndsAt       string `json:"ends_at"`
}

// TestAnnouncementHandler_ListIsLiveByDefault proves the default query
// carries a real clock reading, so an operator opening the dashboard is not
// shown a change freeze that ended last month, and that ?all=true clears it
// for the managing case.
func TestAnnouncementHandler_ListIsLiveByDefault(t *testing.T) {
	store := newStubAnnounceStore(testAnnouncement(1))
	router := announcementRouter(t, store)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/announcements", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if store.gotQuery.LiveAt.IsZero() {
		t.Error("default listing did not constrain the window, so an expired notice would be shown")
	}

	rec = doJSON(t, router, http.MethodGet, "/api/v1/announcements?all=true", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !store.gotQuery.LiveAt.IsZero() {
		t.Error("?all=true still constrained the window")
	}
}

// TestAnnouncementHandler_ListRendersTimestampsAsRFC3339 proves the wire
// format is the one a client can parse without guessing, and that an
// announcement with no window omits both keys rather than rendering a zero
// time that reads as 1 January year one.
func TestAnnouncementHandler_ListRendersTimestampsAsRFC3339(t *testing.T) {
	noWindow := announce.Announcement{ID: 2, Title: "notice", Body: "b", Level: announce.LevelInfo, Author: "a"}
	router := announcementRouter(t, newStubAnnounceStore(testAnnouncement(1), noWindow))

	rec := doJSON(t, router, http.MethodGet, "/api/v1/announcements?all=true", "")
	var got struct {
		Announcements []decodedAnnouncement `json:"announcements"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Announcements) != 2 {
		t.Fatalf("returned %d announcements, want 2", len(got.Announcements))
	}
	if _, err := time.Parse(time.RFC3339, got.Announcements[0].StartsAt); err != nil {
		t.Errorf("starts_at %q is not RFC 3339: %v", got.Announcements[0].StartsAt, err)
	}
	if got.Announcements[1].StartsAt != "" || got.Announcements[1].EndsAt != "" {
		t.Errorf("an announcement with no window rendered timestamps: %+v", got.Announcements[1])
	}
	if got.Announcements[0].Level != string(announce.LevelCritical) {
		t.Errorf("level = %q, want %q", got.Announcements[0].Level, announce.LevelCritical)
	}
}

func TestAnnouncementHandler_ListRejectsMalformedParameters(t *testing.T) {
	router := announcementRouter(t, newStubAnnounceStore())

	for _, target := range []string{
		"/api/v1/announcements?all=maybe",
		"/api/v1/announcements?limit=0",
		"/api/v1/announcements?limit=nope",
	} {
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400", target, rec.Code)
		}
	}
}

func TestAnnouncementHandler_ListCarriesTheLimitToTheStore(t *testing.T) {
	store := newStubAnnounceStore(testAnnouncement(1))
	router := announcementRouter(t, store)

	if rec := doJSON(t, router, http.MethodGet, "/api/v1/announcements?limit=5", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if store.gotQuery.Limit != 5 {
		t.Errorf("store saw limit %d, want 5", store.gotQuery.Limit)
	}
}

func TestAnnouncementHandler_ListStoreFailureIsAGeneric500(t *testing.T) {
	store := newStubAnnounceStore()
	store.listErr = errors.New("pq: relation \"announcements\" does not exist")
	router := announcementRouter(t, store)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/announcements", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "relation") {
		t.Errorf("store error text leaked: %s", rec.Body.String())
	}
}

// TestAnnouncementHandler_CreateTakesTheAuthorFromTheIdentity is the point
// of the write DTO having no author field: an attribution a submitter could
// name is worthless on a message instructing every operator how to run
// production.
func TestAnnouncementHandler_CreateTakesTheAuthorFromTheIdentity(t *testing.T) {
	store := newStubAnnounceStore()
	router := announcementRouter(t, store)

	body := `{"title":"freeze","body":"hold all changes","level":"critical","organization":3,"starts_at":"2026-01-01T00:00:00Z","ends_at":"2026-01-02T00:00:00Z"}`
	rec := doJSON(t, router, http.MethodPost, "/api/v1/announcements", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if store.gotItem.Author != "test-user" {
		t.Errorf("author = %q, want the authenticated subject \"test-user\"", store.gotItem.Author)
	}
	if store.gotItem.Level != announce.LevelCritical {
		t.Errorf("level = %q, want critical", store.gotItem.Level)
	}
	if store.gotItem.OrganizationID != 3 {
		t.Errorf("organization = %d, want 3", store.gotItem.OrganizationID)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/announcements/1" {
		t.Errorf("Location = %q, want /api/v1/announcements/1", loc)
	}
}

// TestAnnouncementHandler_CreateNormalisesAnUnknownLevel proves
// announce.ParseLevel's total mapping reaches the handler: an unrecognised
// level becomes info rather than a 400. A notice is worth posting even when
// its severity was mistyped, and the badge set is closed so an arbitrary
// string could never render anyway.
func TestAnnouncementHandler_CreateNormalisesAnUnknownLevel(t *testing.T) {
	store := newStubAnnounceStore()
	router := announcementRouter(t, store)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/announcements",
		`{"title":"t","body":"b","level":"apocalyptic"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if store.gotItem.Level != announce.LevelInfo {
		t.Errorf("level = %q, want info", store.gotItem.Level)
	}
}

// TestAnnouncementHandler_CreateRefusesAWindowThatIsNeverLive is the
// validation that matters most here. A window ending before it starts is
// never live, so accepting it would post an urgent notice that is invisible
// from the moment it is written, which is the worst possible failure for a
// control whose entire job is being seen.
func TestAnnouncementHandler_CreateRefusesAWindowThatIsNeverLive(t *testing.T) {
	store := newStubAnnounceStore()
	router := announcementRouter(t, store)

	for _, tc := range []struct{ name, body string }{
		{"ends before starts", `{"title":"t","body":"b","starts_at":"2026-06-01T00:00:00Z","ends_at":"2026-01-01T00:00:00Z"}`},
		{"ends equals starts", `{"title":"t","body":"b","starts_at":"2026-06-01T00:00:00Z","ends_at":"2026-06-01T00:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, router, http.MethodPost, "/api/v1/announcements", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if len(store.items) != 0 {
				t.Error("a refused announcement still reached the store")
			}
		})
	}
}

func TestAnnouncementHandler_CreateRejectsMalformedTimestamps(t *testing.T) {
	router := announcementRouter(t, newStubAnnounceStore())

	for _, tc := range []struct{ name, body, wantField string }{
		{"starts_at", `{"title":"t","body":"b","starts_at":"last tuesday"}`, "starts_at"},
		{"ends_at", `{"title":"t","body":"b","ends_at":"soon"}`, "ends_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSON(t, router, http.MethodPost, "/api/v1/announcements", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			// The message names which field was wrong, because "one of your
			// two timestamps is malformed" is not an actionable answer.
			if !strings.Contains(rec.Body.String(), tc.wantField) {
				t.Errorf("error did not name %q: %s", tc.wantField, rec.Body.String())
			}
		})
	}
}

func TestAnnouncementHandler_CreateWithoutAnIdentityIs401(t *testing.T) {
	anonymous := func(next http.Handler) http.Handler { return next }
	router := announcementRouterWithAuth(t, newStubAnnounceStore(), anonymous)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/announcements", `{"title":"t","body":"b"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestAnnouncementHandler_CreateRejectsMalformedJSON(t *testing.T) {
	router := announcementRouter(t, newStubAnnounceStore())

	rec := doJSON(t, router, http.MethodPost, "/api/v1/announcements", `{"title":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestAnnouncementHandler_CreateStoreFailureIsAGeneric500(t *testing.T) {
	store := newStubAnnounceStore()
	store.createErr = errors.New("pq: null value in column \"title\"")
	router := announcementRouter(t, store)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/announcements", `{"title":"t","body":"b"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "column") {
		t.Errorf("store error text leaked: %s", rec.Body.String())
	}
}

// TestAnnouncementHandler_UpdateCarriesAuthorAndOrganizationForward proves
// the two facts an edit must not be able to rewrite. Rewriting an
// attribution is what makes one worthless, and moving an announcement
// between tenants would silently change who it reaches.
func TestAnnouncementHandler_UpdateCarriesAuthorAndOrganizationForward(t *testing.T) {
	store := newStubAnnounceStore(testAnnouncement(1))
	router := announcementRouter(t, store)

	body := `{"title":"lifted","body":"freeze over","level":"info","organization":999}`
	rec := doJSON(t, router, http.MethodPatch, "/api/v1/announcements/1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if store.gotItem.Author != "admin@example.com" {
		t.Errorf("author = %q, want the stored value carried forward", store.gotItem.Author)
	}
	if store.gotItem.OrganizationID != 7 {
		t.Errorf("organization = %d, want the stored 7 rather than the submitted 999", store.gotItem.OrganizationID)
	}
	if store.gotItem.ID != 1 {
		t.Errorf("id = %d, want 1", store.gotItem.ID)
	}
	if store.gotItem.Title != "lifted" {
		t.Errorf("title = %q, want the submitted value", store.gotItem.Title)
	}
	// A cleared window must actually clear, so an announcement can be made
	// permanent by removing its end date rather than only by deleting it.
	if store.gotItem.StartsAt != nil || store.gotItem.EndsAt != nil {
		t.Errorf("window survived an edit that omitted it: %+v", store.gotItem)
	}
}

func TestAnnouncementHandler_UpdateErrorBranches(t *testing.T) {
	t.Run("malformed id", func(t *testing.T) {
		router := announcementRouter(t, newStubAnnounceStore())
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/announcements/abc", `{"title":"t","body":"b"}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		router := announcementRouter(t, newStubAnnounceStore(testAnnouncement(1)))
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/announcements/1", `{"title":`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing record is a 404", func(t *testing.T) {
		router := announcementRouter(t, newStubAnnounceStore())
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/announcements/9", `{"title":"t","body":"b"}`)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("invalid window is refused before the write", func(t *testing.T) {
		store := newStubAnnounceStore(testAnnouncement(1))
		router := announcementRouter(t, store)
		body := `{"title":"t","body":"b","starts_at":"2026-06-01T00:00:00Z","ends_at":"2026-01-01T00:00:00Z"}`
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/announcements/1", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		if store.items[1].Title != "change freeze" {
			t.Error("a refused edit still modified the stored announcement")
		}
	})

	t.Run("write failure", func(t *testing.T) {
		store := newStubAnnounceStore(testAnnouncement(1))
		store.updateErr = errors.New("deliberate store failure")
		router := announcementRouter(t, store)
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/announcements/1", `{"title":"t","body":"b"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
	})

	// The write lands and the read back to render it fails. Reported as a
	// 500 rather than a 200 carrying the pre-edit record, which would tell
	// the caller their change did not apply when it did.
	t.Run("re-read after a successful write", func(t *testing.T) {
		store := newStubAnnounceStore(testAnnouncement(1))
		store.getErrAfter = 1
		router := announcementRouter(t, store)
		rec := doJSON(t, router, http.MethodPatch, "/api/v1/announcements/1", `{"title":"lifted","body":"b"}`)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500: %s", rec.Code, rec.Body.String())
		}
		if store.items[1].Title != "lifted" {
			t.Error("the write did not land, so this test is not exercising the branch it claims to")
		}
	})
}

// TestAnnouncementHandler_CreateFailsClosedWithoutAnIdentity calls the
// handler directly, because api.RequireScope answers 401 on a missing
// identity before any handler runs and makes this branch unreachable on its
// declared route. It exists as defence in depth for a route mounted outside
// that middleware, where the alternative is publishing an instruction to
// every operator with an empty author.
func TestAnnouncementHandler_CreateFailsClosedWithoutAnIdentity(t *testing.T) {
	store := newStubAnnounceStore()
	handler := api.NewAnnouncementHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/announcements", strings.NewReader(`{"title":"t","body":"b"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.Create(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if len(store.items) != 0 {
		t.Error("an unauthenticated create still reached the store")
	}
}

func TestAnnouncementHandler_Delete(t *testing.T) {
	t.Run("removes the record", func(t *testing.T) {
		store := newStubAnnounceStore(testAnnouncement(1))
		router := announcementRouter(t, store)

		rec := doJSON(t, router, http.MethodDelete, "/api/v1/announcements/1", "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
		}
		if _, still := store.items[1]; still {
			t.Error("announcement survived its own delete")
		}
	})

	t.Run("malformed id", func(t *testing.T) {
		router := announcementRouter(t, newStubAnnounceStore())
		rec := doJSON(t, router, http.MethodDelete, "/api/v1/announcements/0", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("missing record", func(t *testing.T) {
		store := newStubAnnounceStore()
		store.deleteErr = announce.ErrNotFound
		router := announcementRouter(t, store)
		rec := doJSON(t, router, http.MethodDelete, "/api/v1/announcements/9", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

// TestNewAnnouncementHandler_NilLoggerFallsBack proves the constructor's
// guard, so a caller that omits a logger gets the process default rather
// than a nil dereference on the first store failure.
func TestNewAnnouncementHandler_NilLoggerFallsBack(t *testing.T) {
	store := newStubAnnounceStore()
	store.listErr = errors.New("boom")
	handler := api.NewAnnouncementHandler(store, nil)

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes:    []api.Route{apispec.ListAnnouncements.Route(handler.List)},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	rec := doJSON(t, router, http.MethodGet, "/api/v1/announcements", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
