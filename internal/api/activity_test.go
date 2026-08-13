// This file covers the activity endpoints over a real ent-backed store and
// the real router.
//
// Real, for the reason access_test.go states: what the handler does is
// translate between the wire and internal/activity, and the parts worth
// proving are the refusals and the narrowing, both of which belong to the
// store and the query. A stub would prove the stub.
package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/activity"
	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/enttest"
	_ "github.com/mattn/go-sqlite3"
)

// activityRouter mounts the activity endpoints over a real store, seeded
// with three entries by two subjects against two kinds of object.
func activityRouter(t *testing.T) (http.Handler, activity.Store) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", fmt.Sprintf("file:apiactivity%s?mode=memory&cache=shared&_fk=1", t.Name()))
	t.Cleanup(func() { _ = client.Close() })

	store := activity.NewEntStore(client)
	h := api.NewActivityHandler(store, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListActivity.Route(h.ListActivity),
			apispec.GetActivityEntry.Route(h.GetActivityEntry),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	ctx := context.Background()
	for _, e := range []activity.Entry{
		{Actor: "ada@example.com", Action: activity.ActionCreated, ObjectKind: activity.KindOrganization, ObjectID: 1, ObjectName: "Network"},
		{Actor: "grace@example.com", Action: activity.ActionUpdated, ObjectKind: activity.KindTeam, ObjectID: 1, ObjectName: "Edge"},
		{Actor: "ada@example.com", Action: activity.ActionDeleted, ObjectKind: activity.KindTeam, ObjectID: 2, ObjectName: "Core"},
	} {
		if err := store.Record(ctx, e); err != nil {
			t.Fatalf("seeding the stream: %v", err)
		}
	}
	return router, store
}

// activityListDTO mirrors what the endpoint returns, as a client sees it.
type decodedActivity struct {
	Activity []struct {
		ID         int    `json:"id"`
		Actor      string `json:"actor"`
		Action     string `json:"action"`
		ObjectKind string `json:"object_kind"`
		ObjectID   int    `json:"object_id"`
		ObjectName string `json:"object_name"`
		At         string `json:"at"`
		Summary    string `json:"summary"`
	} `json:"activity"`
}

func decodeActivity(t *testing.T, body []byte) decodedActivity {
	t.Helper()
	var got decodedActivity
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding the activity listing: %v", err)
	}
	return got
}

func TestActivity_ListsNewestFirstWithASummary(t *testing.T) {
	router, _ := activityRouter(t)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/activity", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d: %s", rec.Code, rec.Body.String())
	}

	got := decodeActivity(t, rec.Body.Bytes())
	if len(got.Activity) != 3 {
		t.Fatalf("list returned %d entries, want 3", len(got.Activity))
	}
	if got.Activity[0].ObjectName != "Core" {
		t.Errorf("list put %q first, want the most recent entry (Core)", got.Activity[0].ObjectName)
	}

	// The summary is rendered by the domain type, so this API and the web
	// UI cannot word the same event two ways.
	if want := "ada@example.com deleted team Core"; got.Activity[0].Summary != want {
		t.Errorf("summary = %q, want %q", got.Activity[0].Summary, want)
	}
	if got.Activity[0].At == "" {
		t.Error("an entry carries no time, so the stream cannot say when anything happened")
	}
}

func TestActivity_NarrowsByActorAndByObject(t *testing.T) {
	router, _ := activityRouter(t)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/activity?actor=grace@example.com", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list by actor: status = %d", rec.Code)
	}
	if got := decodeActivity(t, rec.Body.Bytes()); len(got.Activity) != 1 {
		t.Errorf("narrowing to one subject returned %d entries, want 1", len(got.Activity))
	}

	// Both entries with object id 1 exist, one an organization and one a
	// team. Narrowing by kind and id must return the team's alone.
	rec = doJSON(t, router, http.MethodGet, "/api/v1/activity?object_kind=team&object_id=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list by object: status = %d", rec.Code)
	}
	got := decodeActivity(t, rec.Body.Bytes())
	if len(got.Activity) != 1 || got.Activity[0].ObjectName != "Edge" {
		t.Errorf("narrowing to team 1 returned %+v, want only Edge", got.Activity)
	}
}

