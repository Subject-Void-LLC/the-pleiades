package file_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// These tests run file.copy against a REAL SSH server, in this process,
// that hands every command it receives to a real /bin/sh on this machine
// (pkg/remoteexec/remoteexectest). Nothing about the transport, the
// shell, stat, sha256sum, mktemp, mv or chmod is stubbed.
//
// RULE 0 is why: this method's entire subject is what a real checksum
// reports, what a real atomic rename leaves behind and what a real chmod
// then does about it, and a stand-in returning canned bytes would only
// prove the bytes were canned. Every assertion below reads the FILESYSTEM
// with os.ReadFile and os.Lstat rather than believing the method's own
// report, because a module that returned the right Changed flag while
// writing nothing would pass a test that only read its answer.
//
// Every helper here is prefixed "copy" because the sibling methods in
// this package are being written alongside it and share one test package.
// The shared harness belongs in a common file the way
// internal/catalog/exec/sshd_test.go holds that namespace's, and moving
// these there is a merge step rather than a rewrite.

// errCopyStat is what a stub context returns when a test wants recording
// to fail, so the branches where the change landed and the record did not
// are reachable.
var errCopyStat = errors.New("recording the result failed")

// copyServer is where the in-process SSH harness is listening, plus the
// one credential it accepts.
type copyServer struct {
	host     string
	port     int
	username string
	password string
}

// startCopyServer starts a real SSH server that runs every command
// through a real /bin/sh, and stops it when the test ends.
func startCopyServer(t *testing.T) copyServer {
	return startCopyServerWithSessionBudget(t, -1)
}

// startCopyServerWithSessionBudget is startCopyServer with a cap on how
// many session channels it accepts before refusing.
//
// It is the only way to reach the failure branches that happen PARTWAY
// through this method. A copy opens as many as seven sessions (read the
// path, read its checksum, write, read the path back, apply attributes,
// read the path again, read its checksum again), and each budget below
// stops the run at a different one of them, which is a real
// protocol-level refusal rather than an injected Go error.
func startCopyServerWithSessionBudget(t *testing.T, budget int) copyServer {
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

	return copyServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// copyTarget is a device reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately
// does not provide.
type copyTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *copyTarget) SSHHost() string { return d.host }
func (d *copyTarget) SSHPort() int    { return d.port }

// newCopyStub builds the base inventory item both device shapes wrap.
func newCopyStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "file-copy-1",
		StubName:  "file-copy-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newCopyTarget builds the SSH-reachable device a test runs against.
func newCopyTarget(server copyServer) *copyTarget {
	return &copyTarget{Stub: newCopyStub(), host: server.host, port: server.port}
}

// newCopyUnreachable builds a device this method cannot reach at all: the
// bare stub, which implements InventoryItem and nothing else.
func newCopyUnreachable() inventory.InventoryItem {
	return newCopyStub()
}

// copyContext is a minimal sdk.RunbookContext carrying a fixed secret
// set, standing in for the real one the composition root builds from the
// credential store on the Crawl tier or the dispatch payload on the Walk
// tier.
type copyContext struct {
	secrets map[string]string
	stats   map[string]any

	// failOn, when set, makes SetStat fail for that one key. One key
	// rather than all of them because this method records at three
	// separate call sites, the diff, then the inverse, then the returned
	// stats, and a context that failed on everything could only ever reach
	// the first of them.
	failOn string
}

func newCopyContext(server copyServer) *copyContext {
	return &copyContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *copyContext) InjectSecrets() map[string]string { return c.secrets }

func (c *copyContext) SetStat(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errCopyStat
	}
	c.stats[key] = value
	return nil
}

