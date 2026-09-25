package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestRunbookContextFor_CarriesPayloadSecrets proves the one thing that
// makes PLAN.md Section 17's Just-in-Time delivery real on the in-process
// path: the secrets the Controller attached to the wire payload are what a
// Collection method's InjectSecrets returns, whatever device type the
// Runner rebuilt, since the context is bound to the dispatch rather than
// read back out of the device.
func TestRunbookContextFor_CarriesPayloadSecrets(t *testing.T) {
	payload := wire.DispatchPayload{
		DeviceName: "core-1",
		Secrets:    map[string]string{"username": "admin", "password": "hunter2"},
	}
	for _, device := range []inventory.InventoryItem{newWireDevice(payload), nil} {
		rc, err := runbookContextFor(payload)(context.Background(), device)
		if err != nil {
			t.Fatal(err)
		}
		secrets := rc.InjectSecrets()
		if secrets["username"] != "admin" || secrets["password"] != "hunter2" {
			t.Errorf("InjectSecrets() = %v for device %T", secrets, device)
		}
	}
}

// TestPublishJobEvent_ReturnsBusError is the regression guard for this
// phase's own "stop discarding publish errors" item: the helper must
// surface bus.Publish's failure to its caller rather than logging and
// swallowing it the way the adapter it replaced did.
func TestPublishJobEvent_ReturnsBusError(t *testing.T) {
	err := publishJobEvent(context.Background(), failingBus{}, "job-1", wire.JobEvent{Status: "ok"})
	if err == nil {
		t.Fatal("publishJobEvent() = nil, want the bus's own publish error")
	}
}

// TestRunCollectionChild_WritesResponseAndReportsSuccess exercises the
// child's whole body, not just invokeChild: request decode, method
// dispatch, and the response frame actually being written to the stream
// that becomes fd 3 in a real spawn.
func TestRunCollectionChild_WritesResponseAndReportsSuccess(t *testing.T) {
	fqcn := registerChildTestMethod(t, "runchild_ok", collection.StatusImplemented,
		func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			if err := rc.SetStat("reply", "pong"); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{Changed: true}, nil
		})

	reqBytes, err := json.Marshal(&wire.ChildRequest{FQCN: fqcn, JobID: "job-1", DeviceName: "core-1"})
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}

	var response, errOut bytes.Buffer
	code := runCollectionChild(context.Background(), bytes.NewReader(reqBytes), &response, &errOut)

	if code != 0 {
		t.Errorf("runCollectionChild() = %d, want 0 (stderr: %s)", code, errOut.String())
	}

	var resp wire.ChildResponse
	if err := json.NewDecoder(&response).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !resp.Changed {
		t.Error("response.Changed = false, want true")
	}
	if resp.Error != "" {
		t.Errorf("response.Error = %q, want empty", resp.Error)
	}
	if got := resp.Facts["reply"]; got != "pong" {
		t.Errorf("response.Facts[\"reply\"] = %v, want %q", got, "pong")
	}
}

// TestRunCollectionChild_MalformedRequestExitsNonZero proves the child
// reports a decode failure as a non-zero exit rather than proceeding with
// a zero-valued request, which would look to the parent like a method that
// ran and did nothing.
func TestRunCollectionChild_MalformedRequestExitsNonZero(t *testing.T) {
	var response, errOut bytes.Buffer
	code := runCollectionChild(context.Background(), strings.NewReader("{not json"), &response, &errOut)

	if code != 1 {
		t.Errorf("runCollectionChild() = %d, want 1 for a malformed request", code)
	}
	if response.Len() != 0 {
		t.Errorf("response frame = %q, want nothing written when the request never decoded", response.String())
	}
	if errOut.Len() == 0 {
		t.Error("stderr is empty, want the decode failure reported")
	}
}

// TestRunCollectionChild_ResponseWriteFailureExitsNonZero covers the last
// branch: the method ran, but the parent can never learn the outcome. That
// has to be a non-zero exit, because a silent 0 would tell the parent the
// task succeeded while the response frame it is waiting for never arrives.
func TestRunCollectionChild_ResponseWriteFailureExitsNonZero(t *testing.T) {
	fqcn := registerChildTestMethod(t, "runchild_writefail", collection.StatusImplemented,
		func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			return collection.Result{Changed: true}, nil
		})

	reqBytes, err := json.Marshal(&wire.ChildRequest{FQCN: fqcn})
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}

	var errOut bytes.Buffer
	code := runCollectionChild(context.Background(), bytes.NewReader(reqBytes), errWriter{}, &errOut)

	if code != 1 {
		t.Errorf("runCollectionChild() = %d, want 1 when the response cannot be written", code)
	}
	if errOut.Len() == 0 {
		t.Error("stderr is empty, want the write failure reported")
	}
}

// TestIPCCollectionExecutor_Invoke_RefusesADeviceItWasNotDispatchedFor
// covers the parent side's own guard. A nil device reaching this seam was
// a real failure the SSH mesh Release Gate surfaced (FAILURE_PATTERNS.md
// #86), and a bound executor handed another device would run the call
// against the dispatched one, since the child rebuilds its device from the
// payload. Each refusal happens before any child starts.
func TestIPCCollectionExecutor_Invoke_RefusesADeviceItWasNotDispatchedFor(t *testing.T) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}
	bound := exec.forDispatch(wire.DispatchPayload{DeviceName: "web1"})
	for name, tc := range map[string]struct {
		exec   *ipcCollectionExecutor
		device inventory.InventoryItem
		want   string
	}{
		"no device":                  {exec, nil, "handed no device"},
		"a typed nil device":         {bound, (*wireDevice)(nil), "handed no device"},
		"unbound, not address-only":  {exec, &inventorytest.Stub{StubName: "web1"}, "has no dispatch"},
		"bound, another device name": {bound, &inventorytest.Stub{StubName: "web2"}, `bound to device "web1" and was handed "web2"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := tc.exec.invoke(context.Background(), collection.Descriptor{Name: "x.y"}, tc.device, nil, collection.ModeExecute)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("invoke() error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestSecretValues_ExtractsEveryValue pins the helper redact.Text is fed
// from: a secret missing from this slice is a secret that will not be
// masked out of captured subprocess output.
func TestSecretValues_ExtractsEveryValue(t *testing.T) {
	got := secretValues(map[string]string{"username": "admin", "password": "hunter2"})
	if len(got) != 2 {
		t.Fatalf("secretValues() returned %d values, want 2", len(got))
	}

	seen := map[string]bool{}
	for _, v := range got {
		seen[v] = true
	}
	if !seen["admin"] || !seen["hunter2"] {
		t.Errorf("secretValues() = %v, want both secret values present", got)
	}
}

// TestCollectionInvokerSeamIsSatisfied is a compile-time-shaped assertion
// with a runtime home: it proves the parent-side invoke method still
// matches engine.CollectionInvoker exactly, which is what lets the adapter
// install the subprocess boundary without internal/engine knowing it
// exists.
func TestCollectionInvokerSeamIsSatisfied(t *testing.T) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}
	var seam engine.CollectionInvoker = exec.invoke
	if seam == nil {
		t.Fatal("invoke does not satisfy engine.CollectionInvoker")
	}
}

// errWriter fails every Write, standing in for a response pipe whose read
// end has already gone away (a parent that died mid-invocation).
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("response pipe closed") }
