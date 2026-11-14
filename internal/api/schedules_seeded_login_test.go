// Tests that a schedule naming a template whose runbook would seed a login is
// refused at the write, through the real schedule route, rather than at every
// firing (decided 2026-11-11; every refusal a launchable type makes is made at
// the write through Preflight).
package api_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// seedingRunbookSource returns a runbook source holding "seeds", whose task
// seeds lab-web's login through seedtest.clone, a method the installed
// catalog alone knows (as on a shipped Controller), and "plain", which seeds
// nothing.
func seedingRunbookSource(t *testing.T) runbook.Source {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog([]byte(`{"seedtest.clone":{"status":"implemented","seedsLogin":"login","doc":{"params":[{"name":"login","type":"string","required":true}]}}}`)); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seeds.yaml"), []byte("id: seeds\ntasks:\n  - name: clone\n    seedtest.clone:\n      login: lab-web\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeTestRunbook(t, dir, "plain", noopRunbookFQCN)
	src, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestSchedules_ASeededLoginIsRefusedAtTheWrite: a schedule of a template
// whose runbook seeds a device's login is refused when written, naming the
// task, the method and the device as the launch's 422 does, and nothing is
// stored. The same schedule of a template whose runbook seeds nothing saves.
func TestSchedules_ASeededLoginIsRefusedAtTheWrite(t *testing.T) {
	who := operator(auth.ScopeScheduleWrite, auth.ScopeRunbookExecute, auth.ScopeTemplateRead)
	src := seedingRunbookSource(t)

	f := newLaunchableScheduleFixtureFor(t, who, src, "seeds")
	status, body := f.post(t, scheduleBody("nightly", f.templateLaunchable, ""))
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for a schedule of a seeding runbook; body %s", status, body)
	}
	for _, want := range []string{`task \"tasks[0]\" runs seedtest.clone`, `device \"lab-web\"`, "pleiades CLI", "unified_job_template"} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not say %s: %s", want, body)
		}
	}
	if n := f.client.Schedule.Query().CountX(context.Background()); n != 0 {
		t.Errorf("%d schedules were written despite the refusal", n)
	}

	plain := newLaunchableScheduleFixtureFor(t, who, src, "plain")
	if status, body := plain.post(t, scheduleBody("nightly", plain.templateLaunchable, "")); status != http.StatusCreated {
		t.Errorf("status %d for a schedule of a runbook seeding nothing, want 201; body %s", status, body)
	}
}
