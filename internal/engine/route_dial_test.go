// Tests of a device's route as a dialer, through a real in-process SSH
// bastion that forwards to a real TCP target, its host key verified.
package engine_test

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// echoMessage is what a round trip sends through the route and reads back.
const echoMessage = "through the bastion"

// hops is an inventory holding the route's hop devices, with no groups.
type hops map[string]pkginventory.InventoryItem

func (h hops) GetByName(_ context.Context, name string) (pkginventory.InventoryItem, error) {
	if item, ok := h[name]; ok {
		return item, nil
	}
	return nil, inventory.ErrItemNotFound
}

func (h hops) GroupAncestry(context.Context, string) ([]inventory.HierarchyLayer, error) {
	return nil, nil
}

// routeFixture is a bastion, a target only the test reaches through it,
// and an inventory naming the bastion.
type routeFixture struct {
	bastion *remoteexectest.Server
	target  string
	inv     hops
	creds   credential.Store
}

func newRouteFixture(t *testing.T) *routeFixture {
	t.Helper()
	bastion, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bastion.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{knownhosts.Normalize(bastion.Addr())}, bastion.HostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)

	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			// One message back, then the end of the stream, so the
			// bastion's forwarding of it ends and the bastion can close.
			go func() {
				defer conn.Close()
				msg := make([]byte, len(echoMessage))
				if _, err := io.ReadFull(conn, msg); err == nil {
					_, _ = conn.Write(msg)
				}
			}()
		}
	}()

	hop, err := inventory.NewItemFactory().Build(record.Record{ID: "b1", Name: "bastion", Type: "linux_server",
		Properties: map[string]pkginventory.PropertyValue{"host": bastion.Host, "port": bastion.Port}})
	if err != nil {
		t.Fatal(err)
	}
	return &routeFixture{bastion: bastion, target: echo.Addr().String(), inv: hops{"bastion": hop}, creds: credential.NewStaticStore(bastion.Secrets())}
}

// roundTrip writes through dial to the echo target and reads it back.
func roundTrip(t *testing.T, dial sdk.RouteDial, target string) {
	t.Helper()
	conn, err := dial(context.Background(), "tcp", target)
	if err != nil {
		t.Fatalf("dialing %s through the route: %v", target, err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(echoMessage)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(echoMessage))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != echoMessage {
		t.Fatalf("read back %q, %v", got, err)
	}
}

// TestRouteDialerFor: a device with a route is dialed through its hop,
// whose secrets join the masking set; one with none gets no dialer; a hop
// not in the inventory, and an address the route cannot carry, are
// refused.
func TestRouteDialerFor(t *testing.T) {
	f := newRouteFixture(t)
	ctx := context.Background()
	cluster := &inventorytest.Stub{StubName: "k", Props: map[string]pkginventory.PropertyValue{"route": []any{"bastion"}}}

	dial, secrets, err := engine.RouteDialerFor(ctx, f.inv, f.creds, cluster)
	if err != nil || dial == nil {
		t.Fatalf("RouteDialerFor = %v", err)
	}
	if !slices.Contains(secrets, f.bastion.Password) {
		t.Error("the bastion's password is not in the masking set")
	}
	before := f.bastion.Logins()
	roundTrip(t, dial, f.target)
	if f.bastion.Logins() <= before {
		t.Error("the bastion saw no login, so the connection did not go through it")
	}
	for _, tc := range []struct{ network, address, want string }{
		{"udp", f.target, "carries TCP"},
		{"tcp", "no-port", "dialing"},
		{"tcp", "127.0.0.1:0", "not a TCP port"},
		{"tcp", "127.0.0.1:http", "not a TCP port"},
	} {
		if _, err := dial(ctx, tc.network, tc.address); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("dial(%s, %s) = %v, want a refusal saying %q", tc.network, tc.address, err, tc.want)
		}
	}

	direct := &inventorytest.Stub{StubName: "d"}
	if dial, secrets, err := engine.RouteDialerFor(ctx, f.inv, f.creds, direct); dial != nil || secrets != nil || err != nil {
		t.Errorf("a device with no route = %v, %v, %v; want no dialer", dial != nil, secrets, err)
	}
	lost := &inventorytest.Stub{StubName: "l", Props: map[string]pkginventory.PropertyValue{"route": []any{"missing"}}}
	if _, _, err := engine.RouteDialerFor(ctx, f.inv, f.creds, lost); err == nil || !strings.Contains(err.Error(), `hop "missing"`) {
		t.Errorf("a route naming no device = %v", err)
	}
}

