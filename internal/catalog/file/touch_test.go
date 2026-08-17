package file_test

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These tests drive file.touch against a real in-process SSH server that
// hands every command to a real /bin/sh
// (pkg/remoteexec/remoteexectest), so stat, touch, chmod and chown all
// behave exactly as they do on a device.
//
// Every assertion about what happened is made against the LOCAL
// FILESYSTEM (os.Stat, os.ReadFile) rather than against the stats the
// method recorded about itself. That distinction is the point: a method
// that returned a perfect diff and touched nothing would pass a test that
// only read its own report, and the diff assertions below are therefore
// always paired with a filesystem one.

// errTouchStat is what the stub context fails a chosen SetStat with, so a
// test can drive the branches where the work succeeded and recording it
// did not.
var errTouchStat = errors.New("recording the stat failed")

// touchDevice is a target reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the accessors
// capability.SSHTransportCapable and capability.POSIXFileSystemCapable
// require, which that stub deliberately does not provide.
type touchDevice struct {
	*inventorytest.Stub
	host string
	port int
}

// SSHHost returns where the harness is listening.
func (d *touchDevice) SSHHost() string { return d.host }

// SSHPort returns the harness's kernel-chosen port.
func (d *touchDevice) SSHPort() int { return d.port }

// RootPath satisfies capability.POSIXFileSystemCapable, the capability
// this method's manifest requires.
//
// file.touch never reads it, and the double implements it anyway so that
// the target a test runs against is the shape the manifest describes. A
// double that declared a capability it could not answer would let a later
// change start reading the accessor and pass here while failing against
// any real device.
func (d *touchDevice) RootPath() string { return "/" }

// newTouchStub builds the base inventory item the device doubles wrap.
func newTouchStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "file-touch-1",
		StubName:  "touch-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newTouchDevice builds the target a test runs against.
func newTouchDevice(server *remoteexectest.Server) *touchDevice {
	return &touchDevice{Stub: newTouchStub(), host: server.Host, port: server.Port}
}

// newTouchUnreachableDevice builds a target this method cannot reach at
// all: the bare stub, which implements InventoryItem and nothing else.
func newTouchUnreachableDevice() inventory.InventoryItem {
	return newTouchStub()
}

// touchContext is a minimal sdk.RunbookContext carrying a fixed secret
// set, standing in for the real one the composition root builds from the
// credential store (Walk tier) or the dispatch payload (Crawl tier).
type touchContext struct {
	secrets map[string]string
	stats   map[string]any

	// failKey, when set, makes SetStat fail for that ONE key. A context
	// that failed every key could only ever reach the first recording
	// call, so the later ones would have no covered failure path.
	failKey string
}

// newTouchContext builds a context holding the harness's own credential.
func newTouchContext(server *remoteexectest.Server) *touchContext {
	return &touchContext{secrets: server.Secrets(), stats: map[string]any{}}
}

// InjectSecrets hands back the credential the harness accepts.
func (c *touchContext) InjectSecrets() map[string]string { return c.secrets }

// SetStat records a stat, or fails when this context was told to fail
// that key.
func (c *touchContext) SetStat(key string, value any) error {
	if c.failKey != "" && c.failKey == key {
		return errTouchStat
	}
	c.stats[key] = value
	return nil
}

