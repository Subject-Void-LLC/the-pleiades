// This file holds PATCH /announcements/{id} to the partial-update rules the
// other PATCH routes follow, through the real router and the real ent-backed
// announcement store.
//
// The route used to decode its create body, so a field the body left out was
// written back empty: a PATCH naming only the title answered 500 for the
// body it blanked, one naming only the body reset the level to info and
// erased the window, and so turned a scheduled change freeze into one
// showing now and forever. A blank title or body answered 500 on create and
// on update alike.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/announce"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
)

// announcementFixtureSeq names each fixture's in-memory database.
var announcementFixtureSeq atomic.Int64

// announcementFixture is the real router over the real ent-backed store,
// with one organization.
type announcementFixture struct {
	client *ent.Client
	store  announce.Store
	router http.Handler
	orgID  int
}

// newAnnouncementFixture wires the announcement routes over a fresh database.
func newAnnouncementFixture(t *testing.T) *announcementFixture {
	t.Helper()

	client := newSerializedSQLiteClient(t, fmt.Sprintf("announcements-%d", announcementFixtureSeq.Add(1)))
	org := client.Organization.Create().SetName("network").SaveX(context.Background())
	store := announce.NewEntStore(client)
	handler := api.NewAnnouncementHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.CreateAnnouncement.Route(handler.Create),
			apispec.UpdateAnnouncement.Route(handler.Update),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return &announcementFixture{client: client, store: store, router: router, orgID: org.ID}
}

// announcementPath is the URL of one announcement.
func announcementPath(id int) string { return fmt.Sprintf("/api/v1/announcements/%d", id) }

// do sends one request through the router with body as its raw JSON.
func (f *announcementFixture) do(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	return serveRaw(t, f.router, method, path, body)
}

// freezeBody is a scheduled, tenant-scoped change freeze.
func (f *announcementFixture) freezeBody() string {
	return fmt.Sprintf(`{"title":"Change freeze","body":"No dispatches to core.","level":"warning","organization":%d,
		"starts_at":"2031-01-10T00:00:00Z","ends_at":"2031-01-12T00:00:00Z"}`, f.orgID)
}

// create posts an announcement that must be accepted and returns its id.
func (f *announcementFixture) create(t *testing.T, body string) int {
	t.Helper()
	status, resp := f.do(t, http.MethodPost, "/api/v1/announcements", body)
	if status != http.StatusCreated {
		t.Fatalf("POST %s: status %d, body %s", body, status, resp)
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(resp, &created); err != nil {
		t.Fatalf("decoding the created announcement: %v", err)
	}
	return created.ID
}

// read loads an announcement through the store, since no route reads one.
func (f *announcementFixture) read(t *testing.T, id int) announce.Announcement {
	t.Helper()
	got, err := f.store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("reading announcement %d: %v", id, err)
	}
	return got
}

// patchOK sends a PATCH that must succeed.
func (f *announcementFixture) patchOK(t *testing.T, id int, body string) {
	t.Helper()
	if status, resp := f.do(t, http.MethodPatch, announcementPath(id), body); status != http.StatusOK {
		t.Fatalf("PATCH %s: status %d, want 200; body %s", body, status, resp)
	}
}

// refusedUnchanged sends a PATCH that must be refused with want, with a
// response saying each of mentions, and leave the announcement as it was.
func (f *announcementFixture) refusedUnchanged(t *testing.T, id int, body string, want int, mentions ...string) {
	t.Helper()
	before := f.read(t, id)
	status, resp := f.do(t, http.MethodPatch, announcementPath(id), body)
	if status != want {
		t.Fatalf("PATCH %s: status %d, want %d; body %s", body, status, want, resp)
	}
	for _, mention := range mentions {
		if !strings.Contains(string(resp), mention) {
			t.Errorf("PATCH %s: the refusal %s does not say %q", body, resp, mention)
		}
	}
	if after := f.read(t, id); !reflect.DeepEqual(after, before) {
		t.Errorf("a refused PATCH changed the announcement:\n before %+v\n after  %+v", before, after)
	}
}

