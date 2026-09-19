// Tests for the child side of the per-task process boundary: InvokeRequest,
// the one-frame codec (ReadChildRequest, WriteChildResponse), and
// ServeChild, which is the whole body of a child process.
//
// Most of these moved here from internal/adapters/native, where the same
// code was invokeChild, readChildRequest, writeChildResponse and
// runCollectionChild. The lookup is now a parameter. The ported tests pass
// collection.Lookup over a real registration, which is exactly what the
// Runner's own child passes, so they still exercise the process-wide
// registry a real child reads; Serve's restricted lookup is covered in
// serve_test.go.
package external_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestInvokeRequest_Success(t *testing.T) {
	var gotSecrets map[string]string
	var gotDevice inventory.InventoryItem
	name := registerTestMethod(t, "success", collection.StatusImplemented,
		func(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			gotSecrets = rc.InjectSecrets()
			gotDevice = device
			if err := rc.SetStat("echoed", params["message"]); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{Changed: true}, nil
		})

	req := wire.ChildRequest{
		FQCN:         name,
		Params:       map[string]any{"message": "hello"},
		JobID:        "job-1",
		DeviceID:     "dev-1",
		DeviceName:   "router1",
		DeviceHost:   "10.0.0.1",
		SSHPort:      2222,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      map[string]string{"username": "admin"},
	}

	resp := external.InvokeRequest(context.Background(), collection.Lookup, req)
	if resp.Error != "" {
		t.Fatalf("InvokeRequest() error = %q, want empty", resp.Error)
	}
	if !resp.Changed {
		t.Error("Changed = false, want true")
	}
	if resp.Facts["echoed"] != "hello" {
		t.Errorf("Facts[echoed] = %v, want %q", resp.Facts["echoed"], "hello")
	}
	if gotSecrets["username"] != "admin" {
		t.Errorf("method received secrets %v, want username=admin", gotSecrets)
	}

	// Every device field the request carries must reach the method's
	// device, since that is all a child has to act on.
	if gotDevice == nil {
		t.Fatal("method received a nil device")
	}
	if gotDevice.Name() != "router1" || string(gotDevice.ID()) != "dev-1" {
		t.Errorf("method received device %q (%q), want router1 (dev-1)", gotDevice.Name(), gotDevice.ID())
	}
	if !gotDevice.HasCapability(capability.NameSSHTransport) {
		t.Error("method's device does not report the capability the request carried")
	}
	ssh, ok := gotDevice.(capability.SSHTransportCapable)
	if !ok || ssh.SSHHost() != "10.0.0.1" || ssh.SSHPort() != 2222 {
		t.Errorf("method's device is not reachable over SSH at 10.0.0.1:2222 (ok=%v)", ok)
	}
	if dev, ok := gotDevice.(*external.Device); !ok || dev.Payload().JobID != "job-1" {
		t.Errorf("method's device is %T, want an *external.Device carrying job-1", gotDevice)
	}
}

func TestInvokeRequest_UnregisteredFQCN(t *testing.T) {
	resp := external.InvokeRequest(context.Background(), collection.Lookup, wire.ChildRequest{FQCN: "does.not.exist"})
	if resp.Error == "" {
		t.Fatal("expected a non-empty Error for an unregistered fqcn")
	}
	if !strings.Contains(resp.Error, `"does.not.exist" is not registered`) {
		t.Errorf("Error = %q, want it to name the fqcn as not registered", resp.Error)
	}
}

func TestInvokeRequest_DeclaredNotImplemented(t *testing.T) {
	name := registerTestMethod(t, "declared", collection.StatusDeclared, nil)
	resp := external.InvokeRequest(context.Background(), collection.Lookup, wire.ChildRequest{FQCN: name})
	if resp.Error == "" {
		t.Fatal("expected a non-empty Error for a declared-but-unimplemented method")
	}
	if !strings.Contains(resp.Error, "declared but not implemented") {
		t.Errorf("Error = %q, want it to say the method is declared but not implemented", resp.Error)
	}
}

// TestInvokeRequest_ImplementedWithoutInvokeIsRefused covers the guard
// collection.Register makes unreachable through the registry, and that a
// hand-built lookup (the reason LookupFunc is a parameter at all) can
// still reach: a descriptor claiming StatusImplemented with no Invoke.
// It must be refused as a response, never called as a nil function.
func TestInvokeRequest_ImplementedWithoutInvokeIsRefused(t *testing.T) {
	lookup := tableLookup(collection.Descriptor{
		Name:     "externaltest.hollow",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: fixtureReversibility},
	})

	resp := external.InvokeRequest(context.Background(), lookup, wire.ChildRequest{FQCN: "externaltest.hollow"})
	if !strings.Contains(resp.Error, "declared but not implemented") {
		t.Errorf("Error = %q, want a refusal naming the method as not implemented", resp.Error)
	}
}

func TestInvokeRequest_MethodErrorReported(t *testing.T) {
	sentinel := errors.New("device unreachable")
	name := registerTestMethod(t, "failing", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, sentinel
		})

	resp := external.InvokeRequest(context.Background(), collection.Lookup, wire.ChildRequest{FQCN: name})
	if resp.Error != sentinel.Error() {
		t.Errorf("Error = %q, want %q", resp.Error, sentinel.Error())
	}
	if resp.Changed || len(resp.Facts) != 0 {
		t.Errorf("a failed method's response = %+v, want no Changed and no Facts", resp)
	}
}

// TestInvokeRequest_ZeroesSecretsOnEveryPath proves the deferred Zero
// actually runs, on success, on a method's own failure, and in check mode.
// The method keeps the context it was handed and the test reads the
// secrets back through it after InvokeRequest returns: an empty answer is
// Zero having run. A method that retained a string copy is out of reach
// by design (RunbookContext's own doc comment says why), which is why this
// reads through the context rather than through a copy.
func TestInvokeRequest_ZeroesSecretsOnEveryPath(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		failure error
	}{
		{name: "execute succeeds", mode: ""},
		{name: "execute fails", mode: "execute", failure: errors.New("device unreachable")},
		{name: "check succeeds", mode: "check"},
		{name: "check fails", mode: "check", failure: errors.New("device unreachable")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var kept sdk.RunbookContext
			var seenDuringCall string
			method := func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
				kept = rc
				seenDuringCall = rc.InjectSecrets()["password"]
				return collection.Result{}, tc.failure
			}
			lookup := tableLookup(collection.Descriptor{
				Name: "externaltest.zeroing",
				Manifest: collection.Manifest{
					Status:        collection.StatusImplemented,
					Reversibility: fixtureReversibility,
					SupportsCheck: true,
				},
				Invoke: method,
				Check:  method,
			})

			external.InvokeRequest(context.Background(), lookup, wire.ChildRequest{
				FQCN:    "externaltest.zeroing",
				Mode:    tc.mode,
				Secrets: map[string]string{"password": "hunter2"},
			})

			if seenDuringCall != "hunter2" {
				t.Fatalf("method saw password %q during its call, want %q", seenDuringCall, "hunter2")
			}
			if kept == nil {
				t.Fatal("the method never ran")
			}
			if got := kept.InjectSecrets(); len(got) != 0 {
				t.Errorf("secrets still readable through the method's context after InvokeRequest returned: %v", got)
			}
		})
	}
}

// TestReadChildRequest_And_WriteChildResponse_RoundTrip round-trips both
// messages through the codec each side of the boundary actually uses:
// a request as the parent marshals it into ReadChildRequest, and a
// response out of WriteChildResponse as the parent decodes it. Mode is
// included, since it is the field most recently added to the request and
// a codec that dropped it would silently turn every check into a real run.
func TestReadChildRequest_And_WriteChildResponse_RoundTrip(t *testing.T) {
	wantReq := wire.ChildRequest{
		FQCN:         "net.ssh.ping",
		Mode:         string(collection.ModeCheck),
		Params:       map[string]any{"key": "value"},
		JobID:        "job-1",
		DeviceID:     "dev-1",
		DeviceName:   "router1",
		DeviceHost:   "10.0.0.1",
		SSHPort:      22,
		Capabilities: []capability.Name{capability.NameSSHTransport},
		Secrets:      map[string]string{"username": "admin"},
	}
	reqBytes, err := json.Marshal(wantReq)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	gotReq, err := external.ReadChildRequest(bytes.NewReader(reqBytes))
	if err != nil {
		t.Fatalf("ReadChildRequest: %v", err)
	}
	if !reflect.DeepEqual(gotReq, wantReq) {
		t.Errorf("ReadChildRequest() = %+v, want %+v", gotReq, wantReq)
	}

	wantResp := wire.ChildResponse{Changed: true, Facts: map[string]any{"reply": "pong"}, Error: "partial failure"}
	var buf bytes.Buffer
	if err := external.WriteChildResponse(&buf, wantResp); err != nil {
		t.Fatalf("WriteChildResponse: %v", err)
	}
	// One frame is one newline-terminated line, which is what lets a
	// human or a test read a captured frame directly.
	if frame := buf.String(); !strings.HasSuffix(frame, "\n") || strings.Count(frame, "\n") != 1 {
		t.Errorf("WriteChildResponse wrote %q, want exactly one newline-terminated line", frame)
	}
	var gotResp wire.ChildResponse
	if err := json.NewDecoder(&buf).Decode(&gotResp); err != nil {
		t.Fatalf("decoding the written response: %v", err)
	}
	if !reflect.DeepEqual(gotResp, wantResp) {
		t.Errorf("response round trip = %+v, want %+v", gotResp, wantResp)
	}
}

