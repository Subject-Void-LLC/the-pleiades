package block_test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// These tests run file.block.set and file.block.remove against a REAL
// SSH server, in this process, that hands every command it receives to a
// real /bin/sh on this machine (pkg/remoteexec/remoteexectest). Nothing
// about the transport, the shell, stat, chmod, mktemp or the atomic
// rename is stubbed.
//
// That matters twice over. RULE 0 says a test only counts when it runs
// the path the platform runs, and these methods are almost entirely
// about what a real file looks like after a real rewrite. And every
// assertion below reads the FILESYSTEM back with os.ReadFile rather than
// believing what the method reported about itself: a method that
// returned the right Changed flag and the right stats while writing the
// wrong bytes would pass a test that only read its answer, and that is
// precisely the failure this namespace has to be proof against.
//
// Every helper here is prefixed "block" because both methods share this
// one Go package and Go has no file-level scope, so a helper named
// "server" would collide with the sibling file's idea of one.

// The two marker lines the defaults produce, written out rather than
// computed, so a test compares the method's output against text a person
// wrote rather than against the same expression the method used.
const (
	blockTestBegin = "# BEGIN ANSIBLE MANAGED BLOCK"
	blockTestEnd   = "# END ANSIBLE MANAGED BLOCK"
)

// errBlockStat is what a stub context returns when a test wants
// recording to fail, so the branches where the file changed and the
// record did not are reachable.
var errBlockStat = errors.New("recording the result failed")

// blockServer is where the in-process SSH harness is listening, plus the
// one credential it accepts.
type blockServer struct {
	host     string
	port     int
	username string
	password string
}

// startBlockServer starts a real SSH server that runs every command
// through a real /bin/sh, and stops it when the test ends.
func startBlockServer(t *testing.T) blockServer {
	return startBlockServerWithSessionBudget(t, -1)
}

// startBlockServerWithSessionBudget is startBlockServer with a cap on
// how many session channels it accepts before refusing every further
// one.
//
// It is how the failure branches of a multi-command method are reached.
// A changing run of either method opens six sessions in a fixed order:
// the stat that decides what is at the path, the read of its contents,
// the write, the stat that reads back what the write left, the chmod
// that puts the old mode back, and the read that observes the result. A
// budget of n lets the first n succeed and refuses the next, which is a
// real protocol-level refusal rather than an injected Go error, so the
// branch under test sees the shape a device under session pressure
// produces.
func startBlockServerWithSessionBudget(t *testing.T, budget int) blockServer {
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

	return blockServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// blockTarget is a device reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately
// does not provide.
type blockTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *blockTarget) SSHHost() string { return d.host }
func (d *blockTarget) SSHPort() int    { return d.port }

// newBlockStub builds the base inventory item both device shapes wrap.
func newBlockStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "block-1",
		StubName:  "block-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newBlockTarget builds the SSH-reachable device a test runs against.
func newBlockTarget(server blockServer) *blockTarget {
	return &blockTarget{Stub: newBlockStub(), host: server.host, port: server.port}
}

// newBlockUnreachable builds a device these methods cannot reach at all:
// the bare stub, which implements InventoryItem and nothing else.
func newBlockUnreachable() inventory.InventoryItem {
	return newBlockStub()
}

// blockContext is a minimal sdk.RunbookContext carrying a fixed secret
// set, standing in for the real one the composition root builds from the
// credential store on the Crawl tier or the dispatch payload on the Walk
// tier.
type blockContext struct {
	secrets map[string]string
	stats   map[string]any

	// failOn, when set, makes SetStat fail for that one key. One key
	// rather than all of them because each method records at three
	// separate call sites, the diff, the returned stats and the emitted
	// inverse, and a context that failed on everything could only ever
	// reach the first of them.
	failOn string
}

func newBlockContext(server blockServer) *blockContext {
	return &blockContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *blockContext) InjectSecrets() map[string]string { return c.secrets }

func (c *blockContext) SetStat(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errBlockStat
	}
	c.stats[key] = value
	return nil
}

func (c *blockContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// blockParams builds a task's params with the host key check turned off,
// which every test against the in-process harness needs, and with
// whatever else the test names merged in.
func blockParams(path string, extra map[string]any) map[string]any {
	params := map[string]any{
		"path":                          path,
		"insecure_skip_host_key_verify": true,
	}
	for key, value := range extra {
		params[key] = value
	}
	return params
}

// blockDiffHalf returns one half of the recorded diff stat, failing the
// test when the method recorded nothing or recorded a shape a rollback
// engine could not read.
func blockDiffHalf(t *testing.T, rc *blockContext, half string) map[string]any {
	t.Helper()

	raw, ok := rc.stats["diff"]
	if !ok {
		t.Fatal("no diff stat was recorded, so nothing captured the prior state")
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

// blockInverse returns the emitted inverse, failing the test when none
// was recorded.
func blockInverse(t *testing.T, rc *blockContext) (fqcn string, params map[string]any, description string) {
	t.Helper()

	raw, ok := rc.stats["inverse"]
	if !ok {
		t.Fatal("no inverse was emitted, so nothing recorded how to undo this run")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("inverse stat is %T, want a map", raw)
	}
	fqcn, _ = record["fqcn"].(string)
	params, _ = record["params"].(map[string]any)
	description, _ = record["description"].(string)
	if params == nil {
		t.Fatalf("the inverse carries no params map, got %#v", record)
	}
	return fqcn, params, description
}

// blockNoInverse fails the test when an inverse was emitted.
//
// Its absence is the assertion, not an oversight: a run that changed
// nothing must emit nothing, because an inverse recorded for it would
// make a rollback perform work the forward run never did.
func blockNoInverse(t *testing.T, rc *blockContext) {
	t.Helper()

	if raw, ok := rc.stats["inverse"]; ok {
		t.Errorf("a run that changed nothing emitted the inverse %#v, so a rollback would undo work that never happened", raw)
	}
}

// blockFile writes a file with exactly the contents given, at a mode
// that is deliberately not the 0600 a rewrite would leave behind.
//
// os.WriteFile's own permission argument is masked by the umask, so the
// explicit chmod is what makes the starting mode of every test a known
// one, and 0644 is what proves the method puts the mode back: a file
// written through mktemp and renamed into place arrives as 0600.
func blockFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// blockContents reads a file straight from the filesystem, which is what
// every assertion about what a method wrote compares against.
func blockContents(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// blockMode reads a path's permission bits from the filesystem, as four
// octal digits.
func blockMode(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return "0" + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
}
