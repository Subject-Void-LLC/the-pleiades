// Tests that fan-out refuses a job whose runbook names a file in the project
// before it publishes a single dispatch, by the rule a Runner's validation
// holds it to (collection.ProjectFiles), from the module catalog a Controller
// ships.
package dispatch_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api/wellknown"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// TestWorker_RefusesAProjectFileBeforeAnyDispatch launches, against a device
// serving NETCONF, a runbook calling net.yang.schema with its dest left to the
// default, one giving file.copy a src and file.template a template file, one
// calling net.yang.schema only under a condition, and one reading the device's
// facts. The first two fail at fan-out, naming each task and parameter, with
// no dispatch published; the conditional one and the facts read are
// dispatched, the condition being the Runner's to judge.
func TestWorker_RefusesAProjectFileBeforeAnyDispatch(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog(wellknown.ModuleCatalog); err != nil {
		t.Fatal(err)
	}
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{&inventorytest.Stub{StubID: "dev-nc", StubName: "nc", StubState: pkginventory.StateActive,
		Caps:  []capability.Name{capability.NameNetconf, capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		Props: map[string]pkginventory.PropertyValue{"host": "10.0.0.1"}}}}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"schema.yaml":      "id: schema\ntasks:\n  - name: read the schema\n    net.yang.schema:\n      modules: [ietf-interfaces]\n",
		"files.yaml":       "id: files\ntasks:\n  - name: copy\n    file.copy:\n      src: configs/motd\n      dest: /etc/motd\n  - file.template:\n      src: templates/motd.j2\n      dest: /etc/motd2\n",
		"conditional.yaml": "id: conditional\ntasks:\n  - name: read the schema when asked\n    net.yang.schema:\n      modules: [ietf-interfaces]\n    when: \"false\"\n",
		"facts.yaml":       "id: facts\ntasks:\n  - name: read the facts\n    net.netconf.facts: {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runbooks, err := runbook.NewDirSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string][]string{
		"schema": {`task tasks[0] (name "read the schema") (net.yang.schema) leaves dest to its default, yang/<device>, a file in the project`},
		"files": {`task tasks[0] (name "copy") (file.copy) gives src, a file in the project`,
			`task tasks[1] (file.template) gives src, a file in the project`, "run it from the command line"},
		"conditional": nil,
		"facts":       nil,
	} {
		ctx := t.Context()
		store := newTestJobStore(t)
		bus := newCapturingBus()
		evt := requestJob(t, ctx, store, id, "routers")
		if err := dispatch.NewWorker(store, repo, runbooks, bus, nil).HandleJobRequested(evt); err != nil {
			t.Fatal(err)
		}
		job, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
		if err != nil {
			t.Fatal(err)
		}
		if want == nil {
			if job.State == "failed" || bus.count() != 1 {
				t.Errorf("%s: job %q (%q), %d dispatches; want it dispatched to the device", id, job.State, job.FailureReason, bus.count())
			}
			continue
		}
		if job.State != "failed" || len(tasks) != 0 || bus.count() != 0 {
			t.Errorf("%s: job %q with %d devices and %d dispatches, want failed before any", id, job.State, len(tasks), bus.count())
		}
		for _, w := range want {
			if !strings.Contains(job.FailureReason, w) {
				t.Errorf("%s: failure reason %q, want it to contain %q", id, job.FailureReason, w)
			}
		}
	}
}

// TestRefuseProjectFiles_LeavesWhatItCannotJudge: no runbook, a method no
// catalog knows (an external Collection's, which only a Runner loads) and a
// call naming no project file are each passed; a refusal wraps
// ErrProjectFile.
func TestRefuseProjectFiles_LeavesWhatItCannotJudge(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	t.Cleanup(collection.CatalogForTest())
	if err := collection.InstallCatalog(wellknown.ModuleCatalog); err != nil {
		t.Fatal(err)
	}
	if err := dispatch.RefuseProjectFiles(nil); err != nil {
		t.Errorf("no runbook: %v", err)
	}
	rb := &runbook.Runbook{ID: "x", Calls: map[string][]runbook.Call{
		"acme.external": {{Node: "tasks[0]", Params: map[string]any{"src": "configs/x"}}},
		"file.copy":     {{Node: "tasks[1]", Params: map[string]any{"content": "hello", "dest": "/etc/motd"}}},
	}}
	if err := dispatch.RefuseProjectFiles(rb); err != nil {
		t.Errorf("an unknown method and a copy from content: %v", err)
	}
	rb.Calls["file.copy"] = append(rb.Calls["file.copy"], runbook.Call{Node: "tasks[2]", Params: map[string]any{"src": "configs/y", "dest": "/etc/y"}})
	if err := dispatch.RefuseProjectFiles(rb); !errors.Is(err, dispatch.ErrProjectFile) || !strings.Contains(err.Error(), "task tasks[2] (file.copy) gives src") {
		t.Errorf("a copy from a project file: %v", err)
	}
}