func (c *copyContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// copyParams builds a task's params with host key verification skipped,
// which every test against the in-process harness needs.
func copyParams(dest, content string) map[string]any {
	return map[string]any{
		"dest":                          dest,
		"content":                       content,
		"insecure_skip_host_key_verify": true,
	}
}

// copyDiffHalf returns one half of the recorded diff stat, failing the
// test when the method recorded nothing or recorded a shape a rollback
// engine could not read.
func copyDiffHalf(t *testing.T, rc *copyContext, half string) map[string]any {
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

// copyRecordedInverse returns the emitted inverse, failing the test when
// none was recorded.
func copyRecordedInverse(t *testing.T, rc *copyContext) map[string]any {
	t.Helper()

	raw, ok := rc.stats["inverse"]
	if !ok {
		t.Fatal("no inverse was emitted, which tells a rollback engine that undoing this run means doing nothing")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("inverse stat is %T, want a map", raw)
	}
	return record
}

// copyInverseParams returns the emitted inverse's already-resolved
// parameters, which are what a rollback engine would dispatch.
func copyInverseParams(t *testing.T, rc *copyContext) map[string]any {
	t.Helper()

	params, ok := copyRecordedInverse(t, rc)["params"].(map[string]any)
	if !ok {
		t.Fatalf("the inverse carries no params map, got %#v", copyRecordedInverse(t, rc))
	}
	return params
}

// copyFileMode reads a path's permission bits straight from the
// filesystem, as four octal digits, which is the form the module reports.
func copyFileMode(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return "0" + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
}

// copyFileBytes reads a path's contents straight from the filesystem.
func copyFileBytes(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path) // #nosec G304 -- a path this test itself created under t.TempDir
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(content)
}

// copyFileNames returns the owner and group NAMES of a path, which is
// what the module compares against and what a runbook writes.
func copyFileNames(t *testing.T, path string) (string, string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	uid, gid, ok := posixOwnerIDs(info)
	if !ok {
		t.Skip("this platform does not report POSIX owner and group ids")
	}
	owner, err := user.LookupId(strconv.Itoa(uid))
	if err != nil {
		t.Skipf("uid %d has no name on this machine: %v", uid, err)
	}
	group, err := user.LookupGroupId(strconv.Itoa(gid))
	if err != nil {
		t.Skipf("gid %d has no name on this machine: %v", gid, err)
	}
	return owner.Username, group.Name
}

// copyOtherGroup names a group this process may hand path to, different
// from the one it already has, or "" when there is none.
//
// A real group change needs a real second group, and which ones are
// available depends on who is running the tests: root may use any, and
// anyone else may use a group they belong to. Both are searched so the
// test proving a chgrp really happens runs in as many environments as
// possible rather than only under root.
func copyOtherGroup(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	_, current, ok := posixOwnerIDs(info)
	if !ok {
		t.Skip("this platform does not report POSIX owner and group ids")
	}

	candidates, err := os.Getgroups()
	if err != nil {
		t.Fatalf("reading this process's groups: %v", err)
	}
	if os.Geteuid() == 0 {
		// root's supplementary set is usually just gid 0, and root may
		// chgrp to anything, so widen the search past it.
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
		// An all-digit group name is refused by the method itself, so it
		// would prove the wrong thing here.
		if _, numeric := strconv.Atoi(group.Name); numeric == nil {
			continue
		}
		return group.Name
	}
	return ""
}

// copySHA256 is the hex SHA-256 of s, computed here so a test asserts
// against an independently derived hash rather than against whatever the
// module happened to record.
func copySHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// copyExistingFile writes a file with exactly the mode given, defeating
// the process umask, and returns its path.
//
// os.WriteFile's own permission argument is masked, so a file asked for
// as 0644 can arrive as something else; the explicit chmod is what makes
// the starting state of every test below a known one.
func copyExistingFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// copyBackdate moves a path's modification time an hour into the past and
// returns the Unix timestamp it set.
//
// It exists because stat reports whole seconds, so a file created and
// rewritten inside the same second carries the same mtime on both sides
// of the write, and an assertion that the after state was really read
// back from the device would pass against a method that fabricated it.
// Backdating makes the difference unmistakable without sleeping.
func copyBackdate(t *testing.T, path string) int64 {
	t.Helper()

	when := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("backdating %s: %v", path, err)
	}
	return when.Unix()
}

// copyAbsentPath returns a path inside a real directory with nothing at
// it, which is where a create lands.
func copyAbsentPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "new-file")
}

// TestCopy_Registered proves the method registered itself as implemented
// and answers the one question the manifest is allowed to answer.
//
// The Notes are pinned on their substance, not merely on being non-empty.
// "The previous content is not restored" is the single most important
// thing an operator reading a rollback plan needs to know about this
// method, and a Notes field that quietly lost it would still satisfy
// registration.
func TestCopy_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.copy")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.copy")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, but this method emits a real inverse for both a create and an overwrite")
	}
	for _, phrase := range []string{"file.remove", "CONTENT is not restored", "changed nothing emits nothing"} {
		if !strings.Contains(d.Manifest.Reversibility.Notes, phrase) {
			t.Errorf("Reversibility.Notes = %q, want it to say %q", d.Manifest.Reversibility.Notes, phrase)
		}
	}
}

// TestCopy_RefusesSrc is the refusal this method exists to be honest
// about.
//
// It has to explain, not merely reject: src works perfectly well on the
// Crawl tier, so an author who tried it and got "unsupported parameter"
// would reasonably assume a bug. The message names the tier where it
// breaks and what to write instead. It is also checked BEFORE the
// missing-content refusal, since a converted playbook carries src and no
// content, and "content is required" would send its author to add a
// parameter rather than to understand the one they wrote.
func TestCopy_RefusesSrc(t *testing.T) {
	for _, params := range []map[string]any{
		{"dest": "/etc/hosts", "src": "files/hosts"},
		{"dest": "/etc/hosts", "src": nil},
		{"dest": "/etc/hosts", "src": "files/hosts", "content": "already here"},
	} {
		_, err := file.Copy(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "src is not supported") {
			t.Errorf("params %v: error = %q, want it to name the refused parameter", params, err)
		}
		if !strings.Contains(err.Error(), "per-task container") {
			t.Errorf("params %v: error = %q, want it to say why the runner cannot read it", params, err)
		}
		if !strings.Contains(err.Error(), "pass it as content") {
			t.Errorf("params %v: error = %q, want it to say what to write instead", params, err)
		}
	}
}

