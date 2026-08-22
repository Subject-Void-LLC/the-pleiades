package wait_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pleiades/builtin/wait"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// These tests run pleiades.builtin.wait.port against a REAL SSH server, in
// this process, that hands every command it receives to a real /bin/sh on
// this machine (pkg/remoteexec/remoteexectest). Nothing about the
// transport, the shell, python3, bash or the /dev/tcp redirection is
// stubbed, which is what RULE 0 asks of a method whose entire subject is
// what a real shell does with the command it builds.
//
// The ports are real too: every open port below is a real net.Listener on
// loopback, and every closed one is a port that was bound and released, so
// what the probe reports is what the kernel says rather than what a stand
// in was told to say.
//
// The one thing that IS stood in for is a device's own tooling. PATH is
// narrowed to a directory this file fills, which is how a test picks which
// prober runs and how it reaches the branches a device with a broken or
// missing tool produces. The shell interpreting the probe is still the
// real one either way.
//
// Every helper is prefixed "port" because a sibling method landing in this
// package would share one Go test package and Go has no file-level scope.

// errPortStat is what a stub context returns when a test wants recording
// to fail, so the branch where the wait succeeded and the record did not
// is reachable.
var errPortStat = errors.New("recording the result failed")

// portServer is where the in-process SSH harness is listening, plus the
// one credential it accepts.
type portServer struct {
	host     string
	port     int
	username string
	password string
}

// startPortServer starts a real SSH server that runs every command
// through a real /bin/sh, and stops it when the test ends.
func startPortServer(t *testing.T) portServer {
	return startPortServerWithSessionBudget(t, -1)
}

// startPortServerWithSessionBudget is startPortServer with a cap on how
// many session channels it accepts before refusing.
//
// A budget of zero is the only way to reach the branch where the
// connection authenticates and then cannot carry the tool check, which is
// what a device under session pressure looks like from this side.
func startPortServerWithSessionBudget(t *testing.T, budget int) portServer {
	t.Helper()

	// A negative budget is this file's spelling of "unlimited", which the
	// harness spells as an absent limit.
	opts := remoteexectest.Options{}
	if budget >= 0 {
		opts.SessionLimit = remoteexectest.Limit(budget)
	}

	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	return portServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// portTarget is a device reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately
// does not provide.
type portTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *portTarget) SSHHost() string { return d.host }
func (d *portTarget) SSHPort() int    { return d.port }

// newPortStub builds the base inventory item both device shapes wrap.
func newPortStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "wait-1",
		StubName:  "wait-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NameNetworkAddressable},
		StubState: inventory.StateActive,
	}
}

// newPortTarget builds the SSH-reachable device a test runs against.
func newPortTarget(server portServer) *portTarget {
	return &portTarget{Stub: newPortStub(), host: server.host, port: server.port}
}

// newPortUnreachable builds a device this method cannot reach at all: the
// bare stub, which implements InventoryItem and nothing else.
func newPortUnreachable() inventory.InventoryItem {
	return newPortStub()
}

// portContext is a minimal sdk.RunbookContext carrying a fixed secret set,
// standing in for the real one the composition root builds from the
// credential store on the Crawl tier or the dispatch payload on the Walk
// tier.
type portContext struct {
	secrets map[string]string
	stats   map[string]any

	// failOn, when set, makes SetStat fail for that one key.
	failOn string
}

func newPortContext(server portServer) *portContext {
	return &portContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *portContext) InjectSecrets() map[string]string { return c.secrets }

func (c *portContext) SetStat(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errPortStat
	}
	c.stats[key] = value
	return nil
}

func (c *portContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// portOnlyTools narrows PATH to a directory holding a wrapper for each
// named tool and nothing else, which is how a test chooses which prober
// the method will find on the "device".
//
// A wrapper script rather than a symlink, because a symlinked python3
// resolves its own installation prefix from the path it was started
// through, and an interpreter that cannot find its standard library is a
// different failure than the one being set up here. The wrapper execs the
// real binary by absolute path, so PATH cannot affect it.
//
// The directory is returned so a test can add a deliberately broken tool
// to it afterwards.
func portOnlyTools(t *testing.T, tools ...string) string {
	t.Helper()

	dir := t.TempDir()
	for _, tool := range tools {
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("this machine has no %s, so the prober that needs it cannot be exercised here: %v", tool, err)
		}
		portWriteTool(t, dir, tool, fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", real))
	}

	// Set on this process, which is what the harness's own exec.Command
	// inherits when it runs /bin/sh for a session.
	t.Setenv("PATH", dir)
	return dir
}

// portWriteTool drops an executable script named tool into dir.
func portWriteTool(t *testing.T, dir, tool, script string) {
	t.Helper()

	path := filepath.Join(dir, tool)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the %s stand-in: %v", tool, err)
	}
}