// EmitFact records a fact the same way, which is enough for this method:
// it emits none.
func (c *touchContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// startTouchServer brings up the real-shell SSH harness for one test.
func startTouchServer(t *testing.T) *remoteexectest.Server {
	t.Helper()
	return startTouchServerWithLimit(t, nil)
}

// startTouchServerWithLimit is startTouchServer with a cap on how many
// session channels the server will accept.
//
// It is how the tests below reach the failures that only a device
// refusing part way through a task can produce. Rejecting a channel is a
// real protocol-level refusal rather than an injected Go error, so the
// branch under test sees the shape a device under session pressure
// produces. It is also how the converged-mode test proves a chmod was NOT
// sent: the budget is the exact number of commands a converged run needs.
func startTouchServerWithLimit(t *testing.T, limit *int) *remoteexectest.Server {
	t.Helper()
	server, err := remoteexectest.Start(remoteexectest.Options{SessionLimit: limit})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(server.Close)
	return server
}

// touchParams builds a task's params: the path, host key verification
// skipped because the harness's key is generated per run, plus whatever
// else the case needs.
func touchParams(path string, extra map[string]any) map[string]any {
	params := map[string]any{
		"path":                          path,
		"insecure_skip_host_key_verify": true,
	}
	for key, value := range extra {
		params[key] = value
	}
	return params
}

// touchDiffHalves pulls the two halves of the recorded diff stat apart,
// failing the test when either is missing.
func touchDiffHalves(t *testing.T, rc *touchContext) (before, after map[string]any) {
	t.Helper()
	record, ok := rc.stats["diff"].(map[string]any)
	if !ok {
		t.Fatalf("diff stat = %#v, want a map: nothing recorded the before and after state", rc.stats["diff"])
	}
	before, _ = record["before"].(map[string]any)
	after, _ = record["after"].(map[string]any)
	if before == nil || after == nil {
		t.Fatalf("diff = %#v, want both a before and an after half", record)
	}
	return before, after
}

// TestTouch_Registered proves the method registered itself as
// implemented, with the inverse a rollback engine would read.
//
// The Captures list is pinned rather than merely checked for being
// non-empty. It is the contract between this method's diff and the
// inverse that consumes it, and dropping a key from it would be invisible
// until a rollback tried to read one that was never recorded.
func TestTouch_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.touch")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.touch")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	// True, because a created file can be removed and a changed mode can be
	// put back. It is true even though one thing this method changes on
	// every run, the modification time, can never be restored: the field
	// asks whether an inverse can EVER be emitted, not whether one is
	// always complete.
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, want true: a file this method created can be removed again")
	}
	// The Notes are the only place the incompleteness is stated. Nothing in
	// the build refuses a reversible method whose notes quietly stop saying
	// which runs emit nothing, and a reader who missed that would expect a
	// rollback to restore a timestamp that nothing here can set.
	if d.Manifest.Reversibility.Notes == "" {
		t.Fatal("Reversibility.Notes is empty, so nothing says what this method's inverse cannot restore")
	}
	// Naming the modification time is not enough on its own: notes that
	// mention it while claiming it IS put back would pass that and be
	// exactly the wrong document. The limit itself has to be stated.
	notes := strings.ToLower(d.Manifest.Reversibility.Notes)
	if !strings.Contains(notes, "modification time") {
		t.Errorf("Reversibility.Notes = %q, want it to name the modification time", d.Manifest.Reversibility.Notes)
	}
	if !strings.Contains(notes, "not restored") {
		t.Errorf("Reversibility.Notes = %q, want it to say the modification time is NOT restored", d.Manifest.Reversibility.Notes)
	}
}

// TestTouch_CreatedEmitsARemoval proves the branch where nothing was at
// the path: this run made the file, so the undo removes it.
func TestTouch_CreatedEmitsARemoval(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "made-here")

	if _, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil)); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	fqcn, params, description := fileInverse(t, rc.stats)
	if fqcn != "file.remove" {
		t.Errorf("the inverse names %q, want file.remove: this run created the file", fqcn)
	}
	if got := params["path"]; got != path {
		t.Errorf("the inverse would remove %v, want %q", got, path)
	}
	// The run really did create it, read back out of the record rather than
	// assumed, so a regression that stopped creating would not leave this
	// test quietly asserting the same thing about a different branch.
	before, _ := touchDiffHalves(t, rc)
	if got := before["exists"]; got != false {
		t.Errorf("diff.before.exists = %v, want false: this test did not exercise the create branch", got)
	}
	if !strings.Contains(description, path) {
		t.Errorf("description = %q, want it to name the file a rollback would remove", description)
	}
}

// TestTouch_ReStampingAnExistingFileEmitsNoInverse is the case this
// method is honest about being unable to undo.
//
// The file was already there and nothing but its modification time moved.
// No method in this catalog can set a modification time, so there is no
// instruction that would put it back, and NOTHING is emitted.
//
// The tempting wrong answer is a file.remove, and it would be the worst
// outcome available: the file existed before this task, so a rollback
// would destroy something the run never created in exchange for undoing a
// timestamp. This run reports Changed true, which is what makes the
// assertion worth having: the absence of an inverse here is not the
// converged case, it is a real change that genuinely cannot be reversed.
func TestTouch_ReStampingAnExistingFileEmitsNoInverse(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "already-here")

	if err := os.WriteFile(path, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("creating the file under test: %v", err)
	}

	result, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if !result.Changed {
		t.Fatal("re-stamping a file reported no change, so this method stopped moving the modification time")
	}
	assertNoFileInverse(t, rc.stats)

	// The file has to still be there for the point to hold: this is the
	// branch where an emitted removal would have destroyed it.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("os.Stat(%s) = %v, want the file still there", path, err)
	}
	before, _ := touchDiffHalves(t, rc)
	if got := before["exists"]; got != true {
		t.Errorf("diff.before.exists = %v, want true: this test did not exercise the re-stamp branch", got)
	}
}