// at parses an RFC 3339 instant for a comparison.
func at(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return parsed
}

// assertWindow fails unless a's window is exactly starts to ends, where ""
// means no bound.
func assertWindow(t *testing.T, a announce.Announcement, starts, ends string) {
	t.Helper()
	check := func(name string, got *time.Time, want string) {
		switch {
		case want == "" && got != nil:
			t.Errorf("%s = %v, want none", name, got)
		case want != "" && (got == nil || !got.Equal(at(t, want))):
			t.Errorf("%s = %v, want %s", name, got, want)
		}
	}
	check("starts_at", a.StartsAt, starts)
	check("ends_at", a.EndsAt, ends)
}

// TestAnnouncementPatch_KeepsWhatItDoesNotName is the partial update: a
// PATCH naming one field keeps the body, the level and the window.
func TestAnnouncementPatch_KeepsWhatItDoesNotName(t *testing.T) {
	t.Parallel()
	f := newAnnouncementFixture(t)
	id := f.create(t, f.freezeBody())

	f.patchOK(t, id, `{"title":"Change freeze, extended"}`)
	got := f.read(t, id)
	if got.Title != "Change freeze, extended" || got.Body != "No dispatches to core." || got.Level != announce.LevelWarning {
		t.Errorf("after a title PATCH: %+v, want the body and level kept", got)
	}
	if got.OrganizationID != f.orgID || got.Author != "test-user" {
		t.Errorf("after a title PATCH: organization %d author %q, want %d and test-user", got.OrganizationID, got.Author, f.orgID)
	}
	assertWindow(t, got, "2031-01-10T00:00:00Z", "2031-01-12T00:00:00Z")

	f.patchOK(t, id, `{"ends_at":"2031-01-14T00:00:00Z"}`)
	assertWindow(t, f.read(t, id), "2031-01-10T00:00:00Z", "2031-01-14T00:00:00Z")
}

// TestAnnouncementPatch_AnEmptyBoundClearsIt is how a window is opened: ""
// removes a bound, as leaving it out does on create.
func TestAnnouncementPatch_AnEmptyBoundClearsIt(t *testing.T) {
	t.Parallel()
	f := newAnnouncementFixture(t)
	id := f.create(t, f.freezeBody())

	f.patchOK(t, id, `{"ends_at":""}`)
	assertWindow(t, f.read(t, id), "2031-01-10T00:00:00Z", "")
	f.patchOK(t, id, `{"starts_at":"","level":"critical"}`)
	got := f.read(t, id)
	assertWindow(t, got, "", "")
	if got.Level != announce.LevelCritical {
		t.Errorf("level = %q, want critical", got.Level)
	}
}

// TestAnnouncementPatch_RefusesWhatItCannotApply covers every refusal, each
// leaving the announcement as it was. The window is checked on the merged
// announcement, so moving one bound past the stored other is refused.
func TestAnnouncementPatch_RefusesWhatItCannotApply(t *testing.T) {
	t.Parallel()
	f := newAnnouncementFixture(t)
	id := f.create(t, f.freezeBody())

	cases := map[string]struct {
		body     string
		mentions []string
	}{
		"a null title":            {`{"title":null}`, []string{`\"title\"`, "null"}},
		"a null level":            {`{"level":null}`, []string{`\"level\"`}},
		"a null end":              {`{"ends_at":null}`, []string{`\"ends_at\"`}},
		"a null start":            {`{"starts_at":null,"title":"x"}`, []string{`\"starts_at\"`}},
		"an empty title":          {`{"title":""}`, []string{"title"}},
		"a blank body":            {`{"body":"  \n "}`, []string{"body"}},
		"the author":              {`{"author":"mallory"}`, nil},
		"the id":                  {`{"id":9}`, nil},
		"a field named twice":     {`{"title":"a","Title":"b"}`, []string{`\"title\"`, "more than once"}},
		"a bare null":             {`null`, nil},
		"a string":                {`"freeze"`, nil},
		"another organization":    {fmt.Sprintf(`{"organization":%d}`, f.orgID+1), []string{"organization cannot be changed"}},
		"made system-wide":        {`{"organization":0}`, []string{"organization cannot be changed"}},
		"a malformed end":         {`{"ends_at":"Monday"}`, []string{"ends_at", "RFC 3339"}},
		"an end before the start": {`{"ends_at":"2031-01-09T00:00:00Z"}`, []string{"ends_at must be after starts_at"}},
		"a start after the end":   {`{"starts_at":"2031-01-13T00:00:00Z"}`, []string{"ends_at must be after starts_at"}},
		"a NUL in the title":      {`{"title":"a\u0000"}`, []string{"title may not contain a NUL"}},
		"a NUL in the body":       {`{"body":"\u0000b"}`, []string{"body may not contain a NUL"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f.refusedUnchanged(t, id, tc.body, http.StatusBadRequest, tc.mentions...)
		})
	}
}