// portOpenListener binds a real TCP listener on loopback and returns its
// port, closing it when the test ends.
func portOpenListener(t *testing.T) (net.Listener, int) {
	t.Helper()
	return portOpenListenerOn(t, "127.0.0.1")
}

// portOpenListenerOn is portOpenListener bound to a specific loopback
// address, which is how a test proves the host parameter is really used.
func portOpenListenerOn(t *testing.T, host string) (net.Listener, int) {
	t.Helper()

	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("this machine cannot bind %s: %v", host, err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %T is not TCP", listener.Addr())
	}
	return listener, addr.Port
}

// portClosedPort returns a port number nothing is listening on, by binding
// one and releasing it immediately.
func portClosedPort(t *testing.T) int {
	t.Helper()

	listener, port := portOpenListener(t)
	if err := listener.Close(); err != nil {
		t.Fatalf("releasing the port: %v", err)
	}
	return port
}

// portParams builds a task's params with host key verification off, which
// every test against the harness needs because the harness generates a
// fresh host key per run.
func portParams(extra map[string]any) map[string]any {
	params := map[string]any{"insecure_skip_host_key_verify": true}
	for key, value := range extra {
		params[key] = value
	}
	return params
}

// TestPort_Registered proves the method registered itself as implemented,
// with the reversibility answer a rollback engine reads.
//
// Reversible is pinned to false WITH a reason. Registration refuses a bare
// false, so the Notes cannot silently vanish, but nothing stops somebody
// flipping this to true later; if that ever happens without the method
// learning to emit an inverse, a rollback plan would list a task it can
// never produce an instruction for.
func TestPort_Registered(t *testing.T) {
	d, ok := collection.Lookup("pleiades.builtin.wait.port")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "pleiades.builtin.wait.port")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = true, want false: this method changes nothing, so it can never emit an inverse")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says why this cannot be undone")
	}
}

// TestPort_RefusesAMissingPort proves the one required parameter is really
// required, and that the refusal happens before any connection.
//
// A nil device would fail in connect, so reaching the expected message is
// itself the proof that nothing tried to dial.
func TestPort_RefusesAMissingPort(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{},
		{"host": "127.0.0.1"},
		{"port": nil},
	} {
		_, err := wait.Port(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "port is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestPort_RefusesAPortThatIsNotAPort covers both ends of the range and
// zero, since a port of zero means "any free port" to bind and means
// nothing at all to connect.
func TestPort_RefusesAPortThatIsNotAPort(t *testing.T) {
	for _, number := range []int{0, -1, 65536, 100000} {
		_, err := wait.Port(context.Background(), nil, nil, map[string]any{"port": number})
		if err == nil {
			t.Fatalf("port %d: expected a refusal, got nil", number)
		}
		if !strings.Contains(err.Error(), "is not a TCP port") {
			t.Errorf("port %d: error = %q, want it to say the value is out of range", number, err)
		}
	}
}

// TestPort_RefusesAPortThatIsNotANumber covers the mirror image of the
// mode trap file.permissions guards against: quoting a value that YAML
// would otherwise have decoded as a number.
//
// Treating a wrong type as absent, which is what sdk.StringParam's
// numeric cousin would do, would answer "port is required" to somebody
// who plainly wrote one.
func TestPort_RefusesAPortThatIsNotANumber(t *testing.T) {
	for _, value := range []any{"8080", true, []any{80}} {
		_, err := wait.Port(context.Background(), nil, nil, map[string]any{"port": value})
		if err == nil {
			t.Fatalf("port %v: expected a refusal, got nil", value)
		}
		if !strings.Contains(err.Error(), "not a number") {
			t.Errorf("port %v: error = %q, want it to say the value is not a number", value, err)
		}
		if !strings.Contains(err.Error(), "unquoted") {
			t.Errorf("port %v: error = %q, want it to say how to write it instead", value, err)
		}
	}
}

// TestPort_AcceptsThePortShapesEachTierProduces proves a port survives
// both decodings.
//
// The Crawl tier decodes YAML into an int and the Walk tier decodes the
// same task from JSON across the Runner's per-task subprocess boundary,
// where every number is a float64. A method that accepted only one of
// them would work on one tier and fail on the other, which is the worst
// shape of bug this repository has: it passes every unit test.
func TestPort_AcceptsThePortShapesEachTierProduces(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	_, open := portOpenListener(t)

	for _, value := range []any{open, int64(open), float64(open)} {
		rc := newPortContext(server)
		result, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
			"port": value,
		}))
		if err != nil {
			t.Fatalf("port as %T: Port: %v", value, err)
		}
		if result.Changed {
			t.Errorf("port as %T: Changed = true, want false", value)
		}
		if got := rc.stats["port"]; got != open {
			t.Errorf("port as %T: port stat = %v, want %d", value, got, open)
		}
	}
}

