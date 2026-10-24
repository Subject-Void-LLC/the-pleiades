// Tests of a device's console server as a dialer, through a real
// in-process SSH server scripted to answer as CML's console server does,
// its host key verified, with the inventory's own cml_controller and
// console-served cisco_router types.
package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// consoleUUID is the node's console line.
const consoleUUID = "0ba0f180-c5b2-4dcf-af64-bf9bb645a566"

// consoleFixture is a console server, an inventory with a controller for
// it and a node behind it, and the controller's credential.
type consoleFixture struct {
	server *remoteexectest.Server
	inv    hops
	creds  credential.Store
}

// build makes a device of typ from props through the inventory's factory.
func build(t *testing.T, name, typ string, props map[string]pkginventory.PropertyValue) pkginventory.InventoryItem {
	t.Helper()
	item, err := inventory.NewItemFactory().Build(record.Record{ID: pkginventory.DeviceID(name), Name: name, Type: typ, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func newConsoleFixture(t *testing.T) *consoleFixture {
	t.Helper()
	server, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Banner:  "****\r\nCML2 Console Server\r\n****",
		Prompt:  "consoles> ",
		Replies: map[string]string{"connect " + consoleUUID: "Connecting to console for P1\r\nConnected to CML terminalserver.\r\nEscape character is '^]'."},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{knownhosts.Normalize(server.Addr())}, server.HostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, knownHosts)
	inv := hops{
		"cml":   build(t, "cml", "cml_controller", map[string]pkginventory.PropertyValue{"api_base": "https://" + server.Host, "console_port": server.Port}),
		"linux": build(t, "linux", "linux_server", map[string]pkginventory.PropertyValue{"host": server.Host}),
	}
	return &consoleFixture{server: server, inv: inv, creds: credential.NewStaticStore(server.Secrets())}
}

// node is a cisco_router reached through server's console line.
func node(t *testing.T, server string) pkginventory.InventoryItem {
	return build(t, "P1", "cisco_router", map[string]pkginventory.PropertyValue{"transport": "console", "console_server": server, "console_line": consoleUUID})
}

// TestConsoleDialerFor: a console-served device is dialed through its
// console server with the server's own login, its host key verified, and
// attached to its line, with the server's password in the masking set; a
// device with no console server gets no dialer; and a server that is not
// in the inventory, not a controller, or has no login stored is refused.
func TestConsoleDialerFor(t *testing.T) {
	f := newConsoleFixture(t)
	ctx := context.Background()
	dial, secrets, err := engine.ConsoleDialerFor(ctx, f.inv, f.creds, node(t, "cml"))
	if err != nil || dial == nil {
		t.Fatalf("ConsoleDialerFor = %v", err)
	}
	if !slices.Contains(secrets, f.server.Password) {
		t.Error("the console server's password is not in the masking set")
	}
	shell, err := dial(ctx)
	if err != nil {
		t.Fatalf("dialing the console: %v", err)
	}
	if err := shell.Close(); err != nil {
		t.Errorf("closing the line: %v", err)
	}
	if got := f.server.Commands(); !slices.Contains(got, "connect "+consoleUUID) {
		t.Errorf("the console server was sent %q", got)
	}

	if dial, secrets, err := engine.ConsoleDialerFor(ctx, f.inv, f.creds, &inventorytest.Stub{StubName: "d"}); dial != nil || secrets != nil || err != nil {
		t.Errorf("a device with no console server = %v, %v, %v", dial != nil, secrets, err)
	}
	for name, tc := range map[string]struct {
		server string
		creds  credential.Store
		want   string
	}{
		"not in the inventory": {"missing", f.creds, `console server "missing"`},
		"not a controller":     {"linux", f.creds, "is not a cml_controller"},
		"no login stored":      {"cml", credential.NewLazyFileStore(t.TempDir()), "no login stored"},
	} {
		if _, _, err := engine.ConsoleDialerFor(ctx, f.inv, tc.creds, node(t, tc.server)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error mentioning %q", name, err, tc.want)
		}
	}
}

// TestConsoleDial_RefusesAnUnknownLine: a line the console server does not
// have fails the dial with the server's answer, and nothing is left open.
func TestConsoleDial_RefusesAnUnknownLine(t *testing.T) {
	f := newConsoleFixture(t)
	other := build(t, "P2", "cisco_router", map[string]pkginventory.PropertyValue{"transport": "console", "console_server": "cml", "console_line": "11111111-2222-4333-8444-555555555555"})
	dial, _, err := engine.ConsoleDialerFor(context.Background(), f.inv, f.creds, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dial(context.Background()); err == nil || !strings.Contains(err.Error(), "did not connect") {
		t.Errorf("an unknown line: %v", err)
	}
	// The server notices the close a moment after the client makes it,
	// so wait for it, with a margin no loaded runner reaches.
	deadline := time.Now().Add(10 * time.Second)
	for f.server.Live() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if live := f.server.Live(); live != 0 {
		t.Errorf("%d connection(s) left open after a refused line", live)
	}
}

// TestCollectionExecutor_HandsAMethodItsDevicesConsole: with device routes
// on, a method on a console-served device gets a dialer to its line
// (sdk.Consoler) and one on another device gets none.
func TestCollectionExecutor_HandsAMethodItsDevicesConsole(t *testing.T) {
	f := newConsoleFixture(t)
	t.Cleanup(collection.SnapshotForTest())
	var console sdk.ConsoleDial
	if err := collection.Register(collection.Descriptor{Name: "consoletest.reach",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"}},
		Invoke: func(_ context.Context, rc sdk.RunbookContext, _ pkginventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			console = nil
			if c, ok := rc.(sdk.Consoler); ok {
				console = c.DeviceConsole()
			}
			return collection.Result{}, nil
		}}); err != nil {
		t.Fatal(err)
	}
	exec := engine.NewCollectionActionExecutor(engine.NewBuiltinActionExecutor(), engine.NewDeviceRunbookContext, engine.WithDeviceRoutes(f.inv, f.creds))
	task := &engine.Task{FQCN: "consoletest.reach", Params: map[string]any{}}
	if _, err := exec.Execute(context.Background(), task, node(t, "cml")); err != nil || console == nil {
		t.Fatalf("a console-served device: %v, dialer %v", err, console != nil)
	}
	if _, err := exec.Execute(context.Background(), task, &inventorytest.Stub{StubName: "d"}); err != nil || console != nil {
		t.Errorf("a device with no console server: %v, dialer %v", err, console != nil)
	}
	if _, err := exec.Execute(context.Background(), task, node(t, "missing")); err == nil {
		t.Error("a console server not in the inventory did not fail the task")
	}
}