// TestCopy_RefusesAMissingDest proves the one required path is really
// required, and that the refusal happens before any connection.
//
// A nil device would fail in connect, so reaching the end of this test
// with the expected message is itself the proof that nothing tried to
// dial.
func TestCopy_RefusesAMissingDest(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{"content": "x"},
		{"dest": "", "content": "x"},
		{"dest": nil, "content": "x"},
	} {
		_, err := file.Copy(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "dest is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestCopy_RefusesMissingContent proves a task naming only a destination
// is a refusal rather than a create of an empty file.
//
// file.touch is what creates an empty file. A copy with no source has
// nothing to write, and quietly truncating the destination to nothing
// would be the most destructive possible reading of a runbook typo.
func TestCopy_RefusesMissingContent(t *testing.T) {
	for _, params := range []map[string]any{
		{"dest": "/etc/hosts"},
		{"dest": "/etc/hosts", "content": nil},
		{"dest": "/etc/hosts", "contents": "typo"},
	} {
		_, err := file.Copy(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "content is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestCopy_RefusesAParameterThatIsNotText covers the mistakes YAML makes
// on an author's behalf: an unquoted 0644 is the number 420, and an
// unquoted 8080 as content is an integer that would cross the Runner's
// task subprocess boundary as a float64.
//
// The refusal has to name the parameter and say to quote it. Treating a
// non-string as absent, the way sdk.StringParam does, would answer
// "content is required" for a task that plainly wrote one.
func TestCopy_RefusesAParameterThatIsNotText(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "dest", params: map[string]any{"dest": 42, "content": "x"}},
		{name: "content", params: map[string]any{"dest": "/etc/hosts", "content": 8080}},
		{name: "mode", params: map[string]any{"dest": "/etc/hosts", "content": "x", "mode": 420}},
		{name: "owner", params: map[string]any{"dest": "/etc/hosts", "content": "x", "owner": 1000}},
		{name: "group", params: map[string]any{"dest": "/etc/hosts", "content": "x", "group": 1000}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := file.Copy(context.Background(), nil, nil, tt.params)
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.name+" is int, not text") {
				t.Errorf("error = %q, want it to name %q and its type", err, tt.name)
			}
			if !strings.Contains(err.Error(), "quote it") {
				t.Errorf("error = %q, want it to say to quote the value", err)
			}
		})
	}
}

// TestCopy_RefusesAModeItCannotCompare proves a mode that is not plain
// octal is refused up front.
//
// A symbolic mode is the case that matters. Applying u+x unconditionally
// is the only way to honor it without resolving it against the current
// bits, and that is a method that reports changed on every run forever.
func TestCopy_RefusesAModeItCannotCompare(t *testing.T) {
	for _, mode := range []string{"u+x", "go-w", "0o644", "07777", "648", "rwx"} {
		params := copyParams("/etc/hosts", "x")
		params["mode"] = mode

		_, err := file.Copy(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("mode %q: expected a refusal, got nil", mode)
		}
		if !strings.Contains(err.Error(), "octal digits") {
			t.Errorf("mode %q: error = %q, want it to say what a mode must look like", mode, err)
		}
	}
}

// TestCopy_RefusesANumericOwnerOrGroup proves an id is refused where a
// name is required.
//
// Both parameters are checked because each is its own call site, and a
// copied check can be wired to the wrong value.
func TestCopy_RefusesANumericOwnerOrGroup(t *testing.T) {
	for _, key := range []string{"owner", "group"} {
		params := copyParams("/etc/hosts", "x")
		params[key] = "1000"

		_, err := file.Copy(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("%s: expected a refusal, got nil", key)
		}
		if !strings.Contains(err.Error(), key+" \"1000\" is a numeric id") {
			t.Errorf("%s: error = %q, want it to name the parameter and the id", key, err)
		}
	}
}

// TestCopy_RefusesAnUnreachableDevice covers the connect failure path,
// which is what a device with no SSH transport produces.
func TestCopy_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := file.Copy(context.Background(), newCopyContext(copyServer{}), newCopyUnreachable(), copyParams("/etc/hosts", "x"))
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestCopy_CreatesTheFile is the create path: nothing at dest, so the
// content is written and the file is this run's own.
//
// It pins the created mode at 0600 rather than at whatever the umask
// produces, which is this method's documented divergence from Ansible,
// and it asserts the bytes from the filesystem so a method that reported
// a checksum it computed locally while writing nothing would fail.
func TestCopy_CreatesTheFile(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyAbsentPath(t)
	const content = "endpoint: https://controller.internal:8443\n"

	result, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(dest, content))
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !result.Changed {
		t.Error("a run that created the file reported no change")
	}
	if got := copyFileBytes(t, dest); got != content {
		t.Errorf("the file holds %q on disk, want %q", got, content)
	}
	if got := copyFileMode(t, dest); got != "0600" {
		t.Errorf("the created file is %s on disk, want the documented 0600 default", got)
	}

	if got := rc.stats["dest"]; got != dest {
		t.Errorf("dest stat = %v, want %q", got, dest)
	}
	if got := rc.stats["checksum"]; got != copySHA256(content) {
		t.Errorf("checksum stat = %v, want %q", got, copySHA256(content))
	}
	if got := rc.stats["mode"]; got != "0600" {
		t.Errorf("mode stat = %v, want 0600", got)
	}
	if got := rc.stats["size"]; got != int64(len(content)) {
		t.Errorf("size stat = %v, want %d", got, len(content))
	}

	before := copyDiffHalf(t, rc, "before")
	if got := before["exists"]; got != false {
		t.Errorf("diff before exists = %v, want false: nothing was at the path", got)
	}
	if _, present := before["checksum"]; present {
		t.Error("diff before carries a checksum for a path that did not exist")
	}
	after := copyDiffHalf(t, rc, "after")
	if got := after["checksum"]; got != copySHA256(content) {
		t.Errorf("diff after checksum = %v, want %q", got, copySHA256(content))
	}
}

// TestCopy_CreateEmitsARemovalInverse proves the create path's inverse is
// a real, already-parameterized removal.
//
// This is the only case where a removal is the right undo, and getting it
// wrong in the other direction is what the run-time emission exists to
// prevent: a static "the inverse of copy is remove" would tell a rollback
// to delete a file that was there before the run.
func TestCopy_CreateEmitsARemovalInverse(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyAbsentPath(t)

	if _, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(dest, "fresh\n")); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	inverse := copyRecordedInverse(t, rc)
	if got := inverse["fqcn"]; got != "file.remove" {
		t.Errorf("inverse fqcn = %v, want file.remove: this run created the file", got)
	}
	if got := copyInverseParams(t, rc)["path"]; got != dest {
		t.Errorf("inverse path = %v, want %q", got, dest)
	}
	description, _ := inverse["description"].(string)
	if !strings.Contains(description, "which this run created") {
		t.Errorf("inverse description = %q, want it to say the run created the file", description)
	}
}

// TestCopy_ConvergedRunWritesNothing is the single most important
// property this method has.
//
// The proof is the modification time, not the Changed flag. A method that
// rewrote identical bytes would report no change if it compared its own
// output, and the file's mtime is the only thing that can tell that
// apart: a rewrite replaces the inode and moves the mtime, a genuine
// no-op does not.
func TestCopy_ConvergedRunWritesNothing(t *testing.T) {
	server := startCopyServer(t)
	const content = "verify: true\n"
	dest := copyExistingFile(t, content, 0o644)
	device := newCopyTarget(server)

	original, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat %s: %v", dest, err)
	}

	rc := newCopyContext(server)
	result, err := file.Copy(context.Background(), rc, device, copyParams(dest, content))
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if result.Changed {
		t.Error("a run that found the same bytes reported a change")
	}

	now, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat %s: %v", dest, err)
	}
	if !now.ModTime().Equal(original.ModTime()) {
		t.Error("the modification time moved, so the file was rewritten with identical bytes rather than left alone")
	}
	if got := copyFileMode(t, dest); got != "0644" {
		t.Errorf("the file is %s on disk, want the untouched 0644", got)
	}

	// A converged run still records a diff, and its two halves have to be
	// identical: that is what tells a rollback engine "this task changed
	// nothing, so undoing it means doing nothing", which an absent diff
	// cannot express.
	if before, after := copyDiffHalf(t, rc, "before"), copyDiffHalf(t, rc, "after"); !reflect.DeepEqual(before, after) {
		t.Errorf("a converged run recorded before %v and after %v, want them identical", before, after)
	}
	// And it emits NO inverse. The absence is the record saying undoing
	// this means doing nothing; an inverse here would make a rollback
	// perform work the forward run never did.
	if _, emitted := rc.stats["inverse"]; emitted {
		t.Error("a converged run emitted an inverse, so a rollback would act where the run did not")
	}
}

// TestCopy_OverwritesAndKeepsTheAttributes covers the case that is
// easiest to get wrong and impossible to notice.
//
// An atomic write replaces the destination with a fresh inode carrying
// mktemp's 0600 and the writing account's ownership. A method that did
// not read the path back and re-apply what it found would leave a 0644
// configuration file at 0600, having reported success, and the service
// reading that file would stop. The mode assertion here is the whole
// test; the content assertion is the easy half.
func TestCopy_OverwritesAndKeepsTheAttributes(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyExistingFile(t, "old\n", 0o644)
	backdated := copyBackdate(t, dest)
	const content = "new\n"

	result, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(dest, content))
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !result.Changed {
		t.Error("a run that replaced the content reported no change")
	}
	if got := copyFileBytes(t, dest); got != content {
		t.Errorf("the file holds %q on disk, want %q", got, content)
	}
	if got := copyFileMode(t, dest); got != "0644" {
		t.Errorf("the file is %s on disk, want the 0644 it already had: the atomic write's 0600 was not put back", got)
	}

	before := copyDiffHalf(t, rc, "before")
	if got := before["checksum"]; got != copySHA256("old\n") {
		t.Errorf("diff before checksum = %v, want the old content's hash %q", got, copySHA256("old\n"))
	}
	after := copyDiffHalf(t, rc, "after")
	if got := after["checksum"]; got != copySHA256(content) {
		t.Errorf("diff after checksum = %v, want %q", got, copySHA256(content))
	}
	// Both halves are proven to be real reads of the device rather than
	// the request echoed back: the before half carries the modification
	// time this test planted, and the after half carries a later one that
	// only the write could have produced.
	if got := before["mtime"]; got != backdated {
		t.Errorf("diff before mtime = %v, want the planted %d: the before half was not read from the device", got, backdated)
	}
	afterMtime, ok := after["mtime"].(int64)
	if !ok {
		t.Fatalf("diff after mtime is %T, want an int64", after["mtime"])
	}
	if afterMtime <= backdated {
		t.Errorf("diff after mtime = %d, want something later than the planted %d: the after half was not read back from the device", afterMtime, backdated)
	}
}

// TestCopy_OverwriteEmitsAnAttributeInverseThatAdmitsWhatIsLost proves
// the overwrite path emits the honest inverse.
//
// Two things have to be true at once. The FQCN must not be file.remove,
// because the file was there before this run and deleting it would
// destroy something the run never created. And the description must say
// plainly that the previous content is gone, because it is: the old bytes
// are never read and are journaled nowhere, and an inverse that quietly
// restored only the mode would read like a complete undo.
func TestCopy_OverwriteEmitsAnAttributeInverseThatAdmitsWhatIsLost(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyExistingFile(t, "old\n", 0o640)
	owner, group := copyFileNames(t, dest)

	if _, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(dest, "new\n")); err != nil {
		t.Fatalf("Copy: %v", err)
	}

	inverse := copyRecordedInverse(t, rc)
	if got := inverse["fqcn"]; got != "file.permissions" {
		t.Errorf("inverse fqcn = %v, want file.permissions: removing a file that was already there would destroy what the run did not create", got)
	}
	params := copyInverseParams(t, rc)
	for key, want := range map[string]any{"path": dest, "mode": "0640", "owner": owner, "group": group} {
		if got := params[key]; got != want {
			t.Errorf("inverse %s = %v, want %v", key, got, want)
		}
	}
	description, _ := inverse["description"].(string)
	if !strings.Contains(description, "does NOT restore the previous content") {
		t.Errorf("inverse description = %q, want it to say the old bytes are gone", description)
	}
}

// TestCopy_SetsTheAttributesItIsGiven proves a named mode really reaches
// the filesystem, and that it wins over the file's existing one.
//
// 0640 is deliberately neither the file's starting 0600 nor the create
// default, so a method that ignored the parameter entirely would fail
// here rather than pass by coincidence.
func TestCopy_SetsTheAttributesItIsGiven(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyExistingFile(t, "same\n", 0o600)

	params := copyParams(dest, "same\n")
	params["mode"] = "0640"

	result, err := file.Copy(context.Background(), rc, newCopyTarget(server), params)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !result.Changed {
		t.Error("a run that changed the mode reported no change")
	}
	if got := copyFileMode(t, dest); got != "0640" {
		t.Errorf("the file is %s on disk, want 0640: the mode was never applied", got)
	}
	if got := copyFileBytes(t, dest); got != "same\n" {
		t.Errorf("the file holds %q on disk, want it untouched: the content was already right", got)
	}
	// The inverse restores the mode that was there, which is the whole
	// point of emitting it from the run rather than declaring it.
	if got := copyInverseParams(t, rc)["mode"]; got != "0600" {
		t.Errorf("inverse mode = %v, want the 0600 the file had", got)
	}
	// Nothing was overwritten, so the description must NOT claim lost
	// content: this inverse really is complete.
	description, _ := copyRecordedInverse(t, rc)["description"].(string)
	if strings.Contains(description, "does NOT restore the previous content") {
		t.Errorf("inverse description = %q, want no claim of lost content: only the mode changed", description)
	}
}

// TestCopy_ChangesTheGroup proves a real chgrp reaches the filesystem,
// which no other test here can show: the mode tests would pass against a
// method that only ever looked at mode.
//
// It needs a second group this process may use, so it skips where there
// is none rather than pretending. The content is already converged, so
// the only thing this run changes is the group, and the emitted inverse
// therefore has to name the group the file had.
func TestCopy_ChangesTheGroup(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyExistingFile(t, "settled\n", 0o644)
	_, original := copyFileNames(t, dest)

	wanted := copyOtherGroup(t, dest)
	if wanted == "" {
		t.Skip("this process belongs to no second group, so a real chgrp cannot be proven here")
	}
	resolved, err := user.LookupGroup(wanted)
	if err != nil {
		t.Fatalf("looking up group %q: %v", wanted, err)
	}

	params := copyParams(dest, "settled\n")
	params["group"] = wanted

	result, err := file.Copy(context.Background(), rc, newCopyTarget(server), params)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !result.Changed {
		t.Error("a run that changed the group reported no change")
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat %s: %v", dest, err)
	}
	_, gid, ok := posixOwnerIDs(info)
	if !ok {
		t.Skip("this platform does not report POSIX owner and group ids")
	}
	if strconv.Itoa(gid) != resolved.Gid {
		t.Errorf("the file's gid is %d on disk, want %s: the group was never applied", gid, resolved.Gid)
	}
	if got := rc.stats["group"]; got != wanted {
		t.Errorf("group stat = %v, want %q", got, wanted)
	}
	if got := copyInverseParams(t, rc)["group"]; got != original {
		t.Errorf("inverse group = %v, want the %q the file had", got, original)
	}
	if got := copyFileBytes(t, dest); got != "settled\n" {
		t.Errorf("the file holds %q on disk, want it untouched: the content was already right", got)
	}
}