// TestPort_RefusesAFractionalNumber proves a float that is not whole is
// refused rather than truncated, since nobody can guess what 8080.5 was
// meant to be.
func TestPort_RefusesAFractionalNumber(t *testing.T) {
	tests := []struct {
		params map[string]any
		unit   string
	}{
		{params: map[string]any{"port": 8080.5}, unit: "ports"},
		{params: map[string]any{"port": 8080, "timeout": 1.5}, unit: "seconds"},
	}

	for _, tt := range tests {
		_, err := wait.Port(context.Background(), nil, nil, tt.params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", tt.params)
		}
		if !strings.Contains(err.Error(), "not a whole number of "+tt.unit) {
			t.Errorf("params %v: error = %q, want it to name the unit %q", tt.params, err, tt.unit)
		}
	}
}

// TestPort_RefusesATimingValueOutOfRange proves each timing parameter
// carries its own floor and the shared ceiling.
//
// The floors differ for real reasons and the test pins each one: a
// timeout of zero gives up before it looks, a sleep of zero opens SSH
// sessions in a tight loop, and a delay of zero is simply the default.
func TestPort_RefusesATimingValueOutOfRange(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "timeout zero", params: map[string]any{"port": 80, "timeout": 0}, want: "timeout 0 is below 1"},
		{name: "sleep zero", params: map[string]any{"port": 80, "sleep": 0}, want: "sleep 0 is below 1"},
		{name: "delay negative", params: map[string]any{"port": 80, "delay": -1}, want: "delay -1 is below 0"},
		{name: "timeout over a day", params: map[string]any{"port": 80, "timeout": 86401}, want: "second ceiling"},
		{name: "delay over a day", params: map[string]any{"port": 80, "delay": 90000}, want: "second ceiling"},
		{name: "sleep over a day", params: map[string]any{"port": 80, "sleep": 90000}, want: "second ceiling"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := wait.Port(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestPort_RefusesAnUnknownState proves Ansible's other wait_for states
// are refused by name rather than quietly read as started.
//
// present, absent and drained are all real wait_for values, so a
// converted playbook can carry one here. Silently treating drained as
// started would wait for the wrong condition and report success.
func TestPort_RefusesAnUnknownState(t *testing.T) {
	for _, state := range []string{"present", "absent", "drained", "Started", ""} {
		params := map[string]any{"port": 80, "state": state}
		if state == "" {
			// An explicitly empty state is the one case that must NOT be a
			// refusal: it is indistinguishable from absent and so takes the
			// default.
			_, err := wait.Port(context.Background(), nil, nil, params)
			if err != nil && strings.Contains(err.Error(), "must be") {
				t.Errorf("an empty state was refused, want it treated as absent: %v", err)
			}
			continue
		}

		_, err := wait.Port(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("state %q: expected a refusal, got nil", state)
		}
		if !strings.Contains(err.Error(), "must be \"started\" or \"stopped\"") {
			t.Errorf("state %q: error = %q, want it to name the two accepted values", state, err)
		}
	}
}

// TestPort_RefusesAHostThatIsNotText proves a host written as a bare
// number is refused rather than falling back to the loopback default,
// which would probe a machine nobody asked about.
func TestPort_RefusesAHostThatIsNotText(t *testing.T) {
	_, err := wait.Port(context.Background(), nil, nil, map[string]any{"port": 80, "host": 10})
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "host is int, not text") {
		t.Errorf("error = %q, want it to name the parameter and its type", err)
	}
}

// TestPort_RefusesAStateThatIsNotText covers the same question at the
// other text parameter, which is its own call site and could be wired to
// the wrong value.
func TestPort_RefusesAStateThatIsNotText(t *testing.T) {
	_, err := wait.Port(context.Background(), nil, nil, map[string]any{"port": 80, "state": 1})
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "state is int, not text") {
		t.Errorf("error = %q, want it to name the parameter and its type", err)
	}
}