func TestActivity_RefusesAMalformedNarrowingRatherThanIgnoringIt(t *testing.T) {
	router, _ := activityRouter(t)

	// Silently dropping an unparseable filter answers "the whole stream" to
	// a request that asked for one object's history, which is the shape of
	// answer nobody rechecks because it looks like data.
	for _, query := range []string{"after=abc", "limit=-1", "object_id=0", "after=0"} {
		rec := doJSON(t, router, http.MethodGet, "/api/v1/activity?"+query, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/v1/activity?%s = %d, want 400", query, rec.Code)
		}
	}
}

func TestActivity_GetsOneEntryAndReportsAMissingOne(t *testing.T) {
	router, _ := activityRouter(t)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/activity", "")
	listed := decodeActivity(t, rec.Body.Bytes())
	if len(listed.Activity) == 0 {
		t.Fatal("the seeded stream listed nothing")
	}
	id := listed.Activity[0].ID

	rec = doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/v1/activity/%d", id), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d: %s", rec.Code, rec.Body.String())
	}

	var one struct {
		ID         int    `json:"id"`
		ObjectName string `json:"object_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil {
		t.Fatalf("decoding the entry: %v", err)
	}
	if one.ID != id || one.ObjectName != "Core" {
		t.Errorf("get returned %+v, want entry %d naming Core", one, id)
	}

	if rec := doJSON(t, router, http.MethodGet, "/api/v1/activity/999999", ""); rec.Code != http.StatusNotFound {
		t.Errorf("get(missing): status = %d, want 404", rec.Code)
	}
	if rec := doJSON(t, router, http.MethodGet, "/api/v1/activity/not-a-number", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("get(malformed): status = %d, want 400", rec.Code)
	}
}

func TestActivity_HasNoWriteRoutes(t *testing.T) {
	router, _ := activityRouter(t)

	// The absence of the routes is the enforcement. An endpoint that could
	// append an entry would let a caller forge history; one that could
	// delete an entry would let a caller erase their own.
	for _, attempt := range []struct {
		method string
		target string
	}{
		{http.MethodPost, "/api/v1/activity"},
		{http.MethodPatch, "/api/v1/activity/1"},
		{http.MethodDelete, "/api/v1/activity/1"},
		{http.MethodPut, "/api/v1/activity/1"},
	} {
		rec := doJSON(t, router, attempt.method, attempt.target, `{"actor":"somebody-else"}`)
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want a refusal: the activity stream is append-only",
				attempt.method, attempt.target, rec.Code)
		}
	}
}

// failingActivityStore is a store whose every read fails.
//
// A double, and the narrow kind this project allows: the behaviour under
// test is the handler's own error mapping, and a real ent store cannot be
// made to fail a query on demand without also destroying the fixture the
// rest of the assertion needs.
type failingActivityStore struct{ err error }

func (s failingActivityStore) Record(context.Context, activity.Entry) error { return s.err }

func (s failingActivityStore) List(context.Context, activity.Query) ([]activity.Entry, error) {
	return nil, s.err
}

func (s failingActivityStore) Get(context.Context, int) (activity.Entry, error) {
	return activity.Entry{}, s.err
}

func TestActivity_AStoreFailureIs500AndSaysNothingElse(t *testing.T) {
	// A nil logger too, which is the other branch here: a handler
	// constructed without one has to log somewhere rather than panic on
	// the first failure it meets.
	h := api.NewActivityHandler(failingActivityStore{err: fmt.Errorf("the stream table is missing")}, nil)

	router, err := api.NewRouter(api.RouterConfig{
		Logger:    slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Auth:      alwaysAuthenticated,
		Admission: &fakeAdmitter{},
		HATEOAS:   allowAllGenerator(t),
		Routes: []api.Route{
			apispec.ListActivity.Route(h.ListActivity),
			apispec.GetActivityEntry.Route(h.GetActivityEntry),
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}

	for _, target := range []string{"/api/v1/activity", "/api/v1/activity/1"} {
		rec := doJSON(t, router, http.MethodGet, target, "")
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("GET %s with a failing store = %d, want 500", target, rec.Code)
		}
		// The internal error text stays in the log. A response echoing
		// "the stream table is missing" tells an unauthenticated prober
		// about the schema.
		if body := rec.Body.String(); strings.Contains(body, "stream table") {
			t.Errorf("GET %s leaked the internal error into the response: %s", target, body)
		}
	}
}