// TestAnnouncementPatch_AcceptsTheStoredOrganizationAsAnEcho holds the fixed
// field's other half, for a tenant's announcement and a system-wide one,
// whose organization is 0.
func TestAnnouncementPatch_AcceptsTheStoredOrganizationAsAnEcho(t *testing.T) {
	t.Parallel()
	f := newAnnouncementFixture(t)
	scoped := f.create(t, f.freezeBody())
	system := f.create(t, `{"title":"Maintenance","body":"The API restarts at noon."}`)

	f.patchOK(t, scoped, fmt.Sprintf(`{"organization":%d,"body":"Core only."}`, f.orgID))
	if got := f.read(t, scoped); got.OrganizationID != f.orgID || got.Body != "Core only." {
		t.Errorf("after an echo: %+v", got)
	}
	f.patchOK(t, system, `{"organization":0,"level":"changed"}`)
	if got := f.read(t, system); got.OrganizationID != 0 || got.Level != announce.LevelChanged {
		t.Errorf("after an echo: %+v", got)
	}
	f.refusedUnchanged(t, system, fmt.Sprintf(`{"organization":%d}`, f.orgID), http.StatusBadRequest, "organization cannot be changed")
}

// TestAnnouncementPatch_AMissingAnnouncementIsNotFound answers 404.
func TestAnnouncementPatch_AMissingAnnouncementIsNotFound(t *testing.T) {
	t.Parallel()
	f := newAnnouncementFixture(t)
	if status, resp := f.do(t, http.MethodPatch, announcementPath(4242), `{"title":"x"}`); status != http.StatusNotFound {
		t.Fatalf("PATCH a missing announcement: status %d, want 404; body %s", status, resp)
	}
}

// TestAnnouncementCreate_RefusesWhatTheStoreCannotSave is the create route's
// share of the 500s: a blank title or body and an organization that does not
// exist are the caller's mistakes, answered 400.
func TestAnnouncementCreate_RefusesWhatTheStoreCannotSave(t *testing.T) {
	t.Parallel()
	f := newAnnouncementFixture(t)

	cases := map[string]struct {
		body     string
		mentions []string
	}{
		"no title":                {`{"body":"b"}`, []string{"title"}},
		"a blank title":           {`{"title":"  ","body":"b"}`, []string{"title"}},
		"no body":                 {`{"title":"t"}`, []string{"body"}},
		"an unknown organization": {`{"title":"t","body":"b","organization":999999}`, []string{"organization"}},
		"a NUL in the body":       {`{"title":"t","body":"b\u0000"}`, []string{"body may not contain a NUL"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			status, resp := f.do(t, http.MethodPost, "/api/v1/announcements", tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("POST %s: status %d, want 400; body %s", tc.body, status, resp)
			}
			for _, mention := range tc.mentions {
				if !strings.Contains(string(resp), mention) {
					t.Errorf("POST %s: the refusal %s does not say %q", tc.body, resp, mention)
				}
			}
		})
	}
	if n := f.client.Announcement.Query().CountX(context.Background()); n != 0 {
		t.Errorf("refused creates stored %d announcements, want 0", n)
	}
}
