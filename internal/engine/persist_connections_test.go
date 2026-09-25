// Tests for connection persistence: the device ladder that decides it,
// and the Collection executor lending the run's pool and closing a
// device's connection after a method that changes what a login carries.
package engine_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestPersistConnections_Ladder is the device ladder: on by default, the
// most specific opinion winning, and anything but a boolean read as off.
func TestPersistConnections_Ladder(t *testing.T) {
	layer := func(name string, v any) inventory.HierarchyLayer {
		return inventory.HierarchyLayer{Name: name, Properties: map[string]interface{}{engine.PersistConnectionsProperty: v}}
	}
	none := inventory.HierarchyLayer{Name: "no opinion", Properties: map[string]interface{}{"other": 1}}
	tests := []struct {
		name     string
		ancestry []inventory.HierarchyLayer
		device   any // nil: the device has no opinion
		want     bool
	}{
		{"default", nil, nil, true},
		{"inventory off", []inventory.HierarchyLayer{layer("inv", false)}, nil, false},
		{"group back on beneath an inventory off", []inventory.HierarchyLayer{layer("inv", false), layer("grp", true)}, nil, true},
		{"a layer with no opinion changes nothing", []inventory.HierarchyLayer{layer("inv", false), none}, nil, false},
		{"device off beneath a group on", []inventory.HierarchyLayer{layer("grp", true)}, false, false},
		{"device on beneath an inventory off", []inventory.HierarchyLayer{layer("inv", false)}, true, true},
		{"a string is off, even true", nil, "true", false},
		{"a number is off", []inventory.HierarchyLayer{layer("grp", 1)}, nil, false},
		{"null is off", []inventory.HierarchyLayer{layer("grp", nil)}, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &inventorytest.Stub{StubName: "web1"}
			if tt.device != nil {
				stub.Props = map[string]pkginventory.PropertyValue{engine.PersistConnectionsProperty: tt.device}
			}
			if got := engine.PersistConnections(tt.ancestry, stub); got != tt.want {
				t.Errorf("PersistConnections = %v, want %v", got, tt.want)
			}
		})
	}
	if !engine.PersistConnections(nil, nil) {
		t.Error("no device and no ancestry should resolve to the default, on")
	}
}

// connectingMethod connects through sdk.Connect, runs one command and
// closes, as every SSH-backed Collection method does.
func connectingMethod(ctx context.Context, rc sdk.RunbookContext, device pkginventory.InventoryItem, params map[string]any) (collection.Result, error) {
	conn, err := sdk.Connect(ctx, rc, device, params, "enginetest")
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = conn.Close() }()
	_, err = conn.Run(ctx, "true")
	return collection.Result{}, err
}

// TestCollectionExecutor_LendsThePool drives real tasks through the
// Collection executor against a real SSH server and counts its logins:
// tasks share one while the device persists, each logs in when persist
// refuses it, and a method declaring EndsLoginSession closes the
// connection after a real run but not after a check.
func TestCollectionExecutor_LendsThePool(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	register := func(name string, ends bool) {
		t.Helper()
		if err := collection.Register(collection.Descriptor{
			Name: name,
			Manifest: collection.Manifest{
				Status:           collection.StatusImplemented,
				Reversibility:    collection.Reversibility{Notes: "a test fixture that changes nothing"},
				SupportsCheck:    true,
				EndsLoginSession: ends,
			},
			Invoke: connectingMethod,
			Check:  connectingMethod,
		}); err != nil {
			t.Fatal(err)
		}
	}
	register("enginetest.connects", false)
	register("enginetest.changes_login", true)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	secrets := map[string]string{wire.SecretUsername: srv.Username, wire.SecretPassword: srv.Password}
	newContext := func(context.Context, pkginventory.InventoryItem) (sdk.RunbookContext, error) {
		return engine.NewRunbookContext(secrets), nil
	}
	pool := remoteexec.NewPool(0)
	t.Cleanup(func() { _ = pool.Close() })
	persist := map[string]bool{"pooled": true}
	exec := engine.NewCollectionActionExecutor(&recordingFallback{}, newContext,
		engine.WithConnectionPool(pool, func(_ context.Context, d pkginventory.InventoryItem) bool { return persist[d.Name()] }))
	checker := exec.(engine.CheckExecutor)
	params := map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}
	pooled := newSSHDevice("pooled", srv.Host, srv.Port)
	fresh := newSSHDevice("fresh", srv.Host, srv.Port)

	step := func(fqcn string, device pkginventory.InventoryItem, check bool, wantLogins int64) {
		t.Helper()
		task := &engine.Task{FQCN: fqcn, Params: params}
		if check {
			_, err = checker.Check(context.Background(), task, device)
		} else {
			_, err = exec.Execute(context.Background(), task, device)
		}
		if err != nil {
			t.Fatalf("%s on %s: %v", fqcn, device.Name(), err)
		}
		if got := srv.Logins(); got != wantLogins {
			t.Fatalf("after %s on %s (check %v): %d logins, want %d", fqcn, device.Name(), check, got, wantLogins)
		}
	}
	step("enginetest.connects", pooled, false, 1)
	step("enginetest.connects", pooled, false, 1)
	step("enginetest.connects", fresh, false, 2)
	step("enginetest.connects", fresh, false, 3)
	step("enginetest.changes_login", pooled, true, 3)  // a check keeps the connection
	step("enginetest.changes_login", pooled, false, 3) // the run reuses it, then closes it
	step("enginetest.connects", pooled, false, 4)
	step("enginetest.connects", pooled, false, 4)
}