// TestPort_RefusesAnUnreachableDevice covers the connect failure path,
// which is what a device with no SSH transport produces.
func TestPort_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := wait.Port(context.Background(), newPortContext(portServer{}), newPortUnreachable(), map[string]any{"port": 80})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestPort_ReturnsWhenThePortIsAlreadyOpen is the simplest success: the
// first probe already sees what the task is waiting for.
//
// The port is a real listener, so exit 0 comes from a real TCP handshake
// rather than from anything this test told the shell to say. The stats are
// checked against values that are NOT the defaults wherever possible, so a
// method that ignored a parameter could not pass.
func TestPort_ReturnsWhenThePortIsAlreadyOpen(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	_, open := portOpenListener(t)

	result, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port": open,
	}))
	if err != nil {
		t.Fatalf("Port: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true: waiting observes, it does not act")
	}
	if got := rc.stats["port"]; got != open {
		t.Errorf("port stat = %v, want %d", got, open)
	}
	if got := rc.stats["host"]; got != "127.0.0.1" {
		t.Errorf("host stat = %v, want the default filled in", got)
	}
	if got := rc.stats["state"]; got != "started" {
		t.Errorf("state stat = %v, want started", got)
	}
	if _, recorded := rc.stats["elapsed"]; !recorded {
		t.Error("no elapsed stat was recorded")
	}
}

// TestPort_EmitsNoInverse proves the run records nothing to undo.
//
// That absence is the whole contract for a method like this one, and it
// is worth an assertion rather than an assumption: an inverse recorded
// here would make a rollback engine perform work the forward run never
// did, which for a wait is any work at all.
func TestPort_EmitsNoInverse(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	_, open := portOpenListener(t)

	if _, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port": open,
	})); err != nil {
		t.Fatalf("Port: %v", err)
	}
	if inv, recorded := rc.stats["inverse"]; recorded {
		t.Errorf("an inverse was recorded (%v), want none: undoing a wait means doing nothing", inv)
	}
}

// TestPort_WaitsForAPortToOpen is the case the method exists for: the
// first probe says closed, and a later one says open.
//
// The listener is bound after the task has already started, so the change
// really happens mid-wait rather than before it. Asserting that the first
// probe found the port closed is what separates this from the test above:
// without it, a method that never polled at all would pass both.
func TestPort_WaitsForAPortToOpen(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)

	// Chosen before the wait starts and bound during it, so the probe sees
	// a real closed port first.
	target := portClosedPort(t)

	opened := make(chan struct{})
	go func() {
		time.Sleep(750 * time.Millisecond)
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(target)))
		if err != nil {
			close(opened)
			return
		}
		t.Cleanup(func() { _ = listener.Close() })
		close(opened)
	}()

	start := time.Now()
	result, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":    target,
		"sleep":   1,
		"timeout": 30,
	}))
	// Measured BEFORE the channel is drained, because draining it waits
	// for the goroutine's own sleep and would make any duration look long
	// enough. A first version of this test did it the other way round and
	// passed against a method that returned on its very first probe.
	waited := time.Since(start)
	<-opened

	if err != nil {
		t.Fatalf("Port: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true: waiting observes, it does not act")
	}
	if waited < 500*time.Millisecond {
		t.Errorf("the task returned after %s, so it never saw the port closed and polled again", waited)
	}
}

