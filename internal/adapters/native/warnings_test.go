// Tests for what Execute adds around a rebuilt device: each warning a task
// recorded reaches the job log once and masked, and a dispatch whose device
// cannot be rebuilt is refused before any task runs.
package native

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestRunWarnings: both shapes a stat can arrive in (a []string from this
// process, a []any after a JSON round trip from a child) are read, each
// warning appears once in first-seen order, and a secret inside one is
// masked.
func TestRunWarnings(t *testing.T) {
	result := engine.RunResult{Nodes: []engine.NodeResult{
		{Stats: map[string]any{sdk.StatWarnings: []string{"device d1 allows TLS 1.0", "sent hunter2 over http"}}},
		{Stats: map[string]any{sdk.StatWarnings: []any{"device d1 allows TLS 1.0", "legacy ciphers"}}},
		{Stats: map[string]any{sdk.StatWarnings: "not a list"}},
		{},
	}}
	got := runWarnings(result, []string{"hunter2"})
	if len(got) != 3 || got[0] != "device d1 allows TLS 1.0" || got[2] != "legacy ciphers" {
		t.Fatalf("runWarnings = %q, want the three distinct warnings in first-seen order", got)
	}
	if strings.Contains(got[1], "hunter2") || !strings.HasSuffix(got[1], " over http") {
		t.Errorf("runWarnings[1] = %q, want the secret masked and the rest kept", got[1])
	}
}

// warnFixture registers a method that records warning, run in this process
// the way an external Collection's method is.
func warnFixture(t *testing.T, warning string) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	record := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
		return collection.Result{}, sdk.RecordWarnings(rc, []string{warning})
	}
	if err := collection.Register(collection.Descriptor{
		Name: "nativewarn.weak",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"},
			NoCheckReason: "a test fixture"},
		Invoke:   record,
		Provider: &collection.Provider{Program: "/opt/collections/warn", Digest: "sha256:00"},
	}); err != nil {
		t.Fatal(err)
	}
}

// jobTasks returns the Task of each job event published for jobID, and the
// event carrying task, if any.
func jobTasks(t *testing.T, bus *mockBus, jobID, task string) ([]string, []wire.JobEvent) {
	t.Helper()
	var tasks []string
	var matched []wire.JobEvent
	for _, evt := range bus.logEvents(jobID) {
		var je wire.JobEvent
		if err := json.Unmarshal(evt.Data, &je); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, je.Task)
		if je.Task == task {
			matched = append(matched, je)
		}
	}
	return tasks, matched
}

