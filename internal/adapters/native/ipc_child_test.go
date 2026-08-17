package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func registerChildTestMethod(t *testing.T, suffix string, status collection.Status, fn collection.Method) string {
	t.Helper()
	name := "nativechildtest." + suffix
	if err := collection.Register(collection.Descriptor{
		Name: name,
		// A test fixture still answers the question every real implemented
		// method answers. Not reversible, with the reason registration
		// requires: these fixtures change nothing on any device.
		Manifest: collection.Manifest{Status: status, Reversibility: collection.Reversibility{Notes: "a test fixture that changes nothing"}},
		Invoke:   fn,
	}); err != nil {
		t.Fatalf("registering %s: %v", name, err)
	}
	return name
}

func TestInvokeChild_Success(t *testing.T) {
	var gotSecrets map[string]string
	var gotDevice inventory.InventoryItem
	name := registerChildTestMethod(t, "success", collection.StatusImplemented,
		func(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
			gotSecrets = rc.InjectSecrets()
			gotDevice = device
			if err := rc.SetStat("echoed", params["message"]); err != nil {
				return collection.Result{}, err
			}
			return collection.Result{Changed: true}, nil
		})

	req := wire.ChildRequest{
		FQCN:       name,
		Params:     map[string]any{"message": "hello"},
		DeviceName: "router1",
		DeviceHost: "10.0.0.1",
		Secrets:    map[string]string{"username": "admin"},
	}

	resp := invokeChild(context.Background(), req)
	if resp.Error != "" {
		t.Fatalf("invokeChild() error = %q, want empty", resp.Error)
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
	if gotDevice == nil || gotDevice.Name() != "router1" {
		t.Errorf("method received device %v, want name router1", gotDevice)
	}
}

func TestInvokeChild_UnregisteredFQCN(t *testing.T) {
	resp := invokeChild(context.Background(), wire.ChildRequest{FQCN: "does.not.exist"})
	if resp.Error == "" {
		t.Fatal("expected a non-empty Error for an unregistered fqcn")
	}
}

func TestInvokeChild_DeclaredNotImplemented(t *testing.T) {
	name := registerChildTestMethod(t, "declared", collection.StatusDeclared, nil)
	resp := invokeChild(context.Background(), wire.ChildRequest{FQCN: name})
	if resp.Error == "" {
		t.Fatal("expected a non-empty Error for a declared-but-unimplemented method")
	}
}

func TestInvokeChild_MethodErrorReported(t *testing.T) {
	sentinel := errors.New("device unreachable")
	name := registerChildTestMethod(t, "failing", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, sentinel
		})

	resp := invokeChild(context.Background(), wire.ChildRequest{FQCN: name})
	if resp.Error != sentinel.Error() {
		t.Errorf("Error = %q, want %q", resp.Error, sentinel.Error())
	}
}

func TestReadChildRequest_And_WriteChildResponse_RoundTrip(t *testing.T) {
	want := wire.ChildRequest{FQCN: "net.ssh.ping", Params: map[string]any{"key": "value"}, DeviceHost: "10.0.0.1"}
	var buf bytes.Buffer
	if err := writeChildResponse(&buf, wire.ChildResponse{Changed: true}); err != nil {
		t.Fatalf("writeChildResponse: %v", err)
	}

	reqBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := readChildRequest(bytes.NewReader(reqBytes))
	if err != nil {
		t.Fatalf("readChildRequest: %v", err)
	}
	if got.FQCN != want.FQCN || got.DeviceHost != want.DeviceHost {
		t.Errorf("readChildRequest() = %+v, want %+v", got, want)
	}
}

func TestReadChildRequest_MalformedInputReturnsError(t *testing.T) {
	if _, err := readChildRequest(bytes.NewReader([]byte("not json"))); err == nil {
		t.Fatal("expected an error decoding malformed input, got nil")
	}
}

func TestChildRunbookContext_ZeroClearsSecrets(t *testing.T) {
	rc := newChildRunbookContext(map[string]string{"password": "hunter2"})
	if got := rc.InjectSecrets(); got["password"] != "hunter2" {
		t.Fatalf("InjectSecrets() before zero = %v, want password=hunter2", got)
	}

	rc.zero()

	if got := rc.InjectSecrets(); len(got) != 0 {
		t.Errorf("InjectSecrets() after zero = %v, want empty", got)
	}
}

func TestChildRunbookContext_SetStatAndEmitFactShareOneMap(t *testing.T) {
	rc := newChildRunbookContext(nil)
	if err := rc.SetStat("a", 1); err != nil {
		t.Fatalf("SetStat: %v", err)
	}
	if err := rc.EmitFact("b", 2); err != nil {
		t.Fatalf("EmitFact: %v", err)
	}
	facts := rc.Facts()
	if facts["a"] != 1 || facts["b"] != 2 {
		t.Errorf("Facts() = %v, want a=1 b=2", facts)
	}
}