// TestPort_StoppedWaitsForThePortToClose proves the state parameter
// really inverts the condition.
//
// The listener starts open and is closed mid-wait, so a method that
// ignored state and waited for "open" would return immediately and fail
// the elapsed assertion below.
func TestPort_StoppedWaitsForThePortToClose(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	listener, open := portOpenListener(t)

	closed := make(chan struct{})
	go func() {
		time.Sleep(750 * time.Millisecond)
		_ = listener.Close()
		close(closed)
	}()

	start := time.Now()
	result, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":    open,
		"state":   "stopped",
		"sleep":   1,
		"timeout": 30,
	}))
	// Measured before the channel is drained, for the reason spelled out
	// in TestPort_WaitsForAPortToOpen: draining first hides a method that
	// returned on its first probe.
	waited := time.Since(start)
	<-closed

	if err != nil {
		t.Fatalf("Port: %v", err)
	}
	if result.Changed {
		t.Error("Changed = true: waiting observes, it does not act")
	}
	if waited < 500*time.Millisecond {
		t.Errorf("the task returned after %s, so it never saw the port open and polled again", waited)
	}
	if got := rc.stats["state"]; got != "stopped" {
		t.Errorf("state stat = %v, want stopped", got)
	}
}

// TestPort_HonorsTheDelay proves delay is really slept before the first
// check.
//
// The port is open from the start, so the ONLY thing that can make this
// task take a measurable amount of time is the delay. Two seconds rather
// than one, because one second is close enough to the noise of setting up
// an SSH connection to make a passing assertion unconvincing.
func TestPort_HonorsTheDelay(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	_, open := portOpenListener(t)

	start := time.Now()
	if _, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":  open,
		"delay": 2,
	})); err != nil {
		t.Fatalf("Port: %v", err)
	}

	if waited := time.Since(start); waited < 2*time.Second {
		t.Errorf("the task returned after %s, so the delay was not slept", waited)
	}
	if got, ok := rc.stats["elapsed"].(int); !ok || got < 2 {
		t.Errorf("elapsed stat = %v, want at least 2: the delay counts toward it", rc.stats["elapsed"])
	}
}

// TestPort_ProbesTheHostItWasGiven proves the host parameter reaches the
// probe rather than being replaced by the loopback default.
//
// It binds 127.0.0.2, a second loopback address, and leaves the same port
// closed on 127.0.0.1. A method that ignored host would probe the closed
// one and time out, so the success here can only come from the address
// the task named.
func TestPort_ProbesTheHostItWasGiven(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)

	listener, open := portOpenListenerOn(t, "127.0.0.2")
	// Bound on 127.0.0.1 too and released, which proves the same port
	// number really is free on the default address.
	if probe, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(open))); err == nil {
		_ = probe.Close()
	} else {
		t.Skipf("port %d is in use on 127.0.0.1, so this test could not tell the two addresses apart: %v", open, err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	if _, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":    open,
		"host":    "127.0.0.2",
		"timeout": 5,
	})); err != nil {
		t.Fatalf("Port: %v", err)
	}
	if got := rc.stats["host"]; got != "127.0.0.2" {
		t.Errorf("host stat = %v, want 127.0.0.2", got)
	}
}

// TestPort_UsesTheBashProber runs the whole method against a device whose
// only tool is bash, which exercises the /dev/tcp redirection and the
// outer shell that interprets its failure.
//
// Both answers are checked in one test on purpose: bash's redirection is
// the one prober whose "closed" and "cannot probe" cases look alike from
// the outside, so proving it reports a real closed port as closed matters
// as much as proving it reports an open one as open.
func TestPort_UsesTheBashProber(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "bash")
	_, open := portOpenListener(t)

	rc := newPortContext(server)
	if _, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":    open,
		"timeout": 10,
	})); err != nil {
		t.Fatalf("Port against an open port: %v", err)
	}

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    portClosedPort(t),
		"timeout": 1,
	}))
	if err == nil {
		t.Fatal("a closed port was reported as open by the bash prober")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want the ordinary timeout rather than a probe failure", err)
	}
}