// TestTouch_AttributeChangeEmitsAPartialRestore proves rule three of the
// contract: where a run changed something it cannot fully restore, it
// still emits the inverse for the part it can, and says what is missing.
//
// This run re-stamped an existing file AND changed its mode. The
// modification time is gone for good, but the mode is not, so an inverse
// that put the mode back is strictly better than none. What it cannot do
// has to be said in the description, because an operator approving a
// rollback plan is entitled to know the restore is partial.
func TestTouch_AttributeChangeEmitsAPartialRestore(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "restamped")

	if err := os.WriteFile(path, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("creating the file under test: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("setting the starting mode: %v", err)
	}

	if _, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, map[string]any{
		"mode": "0644",
	})); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	fqcn, params, description := fileInverse(t, rc.stats)
	if fqcn == "file.remove" {
		t.Fatalf("the inverse names file.remove for a file this run did not create: rolling back would delete %q", path)
	}
	if fqcn != "file.permissions" {
		t.Errorf("the inverse names %q, want file.permissions", fqcn)
	}

	// Read out of the recorded prior state, so this proves the inverse
	// carries what the run observed.
	before, _ := touchDiffHalves(t, rc)
	for _, key := range []string{"mode", "owner", "group"} {
		if got, want := params[key], before[key]; got != want {
			t.Errorf("the inverse would set %s to %v, want %v, which is what the run found", key, got, want)
		}
	}
	if got := params["mode"]; got != "0600" {
		t.Errorf("the inverse would set the mode to %v, want 0600: it carries the mode this run APPLIED, not the one it found", got)
	}
	// The honesty requirement. A partial restore that does not say it is
	// partial is worse than one that does, because it reads as complete.
	// Both halves are asserted: naming the modification time while
	// claiming it comes back would satisfy the first check alone and would
	// be precisely the misleading plan this is meant to prevent.
	lowered := strings.ToLower(description)
	if !strings.Contains(lowered, "modification time") {
		t.Errorf("description = %q, want it to name the modification time", description)
	}
	if !strings.Contains(lowered, "not restored") {
		t.Errorf("description = %q, want it to say the modification time is NOT restored", description)
	}
}

// TestTouch_RefusesAMissingPath proves the one required parameter is
// really required, and that the refusal names it.
//
// The device is nil, so anything that tried to connect would fail with a
// different message. Asserting on the message rather than merely on "an
// error happened" is what keeps this from passing for the wrong reason.
func TestTouch_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{nil, {}, {"path": ""}, {"mode": "0644"}, {"path": 7}} {
		_, err := file.Touch(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestTouch_CreatesTheFile is the create path: nothing at the path, an
// empty regular file afterward.
//
// The diff has to say exists false then true, because that is the only
// thing that tells an inverse it may remove the file. A diff that recorded
// the same shape for a created file and a re-stamped one would make a
// rollback either delete a file the run never created or leave one behind
// forever.
func TestTouch_CreatesTheFile(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "created")

	result, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if !result.Changed {
		t.Error("creating a file reported no change")
	}

	// The filesystem, not the method's own account of itself.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the file was reported created and is not there: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("mode = %v, want a regular file", info.Mode())
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want an empty file", info.Size())
	}

	if got := rc.stats["created"]; got != true {
		t.Errorf("created = %v, want true", got)
	}
	if got := rc.stats["dest"]; got != path {
		t.Errorf("dest = %v, want %q", got, path)
	}

	before, after := touchDiffHalves(t, rc)
	if got := before["exists"]; got != false {
		t.Errorf("diff.before.exists = %v, want false", got)
	}
	if got := after["exists"]; got != true {
		t.Errorf("diff.after.exists = %v, want true", got)
	}
	// An absent path has no mode, owner or mtime to capture, and their
	// absence is exactly what says "this file was made here".
	if _, ok := before["mtime"]; ok {
		t.Errorf("diff.before = %v, want no mtime for a path that was not there", before)
	}
	if _, ok := after["mtime"]; !ok {
		t.Errorf("diff.after = %v, want the new modification time recorded", after)
	}
}