func TestReadChildRequest_MalformedInputReturnsError(t *testing.T) {
	if _, err := external.ReadChildRequest(bytes.NewReader([]byte("not json"))); err == nil {
		t.Fatal("expected an error decoding malformed input, got nil")
	}
}

// TestReadChildRequest_ReadFailureIsReported covers the I/O-failure branch
// distinctly from the malformed-JSON branch: both must be refusals, and
// neither may yield a usable zero-valued request.
func TestReadChildRequest_ReadFailureIsReported(t *testing.T) {
	if _, err := external.ReadChildRequest(errReader{}); err == nil {
		t.Fatal("ReadChildRequest() = nil error, want the underlying read failure")
	}
}

// TestServeChild_WritesResponseAndReportsSuccess exercises the child's
// whole body, not just InvokeRequest: request decode, method dispatch, and
// the response frame actually being written to the stream that becomes
// fd 3 in a real spawn.
func TestServeChild_WritesResponseAndReportsSuccess(t *testing.T) {
	fqcn := registerTestMethod(t, "servechild_ok", collection.StatusImplemented,
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
	code := external.ServeChild(context.Background(), collection.Lookup, bytes.NewReader(reqBytes), &response, &errOut)

	if code != 0 {
		t.Errorf("ServeChild() = %d, want 0 (stderr: %s)", code, errOut.String())
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

// TestServeChild_MethodFailureIsAResponseNotAnExitCode pins the split
// ServeChild's doc comment draws: a method's own failure travels back as
// the response's Error with exit code 0, because a non-zero exit means the
// exchange itself broke and the parent reports that differently.
func TestServeChild_MethodFailureIsAResponseNotAnExitCode(t *testing.T) {
	fqcn := registerTestMethod(t, "servechild_fail", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, errors.New("device unreachable")
		})

	reqBytes, err := json.Marshal(&wire.ChildRequest{FQCN: fqcn})
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}

	var response, errOut bytes.Buffer
	code := external.ServeChild(context.Background(), collection.Lookup, bytes.NewReader(reqBytes), &response, &errOut)
	if code != 0 {
		t.Errorf("ServeChild() = %d, want 0: a method's failure is not a broken exchange", code)
	}
	var resp wire.ChildResponse
	if err := json.NewDecoder(&response).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Error != "device unreachable" {
		t.Errorf("response.Error = %q, want %q", resp.Error, "device unreachable")
	}
}

// TestServeChild_MalformedRequestExitsNonZero proves the child reports a
// decode failure as a non-zero exit rather than proceeding with a
// zero-valued request, which would look to the parent like a method that
// ran and did nothing.
func TestServeChild_MalformedRequestExitsNonZero(t *testing.T) {
	var response, errOut bytes.Buffer
	code := external.ServeChild(context.Background(), collection.Lookup, strings.NewReader("{not json"), &response, &errOut)

	if code != 1 {
		t.Errorf("ServeChild() = %d, want 1 for a malformed request", code)
	}
	if response.Len() != 0 {
		t.Errorf("response frame = %q, want nothing written when the request never decoded", response.String())
	}
	if errOut.Len() == 0 {
		t.Error("stderr is empty, want the decode failure reported")
	}
}

// TestServeChild_ResponseWriteFailureExitsNonZero covers the last branch:
// the method ran, but the parent can never learn the outcome. That has to
// be a non-zero exit, because a silent 0 would tell the parent the task
// succeeded while the response frame it is waiting for never arrives.
func TestServeChild_ResponseWriteFailureExitsNonZero(t *testing.T) {
	fqcn := registerTestMethod(t, "servechild_writefail", collection.StatusImplemented,
		func(_ context.Context, _ sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			return collection.Result{Changed: true}, nil
		})

	reqBytes, err := json.Marshal(&wire.ChildRequest{FQCN: fqcn})
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}

	var errOut bytes.Buffer
	code := external.ServeChild(context.Background(), collection.Lookup, bytes.NewReader(reqBytes), errWriter{}, &errOut)

	if code != 1 {
		t.Errorf("ServeChild() = %d, want 1 when the response cannot be written", code)
	}
	if errOut.Len() == 0 {
		t.Error("stderr is empty, want the write failure reported")
	}
}
