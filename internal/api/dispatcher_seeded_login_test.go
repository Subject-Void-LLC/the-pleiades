// Tests that a launch of a template whose runbook would seed a login is
// refused before the job exists, so nothing is published (decided
// 2026-11-11).
package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestLaunchTemplate_RefusesASeededLoginBeforeTheJobExists: a template
// naming a runbook whose task seeds a device's login is refused with
// ErrSeededLogin naming the task and the device; no job is created and no
// job.requested is published. The same template naming a runbook that seeds
// nothing launches. The method is known only from the installed catalog, as
// on a shipped Controller.
func TestLaunchTemplate_RefusesASeededLoginBeforeTheJobExists(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog([]byte(`{"seedtest.clone":{"status":"implemented","seedsLogin":"login","doc":{"params":[{"name":"login","type":"string","required":true}]}}}`)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seeds.yaml"), []byte("id: seeds\ntasks:\n  - name: clone\n    seedtest.clone:\n      login: lab-web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRunbook(t, dir, "pb-1", noopRunbookFQCN)
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		definition string
		refused    bool
	}{{"seeds", true}, {"pb-1", false}} {
		jobs := newTestJobStore(t)
		bus := newCapturingBus()
		tmpl := promptableTemplate()
		tmpl.Definition = tc.definition
		dispatcher := api.NewDispatcher(src, jobs, bus, api.WithTemplates(stubTemplates{tmpl: tmpl}))
		_, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
		if got := errors.Is(err, dispatch.ErrSeededLogin); got != tc.refused {
			t.Fatalf("%s: LaunchTemplate = %v, refused %v, want %v", tc.definition, err, got, tc.refused)
		}
		_, published := bus.firstOnTopic(topology.JobRequestedSubject())
		listed, listErr := jobs.List(context.Background(), "", 10)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if tc.refused {
			if !strings.Contains(err.Error(), `device "lab-web"`) {
				t.Errorf("the refusal %q does not name the device", err)
			}
			if published || len(listed) != 0 {
				t.Errorf("a refused launch published %v and left %d jobs, want neither", published, len(listed))
			}
		} else if !published || len(listed) != 1 {
			t.Errorf("control: published %v, %d jobs; want one of each", published, len(listed))
		}
	}
}

// TestLaunchFromTemplate_ASeededLoginIs422: over HTTP the refusal is a 422
// whose message names the device, never a 500.
func TestLaunchFromTemplate_ASeededLoginIs422(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog([]byte(`{"seedtest.clone":{"status":"implemented","seedsLogin":"login","doc":{"params":[{"name":"login","type":"string","required":true}]}}}`)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seeds.yaml"), []byte("id: seeds\ntasks:\n  - name: clone\n    seedtest.clone:\n      login: lab-web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := promptableTemplate()
	tmpl.Definition = "seeds"
	dispatcher := api.NewDispatcher(src, newTestJobStore(t), newCapturingBus(), api.WithTemplates(stubTemplates{tmpl: tmpl}))
	req := httptest.NewRequest(http.MethodPost, "/templates/12/launch", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "12")
	req = req.WithContext(context.WithValue(contextWithIdentity(req, dispatchTestIdentity), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	dispatcher.LaunchFromTemplate(rec, req)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "lab-web") {
		t.Errorf("status %d, body %s; want 422 naming the device", rec.Code, rec.Body.String())
	}
}
