package exec_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// This file is the harness the tests in this package run against: a real
// SSH server, in this process, that hands every command it receives to a
// real POSIX shell on this machine.
//
// Neither half of that is a mock. The SSH side is
// golang.org/x/crypto/ssh doing a genuine key exchange, a genuine
// authentication round and a genuine session channel, and the shell side
// is /bin/sh, so "test -e", "cd", an exit status and a piped stdin all
// behave exactly as they do on a device. That matters under RULE 0: the
// subject of these tests is what exec.command builds and how it reads
// the answer, and a server that returned canned bytes would only prove
// the canned bytes were canned.
//
// What it is NOT is a remote machine. exec_release_gate_test.go covers
// that, against a real, independent sshd in a container reached over a
// real network hop, which is what actually flips this method's status.

// testSSHServer is this package's thin alias over the shared harness in
// pkg/remoteexec/remoteexectest, kept so the tests written against the
// old local server read unchanged.
//
// The server itself moved out of this file when a second package needed
// it. Those tests passing unchanged afterward is the proof the move
// preserved behavior, which is the same evidence the pkg/remoteexec
// extraction was held to.
type testSSHServer struct {
	host     string
	port     int
	username string
	password string
}

// startShellSSHServer starts a real SSH server that runs every command
// through a real /bin/sh, and stops it when the test ends.
func startShellSSHServer(t *testing.T) testSSHServer {
	return startShellSSHServerWithSessionBudget(t, -1)
}

// startShellSSHServerWithSessionBudget is startShellSSHServer with a cap
// on how many session channels it accepts before refusing, which is how
// a test reaches the branches that only a connection failing partway
// through a task can produce.
func startShellSSHServerWithSessionBudget(t *testing.T, budget int) testSSHServer {
	t.Helper()

	// A negative budget is this package's own spelling of "unlimited",
	// and the harness spells that as an absent limit.
	opts := remoteexectest.Options{}
	if budget >= 0 {
		opts.SessionLimit = remoteexectest.Limit(budget)
	}

	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	return testSSHServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// sshDevice is a target reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately
// does not provide.
type sshDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *sshDevice) SSHHost() string { return d.host }
func (d *sshDevice) SSHPort() int    { return d.port }

// execDevice adds the capability.CommandExecCapable accessor, so it is
// the shape a real linux.Server presents.
type execDevice struct {
	sshDevice
	workingDir string
}

func (d *execDevice) WorkingDirectory() string { return d.workingDir }

// newStub builds the base inventory item both device shapes wrap.
func newStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "exec-1",
		StubName:  "exec-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NameCommandExec},
		StubState: inventory.StateActive,
	}
}

// newDevice builds the target a test runs against: SSH-reachable, and
// declaring workingDir through CommandExecCapable.
func newDevice(server testSSHServer, workingDir string) *execDevice {
	return &execDevice{
		sshDevice:  sshDevice{Stub: newStub(), host: server.host, port: server.port},
		workingDir: workingDir,
	}
}

// shellDevice adds the capability.ShellExecCapable accessor on top of
// execDevice, which is the shape a real linux.Server presents: it
// declares ShellExecCapable, and that resolves upward to satisfy
// CommandExecCapable too, so one type carries both accessors.
type shellDevice struct {
	execDevice
	shell string
}

func (d *shellDevice) ShellPath() string { return d.shell }

// newShellDevice builds a target declaring shell through
// ShellExecCapable, so a test can prove exec.shell reads the device's
// answer rather than always using its own default.
func newShellDevice(server testSSHServer, shell string) *shellDevice {
	return &shellDevice{
		execDevice: execDevice{sshDevice: sshDevice{Stub: newStub(), host: server.host, port: server.port}},
		shell:      shell,
	}
}

// newSSHOnlyDevice builds a target that is reachable over SSH and
// implements no CommandExecCapable accessor at all.
//
// That is the Runner's own device adapter exactly: it carries a declared
// capability list without the accessors behind it, so a method that
// type-asserted CommandExecCapable rather than reading it optionally
// would refuse a dispatch the Controller correctly admitted.
func newSSHOnlyDevice(server testSSHServer) *sshDevice {
	return &sshDevice{Stub: newStub(), host: server.host, port: server.port}
}

// newUnreachableDevice builds a target this method cannot reach at all:
// the bare stub, which implements InventoryItem and nothing else.
func newUnreachableDevice() inventory.InventoryItem {
	return newStub()
}

// stubContext is a minimal sdk.RunbookContext carrying a fixed secret
// set, standing in for the real one the composition root builds from the
// credential store (Walk tier) or the dispatch payload (Crawl tier).
type stubContext struct {
	secrets map[string]string
	stats   map[string]any

	// statErr, when set, makes SetStat fail, so a test can drive the
	// branch where the command succeeded and recording its answer did
	// not.
	statErr error
}

func newStubContext(server testSSHServer) *stubContext {
	return &stubContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *stubContext) InjectSecrets() map[string]string { return c.secrets }

func (c *stubContext) SetStat(key string, value any) error {
	if c.statErr != nil {
		return c.statErr
	}
	if c.stats == nil {
		c.stats = map[string]any{}
	}
	c.stats[key] = value
	return nil
}

func (c *stubContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }
