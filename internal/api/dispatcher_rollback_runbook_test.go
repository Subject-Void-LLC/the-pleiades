// Tests for what a rollback says when the runbook its job ran cannot be
// built from the source, through the real route: a storage failure answers
// 500 and keeps its text (and the server's path) in the log, a runbook that
// does not compile answers 422 with the builder's escaped message, and a
// runbook that is gone answers 422 saying so. Each was refused before; none
// is refused that was not.
package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// rollbackOverHTTP asks the real route to roll the fixture's job back.
func (f *rollbackFixture) rollbackOverHTTP(t *testing.T) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/jobs/"+f.jobID+"/rollback", strings.NewReader(`{"mode":"execute","leave":["tasks[1]"]}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", f.jobID)
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	f.dispatcher.RollbackJob(rec, req)
	return rec.Code, rec.Body.String()
}

// touchLater moves the runbook's modification time on, so the source reads
// the file again in place of its cached compilation.
func (f *rollbackFixture) touchLater(t *testing.T) {
	t.Helper()
	path := filepath.Join(f.dir, "rb-1.yaml")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	later := info.ModTime().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

// TestRollbackJob_ARunbookTheSourceCannotReadNamesNoPath: a storage error's
// text names the server's runbook directory, so the answer is a 500 with
// "internal error" and the text stays in the log.
func TestRollbackJob_ARunbookTheSourceCannotReadNamesNoPath(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	path := filepath.Join(f.dir, "rb-1.yaml")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	f.touchLater(t)
	_, readErr := os.ReadFile(path)
	testsupport.Require(t, "posix-modes", readErr != nil, "a file's mode does not stop this user reading it (root, or a system with no POSIX modes), so the source cannot fail to read one")

	status, body := f.rollbackOverHTTP(t)
	if strings.Contains(body, f.dir) || strings.Contains(body, "rb-1.yaml") {
		t.Errorf("the answer names the server's path: %s", body)
	}
	if status != http.StatusInternalServerError || !strings.Contains(body, "internal error") {
		t.Errorf("status %d, body %s; want 500 internal error", status, body)
	}
	if f.bus.count() != 0 {
		t.Errorf("published %d launches for a rollback that was not planned", f.bus.count())
	}
}

// TestRollbackJob_ARunbookThatDoesNotCompileSaysWhy: the refusal is the
// builder's message as every other route gives it (runbook.CompileError's
// Message, escaped, masked and bounded), not the source's raw error.
func TestRollbackJob_ARunbookThatDoesNotCompileSaysWhy(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	broken := "id: rb-1\ntasks:\n  - name: step\n    noop: {}\n    notify: restart\nhandlers:\n  - name: restart\n    lock_acquisition: all_at_plan_time\n    noop: {}\n"
	if err := os.WriteFile(filepath.Join(f.dir, "rb-1.yaml"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	f.touchLater(t)

	status, body := f.rollbackOverHTTP(t)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, body %s; want 422", status, body)
	}
	for _, want := range []string{"cannot be rolled back", `does not compile`, "lock_acquisition: all_at_plan_time"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s does not say %q", body, want)
		}
	}
	if strings.Contains(body, "failed to compile") {
		t.Errorf("body %s carries the source's raw error", body)
	}
}

// TestRollbackJob_ARunbookThatIsGoneSaysSo: a missing runbook is still a 422
// refusal, naming no path.
func TestRollbackJob_ARunbookThatIsGoneSaysSo(t *testing.T) {
	f := newRollbackFixture(t, nil, nil)
	if err := os.Remove(filepath.Join(f.dir, "rb-1.yaml")); err != nil {
		t.Fatal(err)
	}
	status, body := f.rollbackOverHTTP(t)
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "the runbook it ran cannot be read: it no longer exists") {
		t.Errorf("status %d, body %s; want 422 saying the runbook no longer exists", status, body)
	}
	if strings.Contains(body, f.dir) {
		t.Errorf("the answer names the server's path: %s", body)
	}
}