// TestTouch_MovesAnExistingFilesModificationTime is the other kind of
// change, and the one the method is named for.
//
// The file is aged an hour first, so the move is unambiguous. Without
// that the assertion would be at the mercy of stat's one-second
// resolution: two runs inside the same second produce the same recorded
// mtime, and a test comparing them would fail for a reason that has
// nothing to do with the method.
func TestTouch_MovesAnExistingFilesModificationTime(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "existing")

	const content = "do not lose me\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("creating the file under test: %v", err)
	}
	aged := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, aged, aged); err != nil {
		t.Fatalf("aging the file under test: %v", err)
	}

	result, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if !result.Changed {
		t.Error("moving a modification time reported no change")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after touch: %v", err)
	}
	if !info.ModTime().After(aged) {
		t.Errorf("mtime = %v, want it moved past %v", info.ModTime(), aged)
	}
	// Touch must not truncate. Ansible's own state: touch does not, and a
	// module that emptied a log file it was asked to stamp would be the
	// worst kind of surprise.
	body, err := os.ReadFile(path) // #nosec G304 -- the path is this test's own temporary directory
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	if string(body) != content {
		t.Errorf("contents = %q, want %q: touch truncated the file", body, content)
	}

	if got := rc.stats["created"]; got != false {
		t.Errorf("created = %v, want false: the file was already there", got)
	}

	before, after := touchDiffHalves(t, rc)
	if got := before["exists"]; got != true {
		t.Errorf("diff.before.exists = %v, want true", got)
	}
	beforeMtime, ok := before["mtime"].(int64)
	if !ok {
		t.Fatalf("diff.before.mtime = %#v, want an int64: the inverse's capture is missing", before["mtime"])
	}
	afterMtime, ok := after["mtime"].(int64)
	if !ok {
		t.Fatalf("diff.after.mtime = %#v, want an int64", after["mtime"])
	}
	if afterMtime <= beforeMtime {
		t.Errorf("diff mtime went %d then %d, want the recorded time to move", beforeMtime, afterMtime)
	}
}

// TestTouch_ASecondRunStillReportsChanged pins the property that makes
// this method different from every other converging one in the namespace.
//
// A second run reports changed, on purpose, because the modification time
// really did move again. This is the test that would be deleted by
// somebody "fixing" the module to be idempotent, so it says plainly what
// the right answer is: changed stays true, created goes false, and the
// file is neither recreated nor emptied.
func TestTouch_ASecondRunStillReportsChanged(t *testing.T) {
	server := startTouchServer(t)
	path := filepath.Join(t.TempDir(), "twice")

	first := newTouchContext(server)
	if _, err := file.Touch(context.Background(), first, newTouchDevice(server), touchParams(path, nil)); err != nil {
		t.Fatalf("first Touch: %v", err)
	}
	if got := first.stats["created"]; got != true {
		t.Errorf("first run created = %v, want true", got)
	}

	// Written between the two runs, so the second run's failure to leave
	// it alone shows up as lost content rather than as a flag.
	const content = "written between the runs\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing between the runs: %v", err)
	}

	second := newTouchContext(server)
	result, err := file.Touch(context.Background(), second, newTouchDevice(server), touchParams(path, nil))
	if err != nil {
		t.Fatalf("second Touch: %v", err)
	}
	if !result.Changed {
		t.Error("the second run reported no change; a moved modification time IS a change")
	}
	if got := second.stats["created"]; got != false {
		t.Errorf("second run created = %v, want false: the file was already there", got)
	}

	body, err := os.ReadFile(path) // #nosec G304 -- the path is this test's own temporary directory
	if err != nil {
		t.Fatalf("reading the file back: %v", err)
	}
	if string(body) != content {
		t.Errorf("contents = %q, want %q: the second run rewrote the file", body, content)
	}

	before, after := touchDiffHalves(t, second)
	if before["exists"] != true || after["exists"] != true {
		t.Errorf("diff exists went %v then %v, want true then true for a file that was already there",
			before["exists"], after["exists"])
	}
}

