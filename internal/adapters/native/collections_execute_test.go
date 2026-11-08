// Package native: that Execute compares the Collections a dispatch names
// with the tasks it really runs, and publishes each warning to the job's
// log as well as the Runner's (collections.go).
package native

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// registerIdentityFixtures registers a built-in of the file group (never
// run, so the group has a digest here) and two external programs' methods,
// which run in this process: netbox.make, whose runbook task authors its
// undo through acme.remove.
func registerIdentityFixtures(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	path := collection.Doc{Params: []collection.Param{{Name: "path", Type: "string"}}}
	for _, d := range []collection.Descriptor{
		{Name: "file.test_identity", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "reads"}}},
		{Name: "netbox.make", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"}, Doc: path},
			Provider: &collection.Provider{Program: "/opt/collections/netbox", Digest: "sha256:00"}},
		{Name: "acme.remove", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"}, Doc: path},
			Provider: &collection.Provider{Program: "/opt/collections/acme", Digest: "sha256:00"}},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
}

// identityRunbook makes a path through netbox.make, with an authored undo
// through acme.remove.
const identityRunbook = `id: ran
tasks:
  - name: make it
    netbox.make:
      path: /tmp/made
    rollback:
      - name: remove it
        acme.remove:
          path: /tmp/made
`

// warningsLogged returns the messages of the warnings logger's buffer holds.
func warningsLogged(t *testing.T, buf *bytes.Buffer) []string {
	t.Helper()
	var out []string
	for _, raw := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var line struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &line) == nil && line.Level == "WARN" {
			out = append(out, line.Msg)
		}
	}
	return out
}

// jobWarnings returns the messages of the task.warning events bus carried
// on job-1's log.
func jobWarnings(t *testing.T, bus *mockBus) []string {
	t.Helper()
	var out []string
	for _, evt := range bus.logEvents("job-1") {
		var je wire.JobEvent
		if err := json.Unmarshal(evt.Data, &je); err != nil {
			t.Fatal(err)
		}
		if je.Task == "task.warning" {
			out = append(out, je.EventData.Message)
		}
	}
	return out
}

// TestAdapter_Execute_PublishesEachIdentityWarning runs a dispatch through
// the real Execute naming the file group at another digest under this
// Runner's own version: the stale-catalog warning reaches the Runner's log
// and, as a task.warning event, the job's, and the run still completes. A
// dispatch naming the group at this Runner's own digest warns nowhere.
func TestAdapter_Execute_PublishesEachIdentityWarning(t *testing.T) {
	registerIdentityFixtures(t)
	own, err := collection.GroupDigest("file")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, manifests, want string
	}{
		{name: "another digest", manifests: wire.ManifestDigestPrefix + strings.Repeat("9", 64), want: "stale module catalog or a corrupt build"},
		{name: "the same digest", manifests: own},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			bus := &mockBus{}
			adapter, err := NewAdapter(bus, writeRunbook(t, "ran", identityRunbook), slog.New(slog.NewJSONHandler(&buf, nil)))
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Execute(context.Background(), wire.DispatchPayload{JobID: "job-1", RunbookID: "ran", DeviceName: "web1", DeviceHost: "10.0.0.1",
				Collections: map[string]wire.CollectionIdentity{"file": {Group: "file", Version: runnerVersion(), Manifests: tc.manifests}}})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			logged, published := warningsLogged(t, &buf), jobWarnings(t, bus)
			if tc.want == "" {
				if len(logged) != 0 || len(published) != 0 {
					t.Errorf("logged %q and published %q, want neither", logged, published)
				}
				return
			}
			if len(logged) != 1 || !strings.Contains(logged[0], tc.want) {
				t.Errorf("the Runner logged %q, want one warning saying %q", logged, tc.want)
			}
			if len(published) != 1 || !strings.HasPrefix(published[0], "WARNING: ") || !strings.Contains(published[0], tc.want) || !strings.Contains(published[0], "file") {
				t.Errorf("the job's log carried %q, want one task.warning saying %q", published, tc.want)
			}
			if done := bus.lastJobEvent(t); done.Task != "task.completed" || done.Status == "failed" {
				t.Errorf("the run ended %+v, want it completed", done)
			}
		})
	}
}

// TestAdapter_Execute_ComparesTheRollbackItRuns: a rollback dispatch is
// compared over the undo steps it runs, not the runbook being undone. The
// Controller names the netbox group; the undone task ran netbox.make from a
// program here, which a comparison over that runbook would warn about as a
// method the Controller placed in a group, and the undo runs acme.remove,
// which is in no named group. So the only warning is that this Runner holds
// no netbox group.
func TestAdapter_Execute_ComparesTheRollbackItRuns(t *testing.T) {
	registerIdentityFixtures(t)
	runbooks := writeRunbook(t, "ran", identityRunbook)
	ran, err := runbooks.GetDAG(context.Background(), "ran")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	bus := &mockBus{}
	adapter, err := NewAdapter(bus, runbooks, slog.New(slog.NewJSONHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	step := wire.RollbackStep{Node: "tasks[0]", Emitter: "netbox.make", Method: "acme.remove", Params: map[string]any{"path": "/tmp/made"}, Name: "undo tasks[0], step 1: remove it", Source: "authored"}
	_, err = adapter.Execute(context.Background(), wire.DispatchPayload{JobID: "job-1", RunbookID: "ran", DeviceName: "web1", DeviceHost: "10.0.0.1",
		Rollback:    &wire.Rollback{Of: "job-0", DAGVersion: ran.Version, Steps: []wire.RollbackStep{step}},
		Collections: map[string]wire.CollectionIdentity{"netbox": {Group: "netbox", Version: "0.9.0", Manifests: wire.ManifestDigestPrefix + strings.Repeat("9", 64)}}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	logged := warningsLogged(t, &buf)
	if len(logged) != 1 || !strings.Contains(logged[0], "does not include") {
		t.Errorf("the Runner logged %q, want only that it holds no netbox group", logged)
	}
}

// TestStepFromWire_KeepsEveryField: a rollback step decoded from the wire
// keeps every field the planner's step has a place for, the Collection
// identity and undo digest the journal recorded among them, so this
// Runner's checks read what the Controller planned.
func TestStepFromWire_KeepsEveryField(t *testing.T) {
	ws := wire.RollbackStep{Node: "tasks[2]", Index: 1, Emitter: "file.copy", Method: "file.remove", Params: map[string]any{"path": "/x"},
		Name: "undo", Source: "recorded",
		Collection: wire.CollectionIdentity{Group: "file", Version: "0.4.0", Manifests: wire.ManifestDigestPrefix + strings.Repeat("c", 64)},
		UndoDigest: wire.ManifestDigestPrefix + strings.Repeat("d", 64), SkewAccepted: true}
	s := stepFromWire(ws)
	if s.Node != ws.Node || s.Index != ws.Index || s.Emitter != ws.Emitter || s.FQCN != ws.Method || s.Params["path"] != "/x" ||
		s.Name != ws.Name || s.Source != ws.Source || s.Collection != ws.Collection || s.UndoDigest != ws.UndoDigest || !s.SkewAccepted {
		t.Errorf("stepFromWire(%+v) = %+v", ws, s)
	}
}
