package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	entinventory "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// fakeHopInventory is a hopChainInventory test double: a fixed set of
// items GetByName can resolve, and a fixed per-device ancestry
// GroupAncestry returns. Tests that only need a device-level route
// override (Properties["route"], applied by ResolveRoute after folding
// ancestry) leave ancestry nil; internal/inventory's own tests already
// cover the ancestry walk itself in depth, so this double is deliberately
// not a second implementation of that logic.
type fakeHopInventory struct {
	items       map[string]inventory.InventoryItem
	ancestry    map[string][]entinventory.HierarchyLayer
	ancestryErr error
}

func (f *fakeHopInventory) GetByName(_ context.Context, name string) (inventory.InventoryItem, error) {
	item, ok := f.items[name]
	if !ok {
		return nil, entinventory.ErrItemNotFound
	}
	return item, nil
}

func (f *fakeHopInventory) GroupAncestry(_ context.Context, deviceName string) ([]entinventory.HierarchyLayer, error) {
	if f.ancestryErr != nil {
		return nil, f.ancestryErr
	}
	return f.ancestry[deviceName], nil
}

// TestTransportActionExecutor_ResolvesConfiguredHopChain proves the real
// engine wiring end to end: a device's own Properties["route"] resolves
// through ResolveRoute into a real transport.Target.Route, reaching the
// bound Transport with the hop's Host/Port/DeviceName populated from a
// second, independently looked-up inventory item and a second,
// independently looked-up credential.
func TestTransportActionExecutor_ResolvesConfiguredHopChain(t *testing.T) {
	bastion := newSSHDevice("bastion", "10.0.0.1", 2200)
	target := newSSHDevice("target", "10.0.0.5", 22)
	target.Props = map[string]inventory.PropertyValue{
		"route": []interface{}{"bastion"},
	}

	hopInv := &fakeHopInventory{items: map[string]inventory.InventoryItem{"bastion": bastion}}
	credentials := fakeCredentialStore{perDevice: map[string]credential.Credential{
		"target":  {Username: "admin", Password: "target-secret"},
		"bastion": {Username: "bastion-admin", Password: "bastion-secret"},
	}}

	var capturedTarget transport.Target
	binding := sshBinding(func(_ context.Context, tgt transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
		capturedTarget = tgt
		return transport.Result{ExitCode: 0}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		credentials,
		hopInv,
		engine.NewBuiltinActionExecutor(),
	)

	_, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(capturedTarget.Route) != 1 {
		t.Fatalf("target.Route = %+v, want exactly 1 hop", capturedTarget.Route)
	}
	hop := capturedTarget.Route[0]
	if hop.Host != "10.0.0.1" || hop.Port != 2200 {
		t.Errorf("hop = %+v, want host 10.0.0.1 port 2200 (the bastion's own address)", hop)
	}
	if hop.DeviceName != "bastion" {
		t.Errorf("hop.DeviceName = %q, want %q", hop.DeviceName, "bastion")
	}
	if hop.Credential.Password != "bastion-secret" {
		t.Errorf("hop.Credential.Password = %q, want the bastion's OWN credential, not the target's", hop.Credential.Password)
	}
}

// TestTransportActionExecutor_NilInventorySkipsHopResolution proves a nil
// inventoryRepo (internal/adapters/native's per-task subprocess) behaves
// exactly as this executor did before Phase 72: a configured
// Properties["route"] is never even looked at, and the call succeeds as
// a direct connection.
func TestTransportActionExecutor_NilInventorySkipsHopResolution(t *testing.T) {
	target := newSSHDevice("target", "10.0.0.5", 22)
	target.Props = map[string]inventory.PropertyValue{"route": []interface{}{"bastion"}}

	var capturedTarget transport.Target
	binding := sshBinding(func(_ context.Context, tgt transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
		capturedTarget = tgt
		return transport.Result{ExitCode: 0}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		nil, // no inventory: hop resolution must be skipped entirely
		engine.NewBuiltinActionExecutor(),
	)

	if _, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if capturedTarget.Route != nil {
		t.Errorf("target.Route = %+v, want nil: a nil inventoryRepo must never resolve a route", capturedTarget.Route)
	}
}

// TestTransportActionExecutor_UnresolvableHopFailsNamingTheHop proves a
// route naming a device that does not exist (or has no stored
// credential) is a hard error naming that hop, never a silent fallback to
// the target's own credential and never a dial attempt.
func TestTransportActionExecutor_UnresolvableHopFailsNamingTheHop(t *testing.T) {
	target := newSSHDevice("target", "10.0.0.5", 22)
	target.Props = map[string]inventory.PropertyValue{"route": []interface{}{"missing-bastion"}}

	hopInv := &fakeHopInventory{items: map[string]inventory.InventoryItem{}}
	dialed := false
	binding := sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
		dialed = true
		return transport.Result{}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		hopInv,
		engine.NewBuiltinActionExecutor(),
	)

	_, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target)
	if err == nil {
		t.Fatal("expected an error for a route naming an unresolvable hop")
	}
	if !strings.Contains(err.Error(), "missing-bastion") {
		t.Errorf("err = %v, want it to name the unresolvable hop", err)
	}
	if dialed {
		t.Error("expected no dial attempt against an unresolvable hop chain")
	}
}