// TestTouch_AppliesTheRequestedMode is the attribute change: the file is
// there with the wrong permissions and comes back with the right ones.
func TestTouch_AppliesTheRequestedMode(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "mode")

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("creating the file under test: %v", err)
	}
	// Set explicitly rather than trusting WriteFile, whose permission
	// argument is filtered through the process umask.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("setting the starting mode: %v", err)
	}

	if _, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, map[string]any{
		"mode": "0644",
	})); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after touch: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %04o, want %04o", got, 0o644)
	}

	before, after := touchDiffHalves(t, rc)
	if got := before["mode"]; got != "0600" {
		t.Errorf("diff.before.mode = %v, want %q", got, "0600")
	}
	if got := after["mode"]; got != "0644" {
		t.Errorf("diff.after.mode = %v, want %q", got, "0644")
	}
}

// TestTouch_LeavesAConvergedModeAlone proves the mode is compared before
// it is applied, rather than being chmodded on every run.
//
// The proof is the session budget, not a flag. A converged run needs
// exactly three commands (read the state, touch, read it back), so a
// budget of three succeeds only if no chmod was sent. The second case is
// the control: the same budget with a mode that really does differ must
// fail, which is what stops this test from passing against an
// implementation that sends no chmod at all.
func TestTouch_LeavesAConvergedModeAlone(t *testing.T) {
	const convergedRunCommands = 3

	newPath := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "converged")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("creating the file under test: %v", err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("setting the starting mode: %v", err)
		}
		return path
	}

	t.Run("the mode already matches", func(t *testing.T) {
		server := startTouchServerWithLimit(t, remoteexectest.Limit(convergedRunCommands))
		rc := newTouchContext(server)
		path := newPath(t)

		if _, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, map[string]any{
			"mode": "0644",
		})); err != nil {
			t.Fatalf("Touch: %v: a converged mode was chmodded anyway", err)
		}
	})

	t.Run("the mode differs", func(t *testing.T) {
		server := startTouchServerWithLimit(t, remoteexectest.Limit(convergedRunCommands))
		rc := newTouchContext(server)
		path := newPath(t)

		if _, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, map[string]any{
			"mode": "0600",
		})); err == nil {
			t.Fatal("a differing mode fitted inside a converged run's command budget, so no chmod was sent")
		}
	})
}

// TestTouch_AppliesTheRequestedOwnerAndGroup proves owner and group are
// read and handed to the device.
//
// It needs an account that can change file ownership, which is root on
// Linux, so it skips rather than failing elsewhere. The refusal test
// below covers the same two parameters without any privilege, by giving
// names that do not exist and watching the device say so, so a skipped
// run here still leaves both parameters proven to be read.
func TestTouch_AppliesTheRequestedOwnerAndGroup(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing file ownership needs root")
	}

	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "owned")

	if _, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, map[string]any{
		"owner": "daemon",
		"group": "daemon",
	})); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	// Read back with a separate tool rather than through the method's own
	// stats. Go's portable FileInfo exposes no owner, and asking stat
	// directly keeps this an observation of the filesystem.
	out, err := exec.Command("stat", "-c", "%U:%G", path).Output() // #nosec G204 -- the path is this test's own temporary directory
	if err != nil {
		t.Fatalf("reading the ownership back: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "daemon:daemon" {
		t.Errorf("ownership = %q, want %q", got, "daemon:daemon")
	}

	_, after := touchDiffHalves(t, rc)
	if after["owner"] != "daemon" || after["group"] != "daemon" {
		t.Errorf("diff.after owner/group = %v/%v, want daemon/daemon", after["owner"], after["group"])
	}
}

