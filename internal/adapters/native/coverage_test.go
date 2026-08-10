package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestNewDeviceRunbookContext_CarriesPayloadSecrets proves the one thing
// that makes PLAN.md Section 17's Just-in-Time delivery real on the
// in-process path: the secrets the Controller attached to the wire payload
// are what a Collection method's InjectSecrets actually returns, rather
// than the empty set internal/engine.NewDeviceRunbookContext returns at
// Walk tier (which ignores its device argument entirely).
func TestNewDeviceRunbookContext_CarriesPayloadSecrets(t *testing.T) {
	device := newWireDevice(wire.DispatchPayload{
		DeviceName: "core-1",
		Secrets:    map[string]string{"username": "admin", "password": "hunter2"},
	})

	rc := newDeviceRunbookContext(device)
	secrets := rc.InjectSecrets()

	if got := secrets["username"]; got != "admin" {
		t.Errorf("InjectSecrets()[\"username\"] = %q, want %q", got, "admin")
	}
	if got := secrets["password"]; got != "hunter2" {
		t.Errorf("InjectSecrets()[\"password\"] = %q, want %q", got, "hunter2")
	}
}

// TestNewDeviceRunbookContext_NonWireDeviceGetsNoSecrets covers the
// defensive type-assertion fallback. It is unreachable through this
// package's own composition (singleDeviceResolver only ever yields a
// *wireDevice), so the assertion that matters is the security-relevant
// one: an unexpected device type must yield NO secrets rather than
// silently carrying another device's.
func TestNewDeviceRunbookContext_NonWireDeviceGetsNoSecrets(t *testing.T) {
	rc := newDeviceRunbookContext(nil)
	if got := rc.InjectSecrets(); len(got) != 0 {
		t.Errorf("InjectSecrets() = %v, want empty for a non-wireDevice", got)
	}
}

// TestWireDevice_ShowInfoMatchesProperties pins ShowInfo to Properties.
// The two are separate methods on inventory.InventoryItem and a future
// edit could easily make them disagree, which would show up as a device
// reporting different metadata depending on which accessor a caller
// happened to reach for.
func TestWireDevice_ShowInfoMatchesProperties(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{DeviceHost: "10.0.0.4", SSHPort: 2022})

	shown, ok := d.ShowInfo().String("host")
	if !ok || shown != "10.0.0.4" {
		t.Errorf("ShowInfo()[\"host\"] = %q, %v, want %q, true", shown, ok, "10.0.0.4")
	}
	props, _ := d.Properties().String("host")
	if shown != props {
		t.Errorf("ShowInfo() and Properties() disagree on host: %q vs %q", shown, props)
	}
}

// TestWireDevice_SourceIsZero documents that a Runner-side device carries
// no sync-plugin provenance: the wire payload has no field for it, and
// inventing one here would fabricate an authority that never synced this
// device.
func TestWireDevice_SourceIsZero(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{DeviceName: "core-1"})
	if src := d.Source(); src.Plugin != "" || !src.SyncedAt.IsZero() {
		t.Errorf("Source() = %+v, want the zero SourceAuthority", src)
	}
}

// TestWireDevice_MutatorsRefuseRatherThanSilentlyDrop proves AddInfo and
// RemoveInfo report a real error. The Runner holds no inventory backend,
// so a silent no-op here would let a Collection method believe it had
// persisted a property change that nothing anywhere recorded.
func TestWireDevice_MutatorsRefuseRatherThanSilentlyDrop(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{DeviceName: "core-1"})

	if err := d.AddInfo("k", "v", true); err == nil {
		t.Error("AddInfo() = nil, want an error: a silent no-op would look like a successful write")
	}
	if err := d.RemoveInfo("k"); err == nil {
		t.Error("RemoveInfo() = nil, want an error: a silent no-op would look like a successful delete")
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

// TestIPCCollectionExecutor_Invoke_RejectsNonWireDevice covers the parent
// side's own type guard. This is the exact failure the SSH mesh Release
// Gate surfaced once for real (FAILURE_PATTERNS.md #86, where a nil device
// reached this seam), so it is worth an ordinary unit test that fails fast
// rather than only a container test that takes ten seconds to say so.
func TestIPCCollectionExecutor_Invoke_RejectsNonWireDevice(t *testing.T) {
	exec, err := newIPCCollectionExecutor(nil)
	if err != nil {
		t.Fatalf("newIPCCollectionExecutor: %v", err)
	}

	_, _, err = exec.invoke(context.Background(), collection.Descriptor{Name: "x.y"}, nil, nil)
	if err == nil {
		t.Fatal("invoke() = nil error, want a refusal for a non-*wireDevice device")
	}
	if !strings.Contains(err.Error(), "wireDevice") {
		t.Errorf("invoke() error = %q, want it to name the expected type", err)
	}
}

// TestSecretValues_ExtractsEveryValue pins the helper credential.Mask is
// fed from: a secret missing from this slice is a secret that will not be
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

// TestWireDevice_CapabilitiesCopyIsDefensive proves a caller cannot reach
// back through the returned slice and change what the device reports it
// can do, which would let one task's mutation silently re-gate a later
// task's transport selection.
func TestWireDevice_CapabilitiesCopyIsDefensive(t *testing.T) {
	d := newWireDevice(wire.DispatchPayload{Capabilities: []capability.Name{capability.NameSSHTransport}})

	caps := d.Capabilities()
	if len(caps) != 1 {
		t.Fatalf("Capabilities() returned %d entries, want 1", len(caps))
	}
	caps[0] = capability.NameCiscoIOS

	if !d.HasCapability(capability.NameSSHTransport) {
		t.Error("mutating the slice returned by Capabilities() changed the device's own capabilities")
	}
}

// errReader fails every Read, standing in for a stdin that dies mid-frame.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("stdin exploded") }

// errWriter fails every Write, standing in for a response pipe whose read
// end has already gone away (a parent that died mid-invocation).
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("response pipe closed") }

// TestReadChildRequest_ReadFailureIsReported covers the I/O-failure branch
// distinctly from the malformed-JSON branch: both must be refusals, and
// neither may yield a usable zero-valued request.
func TestReadChildRequest_ReadFailureIsReported(t *testing.T) {
	if _, err := readChildRequest(errReader{}); err == nil {
		t.Fatal("readChildRequest() = nil error, want the underlying read failure")
	}
}