// TestTransportActionExecutor_MasksHopSecretsOnTransportError proves the
// masking union extends to every hop's own credential material, not just
// the target's: a bastion's password leaking through a transport-level
// error message is exactly the class of gap FAILURE_PATTERNS.md #22
// already recorded for a different shape.
func TestTransportActionExecutor_MasksHopSecretsOnTransportError(t *testing.T) {
	const bastionSecret = "s3cr3t-bastion-pw"

	bastion := newSSHDevice("bastion", "10.0.0.1", 22)
	target := newSSHDevice("target", "10.0.0.5", 22)
	target.Props = map[string]inventory.PropertyValue{"route": []interface{}{"bastion"}}

	hopInv := &fakeHopInventory{items: map[string]inventory.InventoryItem{"bastion": bastion}}
	credentials := fakeCredentialStore{perDevice: map[string]credential.Credential{
		"target":  {Username: "admin", Password: "target-secret"},
		"bastion": {Username: "bastion-admin", Password: bastionSecret},
	}}

	binding := sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
		return transport.Result{}, fmt.Errorf("dial failed through hop: auth rejected for password %s", bastionSecret)
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		credentials,
		hopInv,
		engine.NewBuiltinActionExecutor(),
	)

	_, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target)
	if err == nil {
		t.Fatal("expected the transport error to surface")
	}
	if strings.Contains(err.Error(), bastionSecret) {
		t.Errorf("err = %v, want the bastion's own password masked out of the transport error, exactly like the target's own", err)
	}
}

// TestTransportActionExecutor_NoConfiguredRouteIsDirectConnection proves a
// device with no "route" Properties key at all, and no ancestry opinion
// either, resolves to a nil Route: the ordinary, overwhelmingly common
// case of a device with no bastion configured.
func TestTransportActionExecutor_NoConfiguredRouteIsDirectConnection(t *testing.T) {
	target := newSSHDevice("target", "10.0.0.5", 22)

	var capturedTarget transport.Target
	binding := sshBinding(func(_ context.Context, tgt transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
		capturedTarget = tgt
		return transport.Result{ExitCode: 0}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		&fakeHopInventory{items: map[string]inventory.InventoryItem{}},
		engine.NewBuiltinActionExecutor(),
	)

	if _, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if capturedTarget.Route != nil {
		t.Errorf("target.Route = %+v, want nil for a device with no configured route", capturedTarget.Route)
	}
}

// TestTransportActionExecutor_MalformedRoutePropertyIsIgnored proves a
// "route" property present but not a valid list of strings is treated as
// "no opinion" (routeExtract's own documented contract) rather than a
// hard error: a config-validation concern this fold does not own. Both
// ways a stored value can fail to be a valid route are covered: not a
// list at all, and a list holding a non-string element.
func TestTransportActionExecutor_MalformedRoutePropertyIsIgnored(t *testing.T) {
	tests := []struct {
		name  string
		route inventory.PropertyValue
	}{
		{name: "not a list at all", route: "not-a-list"},
		{name: "list with a non-string element", route: []interface{}{"bastion", 42}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target := newSSHDevice("target", "10.0.0.5", 22)
			target.Props = map[string]inventory.PropertyValue{"route": tc.route}

			var capturedTarget transport.Target
			binding := sshBinding(func(_ context.Context, tgt transport.Target, _ credential.Credential, _ string) (transport.Result, error) {
				capturedTarget = tgt
				return transport.Result{ExitCode: 0}, nil
			})

			actions := engine.NewTransportActionExecutor(
				map[string]engine.TransportBinding{"ssh_exec": binding},
				fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
				&fakeHopInventory{items: map[string]inventory.InventoryItem{}},
				engine.NewBuiltinActionExecutor(),
			)

			if _, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if capturedTarget.Route != nil {
				t.Errorf("target.Route = %+v, want nil: a malformed route property must be ignored, not crash the dispatch", capturedTarget.Route)
			}
		})
	}
}

// TestTransportActionExecutor_AncestryLookupErrorSurfaces proves a real
// failure resolving the group/inventory hierarchy (a database error, not
// "nothing configured") is reported rather than silently treated as no
// route.
func TestTransportActionExecutor_AncestryLookupErrorSurfaces(t *testing.T) {
	target := newSSHDevice("target", "10.0.0.5", 22)

	binding := sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
		return transport.Result{ExitCode: 0}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		&fakeHopInventory{ancestryErr: errors.New("simulated database failure")},
		engine.NewBuiltinActionExecutor(),
	)

	_, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target)
	if err == nil || !strings.Contains(err.Error(), "simulated database failure") {
		t.Fatalf("err = %v, want the ancestry lookup failure to surface", err)
	}
}

// TestTransportActionExecutor_HopNotSSHCapableFails proves a hop that
// resolves to a real inventory item, but one that does not implement
// SSHTransportCapable, is a hard error naming the hop rather than a nil
// pointer or a silent skip.
func TestTransportActionExecutor_HopNotSSHCapableFails(t *testing.T) {
	target := newSSHDevice("target", "10.0.0.5", 22)
	target.Props = map[string]inventory.PropertyValue{"route": []interface{}{"not-ssh-hop"}}

	nonSSHHop := &inventorytest.Stub{StubName: "not-ssh-hop"}

	binding := sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
		return transport.Result{ExitCode: 0}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		&fakeHopInventory{items: map[string]inventory.InventoryItem{"not-ssh-hop": nonSSHHop}},
		engine.NewBuiltinActionExecutor(),
	)

	_, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target)
	if err == nil || !strings.Contains(err.Error(), "not-ssh-hop") {
		t.Fatalf("err = %v, want it to name the non-SSH-capable hop", err)
	}
}

// TestTransportActionExecutor_HopCredentialLookupErrorFails proves a hop
// resolved to a real, SSH-capable inventory item but with no stored
// credential of its own fails naming that hop, and never falls back to
// the target's own credential.
func TestTransportActionExecutor_HopCredentialLookupErrorFails(t *testing.T) {
	bastion := newSSHDevice("bastion", "10.0.0.1", 22)
	target := newSSHDevice("target", "10.0.0.5", 22)
	target.Props = map[string]inventory.PropertyValue{"route": []interface{}{"bastion"}}

	// perDevice covers "target" only; "bastion" falls through to err.
	credentials := fakeCredentialStore{
		perDevice: map[string]credential.Credential{"target": {Username: "admin", Password: "target-secret"}},
		err:       credential.ErrNotFound,
	}

	binding := sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
		return transport.Result{ExitCode: 0}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		credentials,
		&fakeHopInventory{items: map[string]inventory.InventoryItem{"bastion": bastion}},
		engine.NewBuiltinActionExecutor(),
	)

	_, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target)
	if err == nil || !strings.Contains(err.Error(), "bastion") {
		t.Fatalf("err = %v, want it to name the hop with no stored credential", err)
	}
}

// TestTransportActionExecutor_AbsurdRouteLengthRejectedBeforeAnyDial
// proves an absurdly long configured route is rejected once, at resolve
// time, before any inventory or credential lookup for any of its
// entries, rather than resolving "successfully" into a huge chain that
// only fails (or hangs) one dial at a time.
func TestTransportActionExecutor_AbsurdRouteLengthRejectedBeforeAnyDial(t *testing.T) {
	target := newSSHDevice("target", "10.0.0.5", 22)
	route := make([]interface{}, 1000)
	for i := range route {
		route[i] = fmt.Sprintf("hop-%d", i)
	}
	target.Props = map[string]inventory.PropertyValue{"route": route}

	var lookups int
	hopInv := &fakeHopInventoryCountingLookups{lookups: &lookups}

	binding := sshBinding(func(context.Context, transport.Target, credential.Credential, string) (transport.Result, error) {
		return transport.Result{ExitCode: 0}, nil
	})

	actions := engine.NewTransportActionExecutor(
		map[string]engine.TransportBinding{"ssh_exec": binding},
		fakeCredentialStore{cred: credential.Credential{Username: "admin", Password: "x"}},
		hopInv,
		engine.NewBuiltinActionExecutor(),
	)

	_, err := actions.Execute(context.Background(), &engine.Task{FQCN: "ssh_exec", Params: map[string]interface{}{"command": "uptime"}}, target)
	if err == nil {
		t.Fatal("expected a 1000-hop route to be rejected")
	}
	if lookups != 0 {
		t.Errorf("GetByName was called %d times, want 0: the length bound must reject before resolving any individual hop", lookups)
	}
}

// fakeHopInventoryCountingLookups counts GetByName calls, so
// TestTransportActionExecutor_AbsurdRouteLengthRejectedBeforeAnyDial can
// prove the length bound short-circuits before any per-hop work, not
// just that it eventually returns an error after doing that work anyway.
type fakeHopInventoryCountingLookups struct {
	lookups *int
}

func (f *fakeHopInventoryCountingLookups) GetByName(_ context.Context, _ string) (inventory.InventoryItem, error) {
	*f.lookups++
	return nil, entinventory.ErrItemNotFound
}

func (f *fakeHopInventoryCountingLookups) GroupAncestry(_ context.Context, _ string) ([]entinventory.HierarchyLayer, error) {
	return nil, nil
}
