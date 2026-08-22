package wait_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// The harness both wait methods' tests run against: a REAL SSH server, in
// this process, that hands every command it receives to a real /bin/sh on
// this machine (pkg/remoteexec/remoteexectest). Nothing about the
// transport, the shell, stat or cat is stubbed.
//
// RULE 0 is why. These methods are almost entirely about what a real
// stat and a real cat report over time, so a harness that answered with
// canned bytes would only prove the canned bytes were canned. Every
// assertion about the device reads the FILESYSTEM back with os.Stat and
// os.ReadFile rather than believing what the method recorded about
// itself.
//
// It lives in its own file, shared by both methods' tests, because
// wait.path and wait.search are the whole of this package and both need
// every piece of it. Every identifier here carries a wait prefix, since
// sibling files share one Go package and Go has no file-level scope.

// errWaitStat is what a stub context returns when a test wants recording
// to fail, so the branches where the wait succeeded and the record did
// not are reachable.
var errWaitStat = errors.New("recording the result failed")

// waitServer is where the in-process SSH harness is listening, plus the
// one credential it accepts.
type waitServer struct {
	host     string
	port     int
	username string
	password string
}

// startWaitServer starts a real SSH server that runs every command
// through a real /bin/sh, and stops it when the test ends.
func startWaitServer(t *testing.T) waitServer {
	return startWaitServerWithSessionBudget(t, -1)
}

// startWaitServerWithSessionBudget is startWaitServer with a cap on how
// many session channels it accepts before refusing.
//
// A budget of zero is the only way to reach the branch where the
// connection authenticates and then cannot carry the first probe, which
// is what a device under session pressure looks like from this side.
func startWaitServerWithSessionBudget(t *testing.T, budget int) waitServer {
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

	return waitServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// waitTarget is a device reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately
// does not provide.
type waitTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *waitTarget) SSHHost() string { return d.host }
func (d *waitTarget) SSHPort() int    { return d.port }

// newWaitStub builds the base inventory item both device shapes wrap.
func newWaitStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "wait-1",
		StubName:  "wait-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newWaitTarget builds the SSH-reachable device a test runs against.
func newWaitTarget(server waitServer) *waitTarget {
	return &waitTarget{Stub: newWaitStub(), host: server.host, port: server.port}
}

// newWaitUnreachable builds a device neither method can reach at all:
// the bare stub, which implements InventoryItem and nothing else.
func newWaitUnreachable() inventory.InventoryItem {
	return newWaitStub()
}

// waitContext is a minimal sdk.RunbookContext carrying a fixed secret
// set, standing in for the real one the composition root builds from the
// credential store on the Crawl tier or the dispatch payload on the Walk
// tier.
type waitContext struct {
	secrets map[string]string
	stats   map[string]any

	// failOn, when set, makes SetStat fail for that one key. One key
	// rather than all of them because these methods record under several
	// separate call sites, and a context that failed on everything could
	// only ever reach the first of them.
	failOn string
}

func newWaitContext(server waitServer) *waitContext {
	return &waitContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *waitContext) InjectSecrets() map[string]string { return c.secrets }

func (c *waitContext) SetStat(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errWaitStat
	}
	c.stats[key] = value
	return nil
}

func (c *waitContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// waitDiffHalf returns one half of the recorded diff stat, failing the
// test when the method recorded nothing or recorded a shape a journal
// could not read.
func waitDiffHalf(t *testing.T, rc *waitContext, half string) map[string]any {
	t.Helper()

	raw, ok := rc.stats["diff"]
	if !ok {
		t.Fatal("no diff stat was recorded, so nothing can tell this observation from a task that never ran")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("diff stat is %T, want a map", raw)
	}
	side, ok := record[half].(map[string]any)
	if !ok {
		t.Fatalf("diff stat has no %q map, got %#v", half, record)
	}
	return side
}

// waitElapsed returns the elapsed stat as the whole number of seconds it
// is documented to be, failing the test when it is missing or is some
// other type.
func waitElapsed(t *testing.T, rc *waitContext) int {
	t.Helper()

	raw, ok := rc.stats["elapsed"]
	if !ok {
		t.Fatal("no elapsed stat was recorded")
	}
	seconds, ok := raw.(int)
	if !ok {
		t.Fatalf("elapsed stat is %T, want an int of whole seconds", raw)
	}
	return seconds
}

// waitMissingPath returns a path inside a fresh temporary directory that
// nothing has created, for a test about something appearing.
func waitMissingPath(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "watched")
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("os.Lstat(%s) = %v, want the path absent before the test starts", path, err)
	}
	return path
}

// waitExistingFile writes a file with the given contents and returns its
// path.
func waitExistingFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "watched")
	waitWrite(t, path, contents)
	return path
}

// waitWrite writes contents to path, failing the test rather than the
// method when the local filesystem refuses.
func waitWrite(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// waitAfter runs change once, after the given delay, on its own
// goroutine, and makes the test wait for it however the test ends.
//
// This is what proves a method really waited. A test whose condition was
// already true when the call started could pass against a method that
// polled once and never slept, which is the one thing these methods
// exist to do.
func waitAfter(t *testing.T, delay time.Duration, change func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(delay)
		change()
	}()
	// Joined at the end of the test rather than left running: a goroutine
	// still writing into a t.TempDir() that the framework is removing
	// produces a failure in whichever test runs next, which is the
	// hardest kind to read.
	t.Cleanup(func() { <-done })
}