// TestTouch_RefusesSomethingThatIsNotARegularFile covers the guard, for
// each of the three things Stat can find that are not one.
//
// Refusing rather than following or replacing is a decision worth pinning.
// A symbolic link is the case that matters most: following it would stamp
// a different path than the task named and then record a diff describing
// the wrong one, and the file at the end of the link would be the thing a
// rollback later acted on.
func TestTouch_RefusesSomethingThatIsNotARegularFile(t *testing.T) {
	tests := []struct {
		name  string
		setUp func(t *testing.T, dir string) string
	}{
		{
			name: "a directory",
			setUp: func(t *testing.T, dir string) string {
				t.Helper()
				path := filepath.Join(dir, "adir")
				if err := os.Mkdir(path, 0o750); err != nil {
					t.Fatalf("creating the directory under test: %v", err)
				}
				return path
			},
		},
		{
			name: "a symbolic link to a regular file",
			setUp: func(t *testing.T, dir string) string {
				t.Helper()
				target := filepath.Join(dir, "target")
				if err := os.WriteFile(target, []byte("target\n"), 0o600); err != nil {
					t.Fatalf("creating the link target: %v", err)
				}
				path := filepath.Join(dir, "alink")
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("creating the link under test: %v", err)
				}
				return path
			},
		},
		{
			name: "a socket",
			setUp: func(t *testing.T, dir string) string {
				t.Helper()
				path := filepath.Join(dir, "asocket")
				listener, err := net.Listen("unix", path)
				if err != nil {
					t.Fatalf("creating the socket under test: %v", err)
				}
				// Held open for the whole case: closing a Unix listener
				// unlinks its socket file, which would leave nothing at the
				// path to refuse.
				t.Cleanup(func() { _ = listener.Close() })
				return path
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startTouchServer(t)
			rc := newTouchContext(server)
			path := tt.setUp(t, t.TempDir())

			before, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("stat before touch: %v", err)
			}

			_, err = file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), "not a regular file") {
				t.Errorf("error = %q, want it to say the path is not a regular file", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to name the path", err)
			}

			// Nothing was touched, replaced or stamped.
			after, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("stat after the refusal: %v", err)
			}
			if !after.ModTime().Equal(before.ModTime()) {
				t.Errorf("modification time moved from %v to %v: the refusal happened after the touch",
					before.ModTime(), after.ModTime())
			}
			if after.Mode() != before.Mode() {
				t.Errorf("mode changed from %v to %v", before.Mode(), after.Mode())
			}
			if _, ok := rc.stats["diff"]; ok {
				t.Error("a diff was recorded for a task that refused to act")
			}
		})
	}
}

// The remaining tests cover this method's error paths, one per call site.
// Each exists because a swallowed error here turns a failed task into a
// successful one, which is the failure shape a run report cannot recover
// from.

// TestTouch_RefusesAnUnreachableDevice covers the connect failure path.
func TestTouch_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := file.Touch(context.Background(), &touchContext{stats: map[string]any{}},
		newTouchUnreachableDevice(), map[string]any{"path": "/tmp/anywhere"})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestTouch_ReportsAFailureToReadTheCurrentState covers the first Stat.
//
// A budget of zero is a device that authenticates and then refuses the
// very first command. Reading the state is what the guard and the whole
// rollback record depend on, so a failure here must stop the task rather
// than be treated as "nothing is there", which would go on to create a
// file over something unknown.
//
// The assertion on WHICH failure is reported is not decoration, and a
// mutation is why it is here. Dropping the error check leaves the zero
// remotefile.Info behind, whose Kind is the empty string, and Exists
// reports true for it because the only value it treats as absent is
// KindAbsent. The guard below then refuses with "not a regular file", so
// the task still fails and a test that only asked whether it failed
// passes while the operator is told to go look at a path that was never
// read.
func TestTouch_ReportsAFailureToReadTheCurrentState(t *testing.T) {
	server := startTouchServerWithLimit(t, remoteexectest.Limit(0))
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "unreadable")

	_, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err == nil {
		t.Fatal("a failure to read the current state was reported as success")
	}
	if !strings.Contains(err.Error(), "stat") {
		t.Errorf("error = %q, want it to say the state could not be read", err)
	}
	if strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %q, want the read failure rather than a guess about what is at the path", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the file was created even though the state could not be read")
	}
}

// TestTouch_ReportsAFailureToCreateTheFile covers the touch itself, with
// a real failure: a parent directory that does not exist.
func TestTouch_ReportsAFailureToCreateTheFile(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "no-such-directory", "child")

	_, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err == nil {
		t.Fatal("a touch into a missing directory was reported as success")
	}
	if !strings.Contains(err.Error(), "file.touch") {
		t.Errorf("error = %q, want it to name the method", err)
	}
	if _, ok := rc.stats["diff"]; ok {
		t.Error("a diff was recorded for a task that never created anything")
	}
}