// TestPort_PrefersPythonWhenBothToolsExist pins the preference order,
// which is a real decision rather than an accident of iteration.
//
// The bash stand-in exits 2, the status meaning "this tool cannot
// probe". If the method ever preferred bash, this task would fail with
// that message instead of succeeding, so the assertion is that the
// working prober was the one chosen.
func TestPort_PrefersPythonWhenBothToolsExist(t *testing.T) {
	server := startPortServer(t)
	dir := portOnlyTools(t, "python3")
	portWriteTool(t, dir, "bash", "#!/bin/sh\nexit 2\n")
	_, open := portOpenListener(t)

	if _, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    open,
		"timeout": 10,
	})); err != nil {
		t.Fatalf("Port: %v, want python3 to have been chosen over the broken bash", err)
	}
}

// TestPort_RefusesADeviceWithNoProbeTool proves the prerequisite is
// checked and named rather than assumed.
//
// This is the branch the whole "probe from the device" decision has to
// pay for, so the refusal has to say what is missing, that the check runs
// on the device, and what to do about it. A device with an empty PATH is
// exactly the minimal container this is about.
func TestPort_RefusesADeviceWithNoProbeTool(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t)
	_, open := portOpenListener(t)

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port": open,
	}))
	if err == nil {
		t.Fatal("a device with no probe tool was accepted, so something answered without being able to look")
	}
	for _, want := range []string{"python3", "bash", "from the device"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

// TestPort_ReportsABashThatCannotOpenTCP covers the failure this method's
// bash command exists to catch: a bash compiled without net redirections,
// which treats /dev/tcp as an ordinary filename and fails exactly like a
// refused connection.
//
// The stand-in prints what such a bash prints and exits 1. Getting this
// wrong is not a cosmetic problem: with state stopped, "closed" is
// success, so a device that could not probe at all would report the port
// released and let a runbook rebind something still in use.
func TestPort_ReportsABashThatCannotOpenTCP(t *testing.T) {
	server := startPortServer(t)
	dir := portOnlyTools(t)
	portWriteTool(t, dir, "bash", "#!/bin/sh\necho 'bash: /dev/tcp/127.0.0.1/80: No such file or directory' >&2\nexit 1\n")
	_, open := portOpenListener(t)

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    open,
		"state":   "stopped",
		"timeout": 5,
	}))
	if err == nil {
		t.Fatal("a bash that cannot open a TCP connection was believed, so a port that is open was reported closed")
	}
	if !strings.Contains(err.Error(), "cannot open a TCP connection") {
		t.Errorf("error = %q, want it to say the device's bash cannot probe", err)
	}
	if !strings.Contains(err.Error(), "No such file") {
		t.Errorf("error = %q, want it to carry what the device actually said", err)
	}
}

// TestPort_ReportsAProbeThatCouldNotRun covers the same status coming
// from the python prober, whose source exits 2 when the connection
// attempt raises rather than returning an errno. An address that will not
// resolve is the realistic shape of it.
func TestPort_ReportsAProbeThatCouldNotRun(t *testing.T) {
	server := startPortServer(t)
	dir := portOnlyTools(t)
	portWriteTool(t, dir, "python3", "#!/bin/sh\necho 'socket.gaierror: [Errno -2] Name or service not known' >&2\nexit 2\n")

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    80,
		"host":    "no-such-host.invalid",
		"timeout": 5,
	}))
	if err == nil {
		t.Fatal("a probe that could not run was reported as a closed port")
	}
	if !strings.Contains(err.Error(), "cannot open a TCP connection to no-such-host.invalid:80") {
		t.Errorf("error = %q, want it to name the tool and the address", err)
	}
	if !strings.Contains(err.Error(), "Name or service not known") {
		t.Errorf("error = %q, want it to carry what the device actually said", err)
	}
}

// TestPort_ReportsAnUnexpectedProbeStatus proves a status that is none of
// the three declared ones is an error rather than a vote.
//
// 127 is the realistic one: a shell reporting that the tool vanished
// between the check and the probe. Reading that as "closed" is how a
// state stopped wait quietly succeeds against a device it never checked.
func TestPort_ReportsAnUnexpectedProbeStatus(t *testing.T) {
	server := startPortServer(t)
	dir := portOnlyTools(t)
	portWriteTool(t, dir, "python3", "#!/bin/sh\nexit 127\n")

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    80,
		"state":   "stopped",
		"timeout": 5,
	}))
	if err == nil {
		t.Fatal("an unexpected exit status was read as an answer")
	}
	if !strings.Contains(err.Error(), "exited 127") {
		t.Errorf("error = %q, want it to name the status it got", err)
	}
	if !strings.Contains(err.Error(), "it printed nothing") {
		t.Errorf("error = %q, want it to say the probe printed nothing rather than trailing off", err)
	}
}