// TestCopy_GroupFailureIsReported covers the group comparison on a device
// where the named group does not exist, which is a case
// TestCopy_ChangesTheGroup skips on a machine with only one group.
//
// It also proves the group is compared at all: a method that ignored the
// parameter would send no chgrp and report a quiet success.
func TestCopy_GroupFailureIsReported(t *testing.T) {
	server := startCopyServer(t)
	dest := copyExistingFile(t, "settled\n", 0o644)

	params := copyParams(dest, "settled\n")
	params["group"] = "pleiades-no-such-group"

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), params)
	if err == nil {
		t.Fatal("a group that does not exist was accepted, so the parameter was never compared or never applied")
	}
	// The device is asked for owner and group in ONE chown, because the
	// owner the task never named is filled in from what the file already
	// had: an atomic write would otherwise hand the file to the connecting
	// account. So the failing operation is chown, and what makes it fail
	// is the group.
	if !strings.Contains(err.Error(), "chown "+dest) {
		t.Errorf("error = %q, want it to name the operation and the path that failed", err)
	}
	if !strings.Contains(err.Error(), "pleiades-no-such-group") {
		t.Errorf("error = %q, want it to carry what the device said about the group", err)
	}
	if strings.Contains(err.Error(), "the content was written") {
		t.Errorf("error = %q, want no claim that anything was written: the content was already right", err)
	}
}

// TestCopy_ConvergedModeReportsNoChange proves the mode comparison
// converges, and the second run writes the mode unpadded on purpose.
//
// "640" from a runbook and "640" from stat are the same permission bits,
// and remotefile.NormalizeMode is what makes them compare equal. Without
// it every run reports changed forever, which is invisible until somebody
// notices their report never settles.
func TestCopy_ConvergedModeReportsNoChange(t *testing.T) {
	server := startCopyServer(t)
	dest := copyExistingFile(t, "body\n", 0o640)

	params := copyParams(dest, "body\n")
	params["mode"] = "640"

	rc := newCopyContext(server)
	result, err := file.Copy(context.Background(), rc, newCopyTarget(server), params)
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if result.Changed {
		t.Error("a converged run reported a change: 640 and 0640 did not compare equal")
	}
	if got := rc.stats["mode"]; got != "0640" {
		t.Errorf("mode stat = %v, want the padded 0640 the device reports", got)
	}
	if _, emitted := rc.stats["inverse"]; emitted {
		t.Error("a converged run emitted an inverse")
	}
}

// TestCopy_TruncatesToEmptyContent proves an empty string is a real
// request rather than a missing one.
//
// Emptying a file is something a runbook does deliberately, so treating
// "" as absent would refuse the one task that most clearly needs it. The
// size read from the filesystem is what proves it happened.
func TestCopy_TruncatesToEmptyContent(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyExistingFile(t, "something\n", 0o644)

	result, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(dest, ""))
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if !result.Changed {
		t.Error("emptying a file reported no change")
	}
	if got := copyFileBytes(t, dest); got != "" {
		t.Errorf("the file holds %q on disk, want it empty", got)
	}
	if got := rc.stats["size"]; got != int64(0) {
		t.Errorf("size stat = %v, want 0", got)
	}
	if got := rc.stats["checksum"]; got != copySHA256("") {
		t.Errorf("checksum stat = %v, want the empty content's hash", got)
	}
}

// TestCopy_RefusesSomethingThatIsNotARegularFile proves a directory and a
// symbolic link are both refused, and that the refusal happens BEFORE
// anything is written.
//
// The atomic write renames over the destination, which would delete a
// directory's entry and REPLACE a link rather than write through it, and
// this method could put neither back. The assertion that the link still
// points where it did is what proves nothing was applied.
func TestCopy_RefusesSomethingThatIsNotARegularFile(t *testing.T) {
	server := startCopyServer(t)

	target := copyExistingFile(t, "pointed at\n", 0o644)
	link := filepath.Join(filepath.Dir(target), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "a-directory")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}

	for _, tt := range []struct{ name, path, kind string }{
		{name: "directory", path: dir, kind: "directory"},
		{name: "symlink", path: link, kind: "symlink"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(tt.path, "replacement\n"))
			if err == nil {
				t.Fatal("a path that is not a regular file was accepted, so this method replaced something it could not restore")
			}
			if !strings.Contains(err.Error(), "is a "+tt.kind+", not a regular file") {
				t.Errorf("error = %q, want it to name what is actually there", err)
			}
		})
	}

	if got := copyFileBytes(t, target); got != "pointed at\n" {
		t.Errorf("the link's target holds %q on disk, want it untouched", got)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("os.Lstat(%s) = %v, want the link still there", link, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("os.ReadDir(%s) = %v, %v, want the directory still there and empty", dir, entries, err)
	}
}

