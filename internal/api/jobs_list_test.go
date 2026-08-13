package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
)

// stubJobLister answers List from a fixed slice and records the arguments
// it was called with, which is the only way to prove the query parameters
// reached the store rather than being parsed and dropped.
type stubJobLister struct {
	jobs []*dispatch.Job

	gotAfter string
	gotLimit int
}

func (s *stubJobLister) Get(context.Context, string) (*dispatch.Job, []dispatch.JobTask, error) {
	return nil, nil, dispatch.ErrJobNotFound
}

func (s *stubJobLister) List(_ context.Context, after string, limit int) ([]*dispatch.Job, error) {
	s.gotAfter, s.gotLimit = after, limit
	jobs := s.jobs
	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs, nil
}

func testJobs(n int) []*dispatch.Job {
	jobs := make([]*dispatch.Job, 0, n)
	for i := range n {
		jobs = append(jobs, &dispatch.Job{
			JobID:     [...]string{"00000000-0000-7000-8000-00000000000a", "00000000-0000-7000-8000-00000000000b", "00000000-0000-7000-8000-00000000000c"}[i%3],
			RunbookID: "pb-1",
			GroupName: "routers",
			Actor:     "user@example.com",
			State:     "completed",
			CreatedAt: time.Unix(1700000000, 0).UTC(),
		})
	}
	return jobs
}

func TestJobHandler_ListReturnsAPageAndACursor(t *testing.T) {
	lister := &stubJobLister{jobs: testJobs(3)}
	router := jobsRouter(t, lister)

	rec := doJSON(t, router, http.MethodGet, "/api/v1/jobs?limit=2", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got struct {
		Jobs []struct {
			JobID     string `json:"job_id"`
			Actor     string `json:"actor"`
			CreatedAt string `json:"created_at"`
		} `json:"jobs"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Jobs) != 2 {
		t.Fatalf("returned %d jobs, want 2", len(got.Jobs))
	}
	if got.NextCursor != got.Jobs[1].JobID {
		t.Errorf("next_cursor = %q, want the last returned job id %q", got.NextCursor, got.Jobs[1].JobID)
	}
	if got.Jobs[0].Actor != "user@example.com" {
		t.Errorf("actor = %q, want it carried through", got.Jobs[0].Actor)
	}
	// RFC 3339, so a client does not have to guess the format.
	if _, err := time.Parse(time.RFC3339, got.Jobs[0].CreatedAt); err != nil {
		t.Errorf("created_at = %q, want RFC 3339: %v", got.Jobs[0].CreatedAt, err)
	}

	// One more than the page size, so a next page is observed rather than
	// inferred from a full page.
	if lister.gotLimit != 3 {
		t.Errorf("store limit = %d, want 3 (page size plus lookahead)", lister.gotLimit)
	}
}

func TestJobHandler_ListOmitsCursorOnTheFinalPage(t *testing.T) {
	router := jobsRouter(t, &stubJobLister{jobs: testJobs(2)})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/jobs?limit=2", "")
	var got struct {
		Jobs       []json.RawMessage `json:"jobs"`
		NextCursor string            `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}
	if len(got.Jobs) != 2 || got.NextCursor != "" {
		t.Errorf("jobs = %d, next_cursor = %q, want 2 and empty", len(got.Jobs), got.NextCursor)
	}
}

func TestJobHandler_ListPassesTheCursorThrough(t *testing.T) {
	lister := &stubJobLister{}
	router := jobsRouter(t, lister)

	const cursor = "00000000-0000-7000-8000-00000000000a"
	if rec := doJSON(t, router, http.MethodGet, "/api/v1/jobs?after="+cursor, ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if lister.gotAfter != cursor {
		t.Errorf("store cursor = %q, want %q", lister.gotAfter, cursor)
	}
}

// The cursor is a job id and a job id is a UUID. It reaches a storage
// query, so it gets the same guard StreamLogs applies to {id} rather than
// letting a caller choose what that query compares against.
func TestJobHandler_ListRejectsANonUUIDCursor(t *testing.T) {
	router := jobsRouter(t, &stubJobLister{})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/jobs?after=not-a-uuid", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestJobHandler_ListRejectsABadLimit(t *testing.T) {
	router := jobsRouter(t, &stubJobLister{})

	for _, target := range []string{"/api/v1/jobs?limit=0", "/api/v1/jobs?limit=-1", "/api/v1/jobs?limit=lots"} {
		if rec := doJSON(t, router, http.MethodGet, target, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400", target, rec.Code)
		}
	}
}

func TestJobHandler_ListCapsAnOversizedLimit(t *testing.T) {
	lister := &stubJobLister{}
	router := jobsRouter(t, lister)

	if rec := doJSON(t, router, http.MethodGet, "/api/v1/jobs?limit=100000", ""); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// The cap is the server's: 200 plus the one-row lookahead.
	if lister.gotLimit != 201 {
		t.Errorf("store limit = %d, want the 200 cap enforced", lister.gotLimit)
	}
}

func TestJobHandler_ListReportsAStoreFailure(t *testing.T) {
	router := jobsRouter(t, erroringJobRepository{})

	rec := doJSON(t, router, http.MethodGet, "/api/v1/jobs", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); jsonContains(body, "deliberate store failure") {
		t.Errorf("body = %s, want the store's own error withheld", body)
	}
}

func jsonContains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