// TestCollectionExecutor_HandsAMethodItsDevicesRoute: with device routes
// on, a method reaching an address of its own gets a dialer through the
// device's route (sdk.Router), and one for a device with no route gets
// none; an unresolvable route fails the task before the method runs.
func TestCollectionExecutor_HandsAMethodItsDevicesRoute(t *testing.T) {
	f := newRouteFixture(t)
	t.Cleanup(collection.SnapshotForTest())
	var routed sdk.RouteDial
	ran := 0
	if err := collection.Register(collection.Descriptor{Name: "routetest.reach",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"}},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ pkginventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			ran++
			routed = nil
			if r, ok := rc.(sdk.Router); ok {
				routed = r.DeviceRoute()
			}
			return collection.Result{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	exec := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext, engine.WithDeviceRoutes(f.inv, f.creds))
	task := &engine.Task{FQCN: "routetest.reach", Params: map[string]any{}}

	cluster := &inventorytest.Stub{StubName: "k", Props: map[string]pkginventory.PropertyValue{"route": []any{"bastion"}}}
	if _, err := exec.Execute(context.Background(), task, cluster); err != nil {
		t.Fatal(err)
	}
	if routed == nil {
		t.Fatal("a device with a route gave its method no dialer")
	}
	roundTrip(t, routed, f.target)

	if _, err := exec.Execute(context.Background(), task, &inventorytest.Stub{StubName: "d"}); err != nil || routed != nil {
		t.Errorf("a device with no route: %v, dialer %v", err, routed != nil)
	}
	before := ran
	lost := &inventorytest.Stub{StubName: "l", Props: map[string]pkginventory.PropertyValue{"route": []any{"missing"}}}
	if _, err := exec.Execute(context.Background(), task, lost); err == nil || errors.Is(err, context.Canceled) || ran != before {
		t.Errorf("an unresolvable route: %v (the method ran %d times more)", err, ran-before)
	}
}

// foreignContext is a run context of another implementation, which has no
// way to carry a route.
type foreignContext struct{ sdk.RunbookContext }

// TestCollectionExecutor_ARouteTheContextCannotCarryFailsTheTask: a device
// with a route, under a run context that cannot hand the method a dialer,
// fails the task before the method runs rather than letting it dial the
// address directly.
func TestCollectionExecutor_ARouteTheContextCannotCarryFailsTheTask(t *testing.T) {
	f := newRouteFixture(t)
	t.Cleanup(collection.SnapshotForTest())
	ran := false
	if err := collection.Register(collection.Descriptor{Name: "routetest.foreign",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"}},
		Invoke: func(context.Context, sdk.RunbookContext, pkginventory.InventoryItem, map[string]any) (collection.Result, error) {
			ran = true
			return collection.Result{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	foreign := func(ctx context.Context, device pkginventory.InventoryItem) (sdk.RunbookContext, error) {
		rc, err := engine.NewDeviceRunbookContext(ctx, device)
		return foreignContext{rc}, err
	}
	exec := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), foreign, engine.WithDeviceRoutes(f.inv, f.creds))
	cluster := &inventorytest.Stub{StubName: "k", Props: map[string]pkginventory.PropertyValue{"route": []any{"bastion"}}}
	_, err := exec.Execute(context.Background(), &engine.Task{FQCN: "routetest.foreign", Params: map[string]any{}}, cluster)
	if err == nil || !strings.Contains(err.Error(), "cannot carry") || ran {
		t.Fatalf("a route the context cannot carry: %v (method ran: %v)", err, ran)
	}
}