// TestPort_ReportsAMultiLineProbeFailureOnOneLine proves a python
// traceback is cut down to its first line.
//
// A task failure that spans six lines of somebody else's stack is
// unreadable in a runbook report, and the first line is the one naming
// what failed.
func TestPort_ReportsAMultiLineProbeFailureOnOneLine(t *testing.T) {
	server := startPortServer(t)
	dir := portOnlyTools(t)
	portWriteTool(t, dir, "python3", "#!/bin/sh\nprintf 'first line\\nsecond line\\n' >&2\nexit 2\n")

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    80,
		"timeout": 5,
	}))
	if err == nil {
		t.Fatal("expected the probe failure to be reported")
	}
	if !strings.Contains(err.Error(), "first line") {
		t.Errorf("error = %q, want the first line of what the device said", err)
	}
	if strings.Contains(err.Error(), "second line") {
		t.Errorf("error = %q, want only the first line", err)
	}
}

// TestPort_ReportsAToolCheckThatCouldNotBeSent covers the branch where
// the connection authenticates and then cannot carry the tool check.
//
// A session budget of zero is what that looks like from this side, and it
// is a real protocol-level refusal rather than an injected Go error, so
// the branch sees the shape a device under session pressure produces.
// Treating it as "no tools found" would blame the device for the wrong
// thing and send somebody to install python3 on a machine that already
// has it.
func TestPort_ReportsAToolCheckThatCouldNotBeSent(t *testing.T) {
	server := startPortServerWithSessionBudget(t, 0)
	portOnlyTools(t, "python3")

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port": 80,
	}))
	if err == nil {
		t.Fatal("a failed tool check was reported as success")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
	if strings.Contains(err.Error(), "has none of") {
		t.Errorf("error = %q, want a transport failure not to be reported as a device missing its tools", err)
	}
}

// TestPort_ReportsAProbeThatCouldNotBeSent covers the same question one
// step later: the tool check gets its session and the first probe does
// not.
//
// A budget of one is the only way to reach it, since the check and the
// probe are separate sessions. The failure must not be read as a closed
// port, which for a state stopped wait would be a silent pass.
func TestPort_ReportsAProbeThatCouldNotBeSent(t *testing.T) {
	server := startPortServerWithSessionBudget(t, 1)
	portOnlyTools(t, "python3")

	_, err := wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    80,
		"state":   "stopped",
		"timeout": 30,
	}))
	if err == nil {
		t.Fatal("a probe that was never sent was read as a closed port")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a transport failure not to be reported as a timeout", err)
	}
}

// TestPort_TimesOutDuringTheDelay proves the delay is inside the timeout
// budget rather than beside it, which is how Ansible's wait_for measures
// it: its start time is taken before the delay is slept.
//
// A delay longer than the timeout is a runbook mistake, and the honest
// outcome is that the task fails on its timeout without ever probing. A
// method that slept the delay outside the budget would instead wait a
// full thirty seconds here.
func TestPort_TimesOutDuringTheDelay(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	_, open := portOpenListener(t)

	start := time.Now()
	_, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":    open,
		"delay":   30,
		"timeout": 2,
	}))
	if err == nil {
		t.Fatal("a delay longer than the timeout was reported as success")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to say the wait expired", err)
	}
	if waited := time.Since(start); waited > 15*time.Second {
		t.Errorf("the task ran for %s, so the delay was slept outside the timeout budget", waited)
	}
}

