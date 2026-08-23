package line_test

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// These tests run file.line.set and file.line.remove against a REAL SSH
// server, in this process, that hands every command it receives to a real
// /bin/sh on this machine (pkg/remoteexec/remoteexectest). Nothing about
// the transport, the shell, stat, cat, mktemp, mv or chmod is stubbed.
//
// That matters twice over. RULE 0 says a test only counts when it runs the
// path the platform runs, and these methods are almost entirely about what
// a real read returns and what a real atomic write leaves behind. And every
// assertion below reads the FILESYSTEM back with os.ReadFile or os.Lstat
// rather than believing the method's own report: a module that returned the
// right Changed flag while writing the wrong bytes would sail through a test
// that only read its answer, and that is the exact failure this whole
// namespace has to be proof against.
//
// Everything shared lives here, prefixed "line", because the two methods'
// tests are separate files sharing one Go package and Go has no file level
// scope.

// errLineStat is what a stub context returns when a test wants recording to
// fail, so the branches where the change landed and the record did not are
// reachable.
var errLineStat = errors.New("recording the result failed")

// lineServer is where the in-process SSH harness is listening, plus the one
// credential it accepts.
type lineServer struct {
	host     string
	port     int
	username string
	password string
}

// startLineServer starts a real SSH server that runs every command through a
// real /bin/sh, and stops it when the test ends.
func startLineServer(t *testing.T) lineServer {
	return startLineServerWithSessionBudget(t, -1)
}

// startLineServerWithSessionBudget is startLineServer with a cap on how many
// session channels it accepts before refusing.
//
// It is how every transport failure branch in these methods is reached, and
// it reaches them the honest way: a real protocol level refusal, which is
// what a device under session pressure actually looks like from this side,
// rather than an error injected into a fake. Each command a method sends
// costs one session, so a budget is really a count of "let the first N
// commands through, then fail". The counts each test picks are written out
// in that test's own comment, because they are the only thing tying a budget
// to the command it is meant to cut off.
func startLineServerWithSessionBudget(t *testing.T, budget int) lineServer {
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

	return lineServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// lineTarget is a device reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately does
// not provide.
type lineTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *lineTarget) SSHHost() string { return d.host }
func (d *lineTarget) SSHPort() int    { return d.port }

// newLineStub builds the base inventory item both device shapes wrap.
func newLineStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "line-1",
		StubName:  "line-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newLineTarget builds the SSH-reachable device a test runs against.
func newLineTarget(server lineServer) *lineTarget {
	return &lineTarget{Stub: newLineStub(), host: server.host, port: server.port}
}

// newLineUnreachable builds a device these methods cannot reach at all: the
// bare stub, which implements InventoryItem and nothing else.
func newLineUnreachable() inventory.InventoryItem {
	return newLineStub()
}

// lineContext is a minimal sdk.RunbookContext carrying a fixed secret set,
// standing in for the real one the composition root builds from the
// credential store on the Crawl tier or the dispatch payload on the Walk
// tier.
type lineContext struct {
	secrets map[string]string
	stats   map[string]any

	// failOn, when set, makes SetStat fail for that one key. One key rather
	// than all of them because these methods record from three separate call
	// sites, the diff, then the inverse, then the returned stats, and a
	// context that failed on everything could only ever reach the first.
	failOn string
}

func newLineContext(server lineServer) *lineContext {
	return &lineContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *lineContext) InjectSecrets() map[string]string { return c.secrets }

func (c *lineContext) SetStat(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errLineStat
	}
	c.stats[key] = value
	return nil
}

func (c *lineContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// lineParams builds a task's params with host key verification skipped,
// since the in-process harness generates a fresh host key every time and
// there is nothing to have trusted it earlier.
func lineParams(extra map[string]any) map[string]any {
	params := map[string]any{"insecure_skip_host_key_verify": true}
	for key, value := range extra {
		params[key] = value
	}
	return params
}

// lineWriteFile creates a file holding exactly content, with exactly mode,
// and returns its path.
//
// The explicit chmod is not redundant: os.WriteFile's permission argument is
// masked by the process umask, so a file asked for as 0640 can arrive as
// something else, and every test below that checks permissions needs its
// starting state to be a known one rather than an inherited one.
func lineWriteFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "target.conf")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// lineOnDisk returns the file's bytes read straight from the filesystem,
// which is the only thing any of these tests trusts about what a method did.
func lineOnDisk(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path) // #nosec G304 -- the path is one this test just created under t.TempDir.
	if err != nil {
		t.Fatalf("reading %s back: %v", path, err)
	}
	return string(content)
}

// lineModeOnDisk reads a path's permission bits straight from the
// filesystem, as four octal digits, which is the form remotefile reports.
func lineModeOnDisk(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return "0" + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
}

// lineGroupOnDisk returns the group NAME owning a path, which is the form
// these methods compare against and the form a runbook writes.
func lineGroupOnDisk(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	_, gid, ok := posixOwnerIDs(info)
	if !ok {
		t.Skip("this platform does not report POSIX owner and group ids")
	}
	group, err := user.LookupGroupId(strconv.Itoa(gid))
	if err != nil {
		t.Skipf("gid %d has no name on this machine: %v", gid, err)
	}
	return group.Name
}

// lineHandToAnotherGroup gives path to some group this process may use that
// is not the one it already has, and returns that group's name, or "" when
// there is none available.
//
// A real group change needs a real second group, and which ones are on offer
// depends on who is running the tests: root may use any, and anybody else may
// use a group they belong to. Both are searched so the test that proves an
// atomic write does not throw the group away runs in as many environments as
// possible rather than only under root.
func lineHandToAnotherGroup(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	_, current, ok := posixOwnerIDs(info)
	if !ok {
		return ""
	}

	candidates, err := os.Getgroups()
	if err != nil {
		t.Fatalf("reading this process's groups: %v", err)
	}
	if os.Geteuid() == 0 {
		// root's supplementary set is usually just gid 0, and root may chgrp to
		// anything, so widen the search past it.
		for gid := 0; gid < 100; gid++ {
			candidates = append(candidates, gid)
		}
	}

	for _, gid := range candidates {
		if gid == current {
			continue
		}
		group, err := user.LookupGroupId(strconv.Itoa(gid))
		if err != nil {
			continue
		}
		if err := os.Chown(path, -1, gid); err != nil {
			continue
		}
		return group.Name
	}
	return ""
}

// lineDiffHalf returns one half of the recorded diff stat, failing the test
// when the method recorded nothing or recorded a shape a rollback engine
// could not read.
func lineDiffHalf(t *testing.T, rc *lineContext, half string) map[string]any {
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

// lineInverse returns the emitted inverse, failing the test when none was
// emitted or when it is not the runnable shape a rollback engine reads.
func lineInverse(t *testing.T, rc *lineContext) map[string]any {
	t.Helper()

	raw, ok := rc.stats["inverse"]
	if !ok {
		t.Fatal("no inverse was emitted, so nothing recorded how to undo this run")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("inverse stat is %T, want a map", raw)
	}
	return record
}

// lineInverseParams returns the already resolved params of the emitted
// inverse, which is what a rollback engine would hand straight to the FQCN
// the inverse names.
func lineInverseParams(t *testing.T, rc *lineContext) map[string]any {
	t.Helper()

	params, ok := lineInverse(t, rc)["params"].(map[string]any)
	if !ok {
		t.Fatalf("the inverse carries no params map, got %#v", lineInverse(t, rc))
	}
	return params
}