// TestCopy_StatFailureIsReported covers the branch where the connection
// authenticates and then cannot carry the first read.
//
// A session budget of zero is what that looks like from this side, and it
// is a real protocol-level refusal rather than an injected Go error. The
// point is that a read that failed is never mistaken for a path that is
// absent, which would turn a transport problem into a create.
func TestCopy_StatFailureIsReported(t *testing.T) {
	server := startCopyServerWithSessionBudget(t, 0)
	dest := copyExistingFile(t, "old\n", 0o644)

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(dest, "new\n"))
	if err == nil {
		t.Fatal("a failed read was reported as success")
	}
	if !strings.Contains(err.Error(), "stat "+dest) {
		t.Errorf("error = %q, want it to name the read that failed", err)
	}
	if got := copyFileBytes(t, dest); got != "old\n" {
		t.Errorf("the file holds %q on disk, want it untouched", got)
	}
}

// TestCopy_ChecksumFailureIsReported covers the branch where the path
// reads fine and the hash of its contents does not.
//
// Swallowing it would leave the comparison with an empty checksum, which
// differs from the content's hash, so the method would write over a file
// it had failed to read. That is a silent overwrite produced by a
// transport error, which is the worst available outcome here.
func TestCopy_ChecksumFailureIsReported(t *testing.T) {
	server := startCopyServerWithSessionBudget(t, 1)
	dest := copyExistingFile(t, "old\n", 0o644)

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(dest, "new\n"))
	if err == nil {
		t.Fatal("a failed checksum was reported as success")
	}
	if !strings.Contains(err.Error(), "checksum "+dest) {
		t.Errorf("error = %q, want it to name the checksum that failed", err)
	}
	if got := copyFileBytes(t, dest); got != "old\n" {
		t.Errorf("the file holds %q on disk, want it untouched: a failed read must never become a write", got)
	}
}

// TestCopy_WriteFailureIsReported covers the branch where the write
// itself fails, using the realistic cause: a parent directory that is not
// there.
//
// This method writes a file and never creates the directories above it,
// which is file.directory's job, so this is a runbook ordering mistake
// and the error has to name the path rather than the temporary file the
// atomic write was building.
func TestCopy_WriteFailureIsReported(t *testing.T) {
	server := startCopyServer(t)
	dest := filepath.Join(t.TempDir(), "no-such-directory", "target")

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(dest, "content\n"))
	if err == nil {
		t.Fatal("a failed write was reported as success")
	}
	if !strings.Contains(err.Error(), "write "+dest) {
		t.Errorf("error = %q, want it to name the write that failed", err)
	}
	if _, statErr := os.Lstat(dest); !os.IsNotExist(statErr) {
		t.Errorf("os.Lstat(%s) = %v, want the path still absent", dest, statErr)
	}
}

// TestCopy_ReadBackAfterWriteFailureIsReported covers the branch where
// the content landed and the fresh read that follows it did not.
//
// The error has to say the content was written, because it was: the
// device is not in the state the runbook started from and the task's
// failure alone would suggest otherwise. The filesystem assertion proves
// the claim is true rather than a fixed phrase.
func TestCopy_ReadBackAfterWriteFailureIsReported(t *testing.T) {
	server := startCopyServerWithSessionBudget(t, 3)
	dest := copyExistingFile(t, "old\n", 0o644)

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(dest, "new\n"))
	if err == nil {
		t.Fatal("a failed read-back was reported as success")
	}
	if !strings.Contains(err.Error(), "the content was written before this failed") {
		t.Errorf("error = %q, want it to say the write already landed", err)
	}
	if got := copyFileBytes(t, dest); got != "new\n" {
		t.Errorf("the file holds %q on disk, want %q: the error's claim about the write must be true", got, "new\n")
	}
}

// TestCopy_ApplyFailureAfterWriteIsReported covers the branch where the
// content landed and putting the attributes back did not.
//
// This is the one state neither the runbook nor a rollback recorded: the
// file holds the new bytes with the atomic write's own 0600 rather than
// the mode it had. The error is the only place that can be said.
func TestCopy_ApplyFailureAfterWriteIsReported(t *testing.T) {
	server := startCopyServerWithSessionBudget(t, 4)
	dest := copyExistingFile(t, "old\n", 0o644)

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(dest, "new\n"))
	if err == nil {
		t.Fatal("a failed chmod after a successful write was reported as success")
	}
	if !strings.Contains(err.Error(), "not the attributes the task asked for") {
		t.Errorf("error = %q, want it to say the attributes did not land", err)
	}
	if got := copyFileBytes(t, dest); got != "new\n" {
		t.Errorf("the file holds %q on disk, want the new content the error claims landed", got)
	}
	if got := copyFileMode(t, dest); got != "0600" {
		t.Errorf("the file is %s on disk, want the atomic write's 0600: the error claims the mode was not restored", got)
	}
}