// TestExecute_PublishesEachWarningOnce: two tasks recording the same
// warning put one task.warning event in the job log, before the completion.
func TestExecute_PublishesEachWarningOnce(t *testing.T) {
	warnFixture(t, "device router1 allows TLS 1.0 (RFC 8996)")
	bus := &mockBus{}
	adapter, err := NewAdapter(bus, writeRunbook(t, "w", "id: w\ntasks:\n  - name: one\n    fqcn: nativewarn.weak\n  - name: two\n    fqcn: nativewarn.weak\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(context.Background(), wire.DispatchPayload{JobID: "job-w", RunbookID: "w", DeviceName: "router1", DeviceHost: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	tasks, warnings := jobTasks(t, bus, "job-w", "task.warning")
	if len(warnings) != 1 || warnings[0].EventData.Message != "WARNING: device router1 allows TLS 1.0 (RFC 8996)" || warnings[0].Status != "ok" {
		t.Fatalf("warning events %+v", warnings)
	}
	if !reflect.DeepEqual(tasks[len(tasks)-2:], []string{"task.warning", "task.completed"}) {
		t.Errorf("job log order %q, want the warning just before the completion", tasks)
	}
}

// TestExecute_SaysAShortCommunityIsMaskedByShapeOnly: a dispatch whose
// credential carries a community too short to mask by value puts that
// warning in its job log once, just after it starts and before any task
// runs, and a dispatch refused before any task runs says it too; one whose
// community is long enough says nothing of it, and none prints the
// community. The warning was published after the run before, so a refused
// or aborted run never gave it.
func TestExecute_SaysAShortCommunityIsMaskedByShapeOnly(t *testing.T) {
	warnFixture(t, "a task's own warning")
	short := "WARNING: " + credential.ShortCommunityWarning("old-switch")
	for _, tc := range []struct {
		name, community, runbook string
		refused                  bool
		want                     []string
	}{
		{"short", "public", "id: s\ntasks:\n  - name: one\n    fqcn: nativewarn.weak\n", false, []string{short, "WARNING: a task's own warning"}},
		{"short, refused", "public", "id: s\ntasks:\n  - name: one\n    fqcn: nativewarn.unregistered\n", true, []string{short}},
		{"long", "c0mmunity-s3cret", "id: s\ntasks:\n  - name: one\n    fqcn: nativewarn.weak\n", false, []string{"WARNING: a task's own warning"}},
	} {
		bus := &mockBus{}
		adapter, err := NewAdapter(bus, writeRunbook(t, "s", tc.runbook), nil)
		if err != nil {
			t.Fatal(err)
		}
		payload := wire.DispatchPayload{JobID: "job-s", RunbookID: "s", DeviceName: "old-switch", DeviceHost: "10.0.0.2",
			Secrets: map[string]string{wire.SecretSNMPCommunity: tc.community}}
		if _, err := adapter.Execute(context.Background(), payload); (err != nil) != tc.refused {
			t.Fatalf("%s: Execute error %v, want refused %v", tc.name, err, tc.refused)
		}
		tasks, warnings := jobTasks(t, bus, "job-s", "task.warning")
		var messages []string
		for _, w := range warnings {
			messages = append(messages, w.EventData.Message)
		}
		if !reflect.DeepEqual(messages, tc.want) {
			t.Errorf("%s: the job log warned %q, want %q", tc.name, messages, tc.want)
		}
		if tc.want[0] == short && (len(tasks) < 2 || tasks[1] != "task.warning") {
			t.Errorf("%s: job log order %q, want the warning just after the start, before any task", tc.name, tasks)
		}
		for _, m := range messages {
			if strings.Contains(m, tc.community) {
				t.Errorf("%s: a warning printed the community: %q", tc.name, m)
			}
		}
	}
}

// TestExecute_RefusesADeviceThatWillNotRebuild: a dispatch of a known type
// whose record does not build is refused, the job log saying why, and no
// task runs; a refusal that cannot be published still refuses.
func TestExecute_RefusesADeviceThatWillNotRebuild(t *testing.T) {
	warnFixture(t, "never recorded")
	payload := wire.DispatchPayload{
		JobID: "job-r", RunbookID: "r", DeviceName: "api1", DeviceHost: "api.example.com",
		DeviceType: generic.TypeHTTP, DeviceProperties: map[string]any{generic.HTTPAuthProperty: "bearer"},
	}
	runbooks := writeRunbook(t, "r", "id: r\ntasks:\n  - name: one\n    fqcn: nativewarn.weak\n")

	bus := &mockBus{}
	adapter, err := NewAdapter(bus, runbooks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "refusing dispatch") {
		t.Fatalf("Execute() = %v, want the dispatch refused", err)
	}
	_, done := jobTasks(t, bus, "job-r", "task.completed")
	if len(done) != 1 || done[0].Status != "failed" || !strings.Contains(done[0].EventData.Message, "rebuilding device") {
		t.Errorf("completion %+v, want a failure naming the rebuild", done)
	}
	if _, warnings := jobTasks(t, bus, "job-r", "task.warning"); len(warnings) != 0 {
		t.Error("a task ran for a device that could not be rebuilt")
	}

	failing := &refusalFailingBus{}
	adapter, err = NewAdapter(failing, runbooks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "failed to publish the refusal") {
		t.Errorf("Execute() = %v, want the unpublished refusal named", err)
	}
}