// TestPort_TimesOutWhileAProbeIsRunning covers the deadline expiring with
// a probe still in flight, which is what a filtered port produces: the
// connection attempt neither succeeds nor is refused, it just hangs.
//
// The stand-in sleeps rather than answering, which is that hang. What
// matters is the message: the raw failure at that moment is a session
// closing, and reporting THAT would tell an operator their SSH broke when
// what really happened is that the wait expired.
func TestPort_TimesOutWhileAProbeIsRunning(t *testing.T) {
	server := startPortServer(t)

	// Resolved BEFORE PATH is narrowed, since afterwards there is nothing
	// on it to resolve. The absolute path is then what the stand-in runs.
	sleeper, err := exec.LookPath("sleep")
	if err != nil {
		t.Skipf("this machine has no sleep command, so a hanging probe cannot be simulated: %v", err)
	}

	dir := portOnlyTools(t)
	portWriteTool(t, dir, "python3", fmt.Sprintf("#!/bin/sh\nexec %s 3\n", sleeper))

	start := time.Now()
	_, err = wait.Port(context.Background(), newPortContext(server), newPortTarget(server), portParams(map[string]any{
		"port":    80,
		"timeout": 1,
	}))
	if err == nil {
		t.Fatal("a probe that never answered was reported as success")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want the expired wait rather than the session failure it looks like", err)
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the task ran for %s with a 1 second timeout, so a hanging probe outlived the deadline", waited)
	}
}

// TestPort_TimesOutWhenThePortNeverOpens proves the timeout is real and
// that running out is an error rather than a quiet pass.
//
// The stats are checked too: elapsed is the number an operator wants most
// from a failed wait, and Ansible's wait_for returns it on failure for the
// same reason.
func TestPort_TimesOutWhenThePortNeverOpens(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	closed := portClosedPort(t)

	start := time.Now()
	_, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":    closed,
		"timeout": 2,
		"sleep":   1,
	}))
	if err == nil {
		t.Fatal("a port that never opened was reported as open")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want it to say the wait expired", err)
	}
	if !strings.Contains(err.Error(), "to be started") {
		t.Errorf("error = %q, want it to say what it was waiting for", err)
	}
	if waited := time.Since(start); waited > 10*time.Second {
		t.Errorf("the task ran for %s with a 2 second timeout, so the deadline did not bound it", waited)
	}
	if got, ok := rc.stats["elapsed"].(int); !ok || got < 1 {
		t.Errorf("elapsed stat = %v, want the time a failed wait held recorded too", rc.stats["elapsed"])
	}
	if got := rc.stats["port"]; got != closed {
		t.Errorf("port stat = %v, want %d recorded on failure too", got, closed)
	}
}

// TestPort_ReportsACanceledContextAsCancellation proves a caller's own
// cancellation is not dressed up as a timeout.
//
// The distinction matters to whoever reads the failure: a timeout sends
// somebody to look at the device for a service that never appeared, and a
// cancellation means the service was never given the chance.
func TestPort_ReportsACanceledContextAsCancellation(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(600 * time.Millisecond)
		cancel()
	}()
	defer cancel()

	_, err := wait.Port(ctx, rc, newPortTarget(server), portParams(map[string]any{
		"port":    portClosedPort(t),
		"timeout": 60,
		"sleep":   2,
	}))
	if err == nil {
		t.Fatal("a canceled wait was reported as success")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %q, want it to carry context.Canceled", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a cancellation not to be reported as a timeout", err)
	}
}

// TestPort_StatRecordFailureIsReported proves a failure to record the
// result is never swallowed.
//
// A task whose stats never landed has not really succeeded: a later task's
// condition reading elapsed or port would find nothing and behave as
// though the wait never happened.
func TestPort_StatRecordFailureIsReported(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	rc.failOn = "port"
	_, open := portOpenListener(t)

	_, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port": open,
	}))
	if err == nil {
		t.Fatal("a failure to record the result was swallowed")
	}
	if !errors.Is(err, errPortStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
}

// TestPort_StatRecordFailureOnTimeoutIsReported covers the same question
// on the failure path, which the test above can never reach.
//
// The timeout error would otherwise hide a recording failure behind a
// message about the device, which is the wrong thing to go and look at.
func TestPort_StatRecordFailureOnTimeoutIsReported(t *testing.T) {
	server := startPortServer(t)
	portOnlyTools(t, "python3")
	rc := newPortContext(server)
	rc.failOn = "elapsed"

	_, err := wait.Port(context.Background(), rc, newPortTarget(server), portParams(map[string]any{
		"port":    portClosedPort(t),
		"timeout": 1,
	}))
	if err == nil {
		t.Fatal("a failure to record the result was swallowed")
	}
	if !errors.Is(err, errPortStat) {
		t.Errorf("error = %q, want it to carry what recording returned rather than only the timeout", err)
	}
}