// TestCopy_ApplyFailureWithoutAWriteIsReported covers the branch where
// nothing had been changed yet when the change failed.
//
// An owner that does not exist is the realistic shape of it, and the
// content is already converged so no write is sent. The error must NOT
// claim the content was written, because telling an operator to expect
// new bytes where there are none sends them looking at the wrong thing.
func TestCopy_ApplyFailureWithoutAWriteIsReported(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	dest := copyExistingFile(t, "same\n", 0o644)

	params := copyParams(dest, "same\n")
	params["owner"] = "pleiades-no-such-user"

	_, err := file.Copy(context.Background(), rc, newCopyTarget(server), params)
	if err == nil {
		t.Fatal("a failed chown was reported as success")
	}
	if !strings.Contains(err.Error(), "chown") {
		t.Errorf("error = %q, want it to name the operation that failed", err)
	}
	if strings.Contains(err.Error(), "the content was written") {
		t.Errorf("error = %q, want no claim that anything was written: the content was already right", err)
	}
	if got := copyFileBytes(t, dest); got != "same\n" {
		t.Errorf("the file holds %q on disk, want it untouched", got)
	}
	if _, recorded := rc.stats["diff"]; recorded {
		t.Error("a diff was recorded for a failed run, so its after state describes a change that never completed")
	}
}

// TestCopy_ReadBackStatFailureIsReported covers the branch where the
// whole change landed and the final read of the path did not.
func TestCopy_ReadBackStatFailureIsReported(t *testing.T) {
	server := startCopyServerWithSessionBudget(t, 5)
	dest := copyExistingFile(t, "old\n", 0o644)

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(dest, "new\n"))
	if err == nil {
		t.Fatal("a failed read-back was reported as success")
	}
	if !strings.Contains(err.Error(), "the change was applied") {
		t.Errorf("error = %q, want it to say the device already moved", err)
	}
	if got := copyFileBytes(t, dest); got != "new\n" {
		t.Errorf("the file holds %q on disk, want the new content", got)
	}
}

// TestCopy_ReadBackChecksumFailureIsReported covers the second half of
// the read-back, which the stat failure above can never reach.
//
// Two reads make the after state, and each is its own call site with its
// own error. Verifying only one of them would leave a method that could
// swallow the other and report a checksum it never obtained.
func TestCopy_ReadBackChecksumFailureIsReported(t *testing.T) {
	server := startCopyServerWithSessionBudget(t, 6)
	dest := copyExistingFile(t, "old\n", 0o644)

	_, err := file.Copy(context.Background(), newCopyContext(server), newCopyTarget(server), copyParams(dest, "new\n"))
	if err == nil {
		t.Fatal("a failed checksum read-back was reported as success")
	}
	if !strings.Contains(err.Error(), "checksum "+dest) {
		t.Errorf("error = %q, want it to name the checksum that failed", err)
	}
	if !strings.Contains(err.Error(), "the change was applied") {
		t.Errorf("error = %q, want it to say the device already moved", err)
	}
}

// TestCopy_DiffRecordFailureIsReported covers the branch where the change
// landed and recording the prior state did not.
//
// Swallowing it would leave a rollback engine with no record of a change
// that really happened, which is worse than a failed task.
func TestCopy_DiffRecordFailureIsReported(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	rc.failOn = "diff"

	_, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(copyAbsentPath(t), "content\n"))
	if err == nil {
		t.Fatal("a failure to record the diff was swallowed")
	}
	if !errors.Is(err, errCopyStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
}

// TestCopy_InverseRecordFailureIsReported covers the same swallowing
// question at the second recording call site, which only a run that
// really changed something reaches.
func TestCopy_InverseRecordFailureIsReported(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	rc.failOn = "inverse"

	_, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(copyAbsentPath(t), "content\n"))
	if err == nil {
		t.Fatal("a failure to record the inverse was swallowed")
	}
	if !errors.Is(err, errCopyStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
	if _, recorded := rc.stats["diff"]; !recorded {
		t.Error("the diff was not recorded before the inverse, so the two call sites ran in the wrong order")
	}
}

// TestCopy_StatRecordFailureIsReported covers the third recording call
// site, the returned stats, which neither test above can reach.
func TestCopy_StatRecordFailureIsReported(t *testing.T) {
	server := startCopyServer(t)
	rc := newCopyContext(server)
	rc.failOn = "dest"

	_, err := file.Copy(context.Background(), rc, newCopyTarget(server), copyParams(copyAbsentPath(t), "content\n"))
	if err == nil {
		t.Fatal("a failure to record the returned stats was swallowed")
	}
	if !errors.Is(err, errCopyStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
	if _, recorded := rc.stats["inverse"]; !recorded {
		t.Error("the inverse was not recorded before the stats, so the call sites ran in the wrong order")
	}
}