// TestTouch_ReportsAFailureToApplyAttributes covers the Apply call, twice
// over, and does double duty.
//
// Each case fails because the device rejects the value, and the value is
// the one the task named, so the error text is also the proof that the
// parameter was read and passed through at all. That is what lets the
// positive ownership test skip on a machine without root while both
// parameters stay covered.
func TestTouch_ReportsAFailureToApplyAttributes(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{name: "an impossible mode", extra: map[string]any{"mode": "not-a-mode"}, want: "not-a-mode"},
		{name: "an unknown owner", extra: map[string]any{"owner": "no-such-user-here"}, want: "no-such-user-here"},
		{name: "an unknown group", extra: map[string]any{"group": "no-such-group-here"}, want: "no-such-group-here"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startTouchServer(t)
			rc := newTouchContext(server)
			path := filepath.Join(t.TempDir(), "attributes")

			_, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, tt.extra))
			if err == nil {
				t.Fatal("a rejected attribute was reported as success")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to carry %q, the value the task asked for", err, tt.want)
			}
			// The file was still created before the attribute failed, which
			// is worth pinning: the task failed part way, and a diff that
			// claimed otherwise would be a lie.
			if _, statErr := os.Stat(path); statErr != nil {
				t.Errorf("the file is not there: %v", statErr)
			}
			if _, ok := rc.stats["diff"]; ok {
				t.Error("a diff was recorded for a task that failed before reading the state back")
			}
		})
	}
}

// TestTouch_ReportsAFailureToReadTheStateBack covers the second Stat.
//
// A budget of two lets the state read and the touch through and refuses
// the read-back. The change has already happened at that point, so the
// task genuinely failed: it can no longer say what it left behind, and a
// rollback record with a guessed after half is worse than none.
func TestTouch_ReportsAFailureToReadTheStateBack(t *testing.T) {
	server := startTouchServerWithLimit(t, remoteexectest.Limit(2))
	rc := newTouchContext(server)
	path := filepath.Join(t.TempDir(), "half-done")

	_, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err == nil {
		t.Fatal("a failure to read the state back was reported as success")
	}
	// The touch itself did land, which is what makes this branch worth
	// having: the device changed and the task still has to fail.
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("the touch did not run, so this covered the wrong branch: %v", statErr)
	}
}

// TestTouch_ReportsAFailureToRecordTheDiff covers the rollback record's
// own write. Losing it silently would leave a changed device with no
// record of what it looked like before.
func TestTouch_ReportsAFailureToRecordTheDiff(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	rc.failKey = "diff"
	path := filepath.Join(t.TempDir(), "undiffable")

	_, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err == nil {
		t.Fatal("a failure to record the diff was swallowed")
	}
	if !errors.Is(err, errTouchStat) {
		t.Errorf("error = %v, want it to wrap the recording failure", err)
	}
}

// TestTouch_ReportsAFailureToRecordTheInverse covers the recording call
// site between the diff and the returned stats.
//
// Swallowing it would leave a file this run created with nothing saying it
// can be removed again, which is the one piece of information only the
// forward run was ever in a position to write down.
func TestTouch_ReportsAFailureToRecordTheInverse(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	rc.failKey = sdk.StatInverse
	path := filepath.Join(t.TempDir(), "unrecordable")

	_, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err == nil {
		t.Fatal("a failure to record the inverse was swallowed")
	}
	if !errors.Is(err, errTouchStat) {
		t.Errorf("error = %v, want it to wrap the recording failure", err)
	}
	// The diff got through first, which is what proves this test reached
	// the inverse call site rather than the one before it.
	if _, recorded := rc.stats["diff"]; !recorded {
		t.Error("the diff was not recorded, so the two recording call sites ran in the wrong order")
	}
}

// TestTouch_ReportsAFailureToRecordTheStats covers the second recording
// call, which only a context that fails one chosen key can reach: a
// context failing every key would stop at the diff above.
func TestTouch_ReportsAFailureToRecordTheStats(t *testing.T) {
	server := startTouchServer(t)
	rc := newTouchContext(server)
	rc.failKey = "dest"
	path := filepath.Join(t.TempDir(), "unrecordable")

	_, err := file.Touch(context.Background(), rc, newTouchDevice(server), touchParams(path, nil))
	if err == nil {
		t.Fatal("a failure to record the stats was swallowed")
	}
	if !errors.Is(err, errTouchStat) {
		t.Errorf("error = %v, want it to wrap the recording failure", err)
	}
	// The diff went in first, so this really is the later call site.
	if _, ok := rc.stats["diff"]; !ok {
		t.Error("no diff was recorded, so this covered the diff's branch rather than the stats'")
	}
}
