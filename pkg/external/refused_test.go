// Tests that a child marks a refused login on its response, since the
// error's type cannot cross the process boundary and the parent's engine
// needs to know not to send the login again.
package external_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestInvokeRequest_ARefusedLoginIsMarked runs a method that logs in over
// sdk.Connect to a real SSH server with a password it refuses: the response
// carries the refusal's text and the flag. A method failing any other way
// carries no flag.
func TestInvokeRequest_ARefusedLoginIsMarked(t *testing.T) {
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	connect := func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
		conn, err := sdk.Connect(ctx, rc, device, params, "externaltest.connect")
		if err != nil {
			return collection.Result{}, err
		}
		_ = conn.Close()
		return collection.Result{}, nil
	}
	fails := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, errors.New("command exited 1")
	}
	manifest := collection.Manifest{Status: collection.StatusImplemented, Reversibility: fixtureReversibility}
	lookup := tableLookup(
		collection.Descriptor{Name: "externaltest.connect", Manifest: manifest, Invoke: connect},
		collection.Descriptor{Name: "externaltest.fails", Manifest: manifest, Invoke: fails},
	)
	request := func(fqcn, password string) wire.ChildRequest {
		return wire.ChildRequest{
			FQCN: fqcn, Mode: string(collection.ModeExecute),
			Params:     map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true},
			DeviceName: "d", DeviceHost: srv.Host, SSHPort: srv.Port,
			Capabilities: []capability.Name{capability.NameSSHTransport},
			Secrets:      map[string]string{wire.SecretUsername: srv.Username, wire.SecretPassword: password},
		}
	}

	resp := external.InvokeRequest(context.Background(), lookup, request("externaltest.connect", "wrong"))
	if resp.Error == "" || !resp.Refused {
		t.Fatalf("a refused login answered %+v, want its failure marked refused", resp)
	}
	if srv.Refusals() != 1 {
		t.Errorf("the server refused %d passwords, want 1", srv.Refusals())
	}
	if resp := external.InvokeRequest(context.Background(), lookup, request("externaltest.connect", srv.Password)); resp.Error != "" || resp.Refused {
		t.Errorf("the right password answered %+v, want a success", resp)
	}
	if resp := external.InvokeRequest(context.Background(), lookup, request("externaltest.fails", srv.Password)); resp.Error == "" || resp.Refused {
		t.Errorf("an ordinary failure answered %+v, want it unmarked", resp)
	}
}
