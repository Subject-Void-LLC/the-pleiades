// Tests for validateDispatch through the adapter's real Execute: a
// dispatched runbook that fails validation is refused before any task
// runs, and the job log says why.
package native

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// registerValidationFixtures registers the methods the refusal cases
// call: one implemented with a declared parameter and no check support,
// and one only declared.
func registerValidationFixtures(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	invoke := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{Changed: true}, nil
	}
	for _, d := range []collection.Descriptor{
		{
			Name: "valfixture.write",
			Manifest: collection.Manifest{
				Status:        collection.StatusImplemented,
				Reversibility: collection.Reversibility{Notes: "a test fixture"},
				NoCheckReason: "a test fixture that cannot say what it would change",
				Doc:           collection.Doc{Summary: "Writes.", Params: []collection.Param{{Name: "path", Type: "string"}}},
			},
			Invoke: invoke,
		},
		{
			Name:     "valfixture.stub",
			Manifest: collection.Manifest{Status: collection.StatusDeclared, Doc: collection.Doc{Summary: "Not yet."}},
		},
	} {
		if err := collection.Register(d); err != nil {
			t.Fatal(err)
		}
	}
}

// TestExecute_RefusesARunbookThatFailsValidation covers each kind of
// finding the Runner used to meet only when the task was reached: the
// dispatch is refused with the finding, the job log holds exactly the
// started event and the refusal, and no parameter value appears in
// either.
func TestExecute_RefusesARunbookThatFailsValidation(t *testing.T) {
	registerValidationFixtures(t)
	const secret = "s3cr3t-param-value"
	for _, tc := range []struct {
		name, tasks, want string
	}{
		{"unregistered method", "  - name: first\n    fqcn: noop\n  - name: second\n    fqcn: totally.fake.name\n", `"totally.fake.name", which is not a registered collection name`},
		{"declared-only method", "  - name: first\n    fqcn: noop\n  - name: second\n    fqcn: valfixture.stub\n", "declared but not yet implemented"},
		{"undeclared parameter", "  - name: write\n    fqcn: valfixture.write\n    params:\n      path: /tmp/x\n      pth: " + secret + "\n", `passes parameter "pth"`},
		{"check_mode on a method that cannot check", "  - name: write\n    fqcn: valfixture.write\n    check_mode: true\n    params:\n      path: " + secret + "\n", "valfixture.write"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := &mockBus{}
			adapter, err := NewAdapter(bus, writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n"+tc.tasks), nil)
			if err != nil {
				t.Fatalf("NewAdapter: %v", err)
			}
			payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
			_, err = adapter.Execute(context.Background(), payload)
			if err == nil || !strings.Contains(err.Error(), "fails validation, so no task ran") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Execute() error = %v, want a validation refusal naming %q", err, tc.want)
			}

			events := bus.logEvents("job-1")
			if len(events) != 2 {
				t.Fatalf("job log has %d events, want exactly started and the refusal", len(events))
			}
			var first, last wire.JobEvent
			if err := json.Unmarshal(events[0].Data, &first); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(events[1].Data, &last); err != nil {
				t.Fatal(err)
			}
			if first.Status != "started" || last.Status != "failed" || !strings.Contains(last.EventData.Message, tc.want) {
				t.Errorf("job log = %q then %q (%q), want started then failed naming %q", first.Status, last.Status, last.EventData.Message, tc.want)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(last.EventData.Message, secret) {
				t.Errorf("a parameter value reached the refusal: %v", err)
			}
		})
	}
}

// TestExecute_ValidRunbookStillRuns is the control: a runbook with no
// finding is not refused, so the check above is not refusing everything.
func TestExecute_ValidRunbookStillRuns(t *testing.T) {
	bus := &mockBus{}
	adapter, err := NewAdapter(bus, writeRunbook(t, "pb-1", "id: pb-1\ntasks:\n  - name: step\n    fqcn: noop\n    params:\n      changed: true\n"), nil)
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	payload := wire.DispatchPayload{JobID: "job-1", RunbookID: "pb-1", DeviceName: "router1", DeviceHost: "10.0.0.1"}
	if _, err := adapter.Execute(context.Background(), payload); err != nil {
		t.Fatalf("Execute() = %v, want a clean run", err)
	}
	if final := bus.lastJobEvent(t); final.Status != "changed" {
		t.Errorf("final status = %q, want changed", final.Status)
	}
}
