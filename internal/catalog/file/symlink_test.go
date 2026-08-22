package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These tests run against a real SSH server, in this process, that hands
// every command it receives to a real /bin/sh on this machine. Neither
// half is a mock: the SSH side is golang.org/x/crypto/ssh doing a genuine
// key exchange and a genuine session channel, and the shell side runs the
// actual stat, readlink and ln against the actual filesystem.
//
// That is what makes the assertions worth anything under RULE 0. Every
// one of them checks the FILESYSTEM afterward, with os.Lstat, os.Readlink
// and os.ReadFile, rather than checking what the method said about
// itself. A method can return Changed false and still have moved a link;
// only the filesystem settles it.
//
// Every identifier below carries a symlink prefix because package file
// holds one test file per method and they all compile into one test
// package, so a bare newDevice or stubContext would collide with a
// sibling's.

// errSymlinkStat is the failure a test injects to drive the branch where
// the work succeeded and recording it did not.
var errSymlinkStat = errors.New("stat recording refused")

// symlinkDevice is the target these tests run against: the shared
// inventorytest.Stub plus the accessors capability.SSHTransportCapable
// and capability.POSIXFileSystemCapable require, which together are the
// shape a real POSIX server presents.
type symlinkDevice struct {
	*inventorytest.Stub
	host string
	port int
}

// SSHHost returns where the harness is listening.
func (d *symlinkDevice) SSHHost() string { return d.host }

// SSHPort returns the harness's port.
func (d *symlinkDevice) SSHPort() int { return d.port }

// RootPath satisfies capability.POSIXFileSystemCapable, the capability
// this method's manifest requires.
func (d *symlinkDevice) RootPath() string { return "/" }

// newSymlinkStub builds the base inventory item the device wraps.
func newSymlinkStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "file-1",
		StubName:  "file-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newSymlinkDevice builds an SSH-reachable target pointed at the harness.
func newSymlinkDevice(server *remoteexectest.Server) *symlinkDevice {
	return &symlinkDevice{Stub: newSymlinkStub(), host: server.Host, port: server.Port}
}

// newSymlinkUnreachableDevice builds a target this method cannot reach at
// all: the bare stub, which implements InventoryItem and nothing else.
func newSymlinkUnreachableDevice() inventory.InventoryItem { return newSymlinkStub() }

// symlinkContext is a minimal sdk.RunbookContext carrying a fixed secret
// set, standing in for the real one the composition root builds from the
// credential store on the Crawl tier or from the dispatch payload on the
// Walk tier.
type symlinkContext struct {
	secrets map[string]string
	stats   map[string]any

	// statErr, when set, makes SetStat fail, which is the only way to
	// reach the branches where the link was written and recording it was
	// refused.
	statErr error

	// failKey narrows statErr to ONE key. Left empty, the failure lands on
	// whichever recording call comes first, which is the diff; this method
	// records the inverse afterward, and a context that failed everything
	// could never reach that second call site to prove its error is
	// reported rather than swallowed.
	failKey string
}

// InjectSecrets returns the harness credential.
func (c *symlinkContext) InjectSecrets() map[string]string { return c.secrets }

// SetStat records a stat, or fails when the test asked it to.
func (c *symlinkContext) SetStat(key string, value any) error {
	if c.statErr != nil && (c.failKey == "" || c.failKey == key) {
		return c.statErr
	}
	c.stats[key] = value
	return nil
}

// EmitFact routes to SetStat, since nothing here distinguishes the two.
func (c *symlinkContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// startSymlinkServer brings up the harness with no session limit and
// stops it when the test ends.
func startSymlinkServer(t *testing.T) *remoteexectest.Server {
	t.Helper()
	return startSymlinkServerWithSessionBudget(t, -1)
}

// startSymlinkServerWithSessionBudget is startSymlinkServer with a cap on
// how many session channels the server accepts before refusing every
// further one.
//
// The budget is how these tests prove a NEGATIVE that no filesystem check
// can prove on its own: that a converged run sent no ln. A refused
// channel is a real protocol-level rejection, so a method that tried one
// more command than it should gets the same failure a device under
// session pressure would hand it.
func startSymlinkServerWithSessionBudget(t *testing.T, budget int) *remoteexectest.Server {
	t.Helper()

	opts := remoteexectest.Options{}
	if budget >= 0 {
		opts.SessionLimit = remoteexectest.Limit(budget)
	}

	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// newSymlinkContext builds a context holding the harness credential.
func newSymlinkContext(server *remoteexectest.Server) *symlinkContext {
	return &symlinkContext{secrets: server.Secrets(), stats: map[string]any{}}
}

// symlinkParams builds the params map a run needs, with host key
// verification skipped because the harness generates a fresh host key per
// test and there is no known_hosts entry for it.
func symlinkParams(src, path string) map[string]any {
	return map[string]any{
		"src":                           src,
		"path":                          path,
		"insecure_skip_host_key_verify": true,
	}
}

// symlinkDiffHalf pulls one half of the recorded diff stat out of the
// context, failing the test when the shape is not what RecordDiff writes.
func symlinkDiffHalf(t *testing.T, rc *symlinkContext, half string) map[string]any {
	t.Helper()

	raw, ok := rc.stats["diff"]
	if !ok {
		t.Fatal("no diff stat was recorded, so nothing could roll this task back")
	}
	diff, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("diff stat is %T, want a map", raw)
	}
	side, ok := diff[half].(map[string]any)
	if !ok {
		t.Fatalf("diff[%q] is %T, want a map", half, diff[half])
	}
	return side
}

// TestSymlink_Registered proves the method registered itself as
// implemented, with the inverse a rollback engine will read.
//
// The captures are pinned by name rather than merely counted. They are a
// contract with remotefile.Info.Map's keys, and a rename on either side
// that nothing noticed would leave a rollback reading a key that is not
// there and concluding the path never existed.
func TestSymlink_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.symlink")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.symlink")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, want true: a created link can be removed and a repointed one can be pointed back")
	}
	// The notes are where the two different inverses get explained, so
	// silence here leaves an operator no way to know that rolling this back
	// sometimes removes a link and sometimes does not.
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says which inverse a run emits, or when")
	}
}

// TestSymlink_CreatedEmitsARemoval proves the branch where there was
// nothing at the path: this run made the link, so the undo removes it.
func TestSymlink_CreatedEmitsARemoval(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	target := filepath.Join(dir, "release-1")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "current")

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(target, link))
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if !result.Changed {
		t.Fatal("creating a link reported no change")
	}

	fqcn, params, description := fileInverse(t, rc.stats)
	if fqcn != "file.remove" {
		t.Errorf("the inverse names %q, want file.remove: this run created the link", fqcn)
	}
	if got := params["path"]; got != link {
		t.Errorf("the inverse would remove %v, want the link %q", got, link)
	}
	// The removal must name the LINK, never what it points at. file.remove
	// deletes a link as the link, so naming the target here would delete the
	// release directory this run only pointed at.
	if got := params["path"]; got == target {
		t.Errorf("the inverse would remove %q, which is what the link points AT, not the link this run created", target)
	}
	if got := symlinkDiffHalf(t, rc, "before")["exists"]; got != false {
		t.Errorf("diff.before.exists = %v, want false: this test did not exercise the create branch", got)
	}
	if !strings.Contains(description, link) {
		t.Errorf("description = %q, want it to name the link a rollback would remove", description)
	}
}

// TestSymlink_RepointedEmitsALinkBackToTheOldTarget proves the branch a
// static declaration could never express.
//
// The link was already there and this run only moved where it points. The
// undo points it back; it must NOT remove the link, which existed before
// this run and is not this run's to delete. Same method, same parameters,
// a different inverse from the test above purely because of what the run
// found.
func TestSymlink_RepointedEmitsALinkBackToTheOldTarget(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	old := filepath.Join(dir, "release-1")
	current := filepath.Join(dir, "release-2")
	for _, path := range []string{old, current} {
		if err := os.Mkdir(path, 0o750); err != nil {
			t.Fatalf("creating the fixture %s: %v", path, err)
		}
	}
	link := filepath.Join(dir, "current")
	if err := os.Symlink(old, link); err != nil {
		t.Fatalf("creating the fixture link: %v", err)
	}

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(current, link))
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if !result.Changed {
		t.Fatal("repointing a link reported no change")
	}

	fqcn, params, description := fileInverse(t, rc.stats)
	if fqcn == "file.remove" {
		t.Fatalf("the inverse names file.remove for a link this run did not create: rolling back would delete %q", link)
	}
	if fqcn != "file.symlink" {
		t.Errorf("the inverse names %q, want file.symlink", fqcn)
	}

	// The old target is read out of the recorded prior state, so this proves
	// the inverse carries what the run observed rather than what the test
	// arranged.
	before := symlinkDiffHalf(t, rc, "before")
	if got, want := params["src"], before["target"]; got != want {
		t.Errorf("the inverse would point the link at %v, want %v, which is where the run found it pointing", got, want)
	}
	if got := params["src"]; got != old {
		t.Errorf("the inverse would point the link at %v, want %q: it carries the target this run APPLIED, not the one it found", got, old)
	}
	if got := params["path"]; got != link {
		t.Errorf("the inverse would write the link at %v, want %q", got, link)
	}
	if !strings.Contains(description, old) {
		t.Errorf("description = %q, want it to name the target the link would go back to", description)
	}
}

// TestSymlink_ConvergedRunEmitsNoInverse proves a run that found the link
// already pointing at the target records nothing to undo.
//
// This run sent no ln at all, so there is nothing an undo could put back.
// The session budget is what makes that airtight: the run gets one session
// for its single stat probe and no second one for a command it must not
// send.
func TestSymlink_ConvergedRunEmitsNoInverse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "release-1")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "current")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the fixture link: %v", err)
	}

	server := startSymlinkServerWithSessionBudget(t, 2)
	rc := newSymlinkContext(server)

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(target, link))
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if result.Changed {
		t.Fatal("a link that already pointed at the target reported a change")
	}
	assertNoFileInverse(t, rc.stats)
}

// TestSymlink_RefusesAMissingSrc proves the target parameter is really
// required, and that the refusal happens before anything dials.
//
// The device is nil, so a method that connected first would fail with
// "no target device" instead. Asserting the message names src is what
// distinguishes the two.
func TestSymlink_RefusesAMissingSrc(t *testing.T) {
	for _, params := range []map[string]any{nil, {"path": "/tmp/link"}, {"src": "", "path": "/tmp/link"}} {
		_, err := file.Symlink(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "src is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestSymlink_RefusesAMissingPath proves the link's own path is required
// too, and that neither name for it was set.
func TestSymlink_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{{"src": "/opt/app/releases/1.0"}, {"src": "/opt/app/releases/1.0", "dest": ""}} {
		_, err := file.Symlink(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestSymlink_RefusesConflictingPathAndDest proves the alias is refused
// rather than resolved when the two names disagree.
//
// Picking one silently is the failure this guards: the link would land at
// a path the author never asked about, while the path they were reading
// in the runbook stayed untouched.
func TestSymlink_RefusesConflictingPathAndDest(t *testing.T) {
	_, err := file.Symlink(context.Background(), nil, nil, map[string]any{
		"src":  "/opt/app/releases/1.0",
		"path": "/opt/app/current",
		"dest": "/opt/app/previous",
	})
	if err == nil {
		t.Fatal("expected a task setting path and dest to different values to be refused")
	}
	if !strings.Contains(err.Error(), "/opt/app/current") || !strings.Contains(err.Error(), "/opt/app/previous") {
		t.Errorf("error = %q, want it to show both values so the author can see the conflict", err)
	}
}

// TestSymlink_CreatesTheLink covers the create path against a path with
// nothing at it.
func TestSymlink_CreatesTheLink(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	target := filepath.Join(dir, "release-1")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "current")

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(target, link))
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if !result.Changed {
		t.Error("creating a link that was not there reported no change")
	}

	// The filesystem, not the method's own claim about itself.
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("Lstat %s: %v", link, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is %v, want a symbolic link", link, info.Mode())
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink %s: %v", link, err)
	}
	if got != target {
		t.Errorf("%s points at %q, want %q", link, got, target)
	}

	before := symlinkDiffHalf(t, rc, "before")
	if before["exists"] != false {
		t.Errorf("diff.before[exists] = %v, want false: the path was empty", before["exists"])
	}
	if before["kind"] != "absent" {
		t.Errorf("diff.before[kind] = %v, want %q", before["kind"], "absent")
	}
	after := symlinkDiffHalf(t, rc, "after")
	if after["kind"] != "symlink" {
		t.Errorf("diff.after[kind] = %v, want %q", after["kind"], "symlink")
	}
	if after["target"] != target {
		t.Errorf("diff.after[target] = %v, want %q", after["target"], target)
	}
}

// TestSymlink_CreatesADanglingLink proves src is not required to exist,
// which is ln's own behavior and the behavior a deploy that links a
// directory it is about to populate depends on.
func TestSymlink_CreatesADanglingLink(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	target := filepath.Join(dir, "not-here-yet")
	link := filepath.Join(dir, "current")

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(target, link))
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if !result.Changed {
		t.Error("creating a dangling link reported no change")
	}
	// Lstat, not Stat: Stat follows the link and would report the missing
	// target rather than the link that is genuinely there.
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("Lstat %s: %v", link, err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink %s: %v", link, err)
	}
	if got != target {
		t.Errorf("%s points at %q, want %q", link, got, target)
	}
}

// TestSymlink_AcceptsDestAsAnAliasForPath proves a converted playbook
// that wrote dest works unchanged.
func TestSymlink_AcceptsDestAsAnAliasForPath(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	target := filepath.Join(dir, "tool")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "tool-link")

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), map[string]any{
		"src":                           target,
		"dest":                          link,
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if !result.Changed {
		t.Error("creating a link named by dest reported no change")
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink %s: %v", link, err)
	}
	if got != target {
		t.Errorf("%s points at %q, want %q", link, got, target)
	}
}

// TestSymlink_AcceptsPathAndDestWhenTheyAgree proves the alias check
// refuses a real conflict rather than any use of both names.
func TestSymlink_AcceptsPathAndDestWhenTheyAgree(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	target := filepath.Join(dir, "release-1")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "current")

	if _, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), map[string]any{
		"src":                           target,
		"path":                          link,
		"dest":                          link,
		"insecure_skip_host_key_verify": true,
	}); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink %s: %v", link, err)
	}
	if got != target {
		t.Errorf("%s points at %q, want %q", link, got, target)
	}
}

// TestSymlink_ConvergedRunSendsNoLn is the single most important property
// of this method, and the budget is what proves it.
//
// The link already points where the task asks, so the only commands the
// method may send are the stat probe and the readlink that goes with it.
// A budget of two grants exactly those. An implementation that ran ln
// anyway, which is the easy mistake because ln -sfn succeeds in this
// case, would be refused a third session and fail this test loudly rather
// than passing while reporting changed forever.
func TestSymlink_ConvergedRunSendsNoLn(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "release-1")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "current")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the fixture link: %v", err)
	}

	server := startSymlinkServerWithSessionBudget(t, 2)
	rc := newSymlinkContext(server)

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(target, link))
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if result.Changed {
		t.Error("a link that already pointed at the target reported a change")
	}

	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink %s: %v", link, err)
	}
	if got != target {
		t.Errorf("%s points at %q, want it left at %q", link, got, target)
	}

	// Both halves are recorded even though nothing moved, because an
	// absent diff cannot say "there was nothing to undo".
	before := symlinkDiffHalf(t, rc, "before")
	after := symlinkDiffHalf(t, rc, "after")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("diff before %v and after %v differ on a run that changed nothing", before, after)
	}
	if before["target"] != target {
		t.Errorf("diff.before[target] = %v, want %q", before["target"], target)
	}
}

// TestSymlink_RepointsAnExistingLink covers the attribute-change path: a
// link that is there but points somewhere else.
func TestSymlink_RepointsAnExistingLink(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	old := filepath.Join(dir, "release-1")
	fresh := filepath.Join(dir, "release-2")
	for _, d := range []string{old, fresh} {
		if err := os.Mkdir(d, 0o750); err != nil {
			t.Fatalf("creating the fixture: %v", err)
		}
	}
	link := filepath.Join(dir, "current")
	if err := os.Symlink(old, link); err != nil {
		t.Fatalf("creating the fixture link: %v", err)
	}

	result, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(fresh, link))
	if err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if !result.Changed {
		t.Error("repointing a link reported no change")
	}

	// The link itself must have moved, not a new link inside the
	// directory it used to point at, which is exactly what ln without -n
	// would have produced.
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink %s: %v", link, err)
	}
	if got != fresh {
		t.Errorf("%s points at %q, want %q", link, got, fresh)
	}
	if _, err := os.Lstat(filepath.Join(old, filepath.Base(fresh))); err == nil {
		t.Errorf("a link was created inside %s, so the existing link was followed instead of replaced", old)
	}

	before := symlinkDiffHalf(t, rc, "before")
	if before["target"] != old {
		t.Errorf("diff.before[target] = %v, want %q: a rollback needs the target it used to have", before["target"], old)
	}
	after := symlinkDiffHalf(t, rc, "after")
	if after["target"] != fresh {
		t.Errorf("diff.after[target] = %v, want %q", after["target"], fresh)
	}
}

// TestSymlink_RefusesARegularFile proves real content is never replaced,
// and proves it by reading the file back rather than by trusting the
// error.
func TestSymlink_RefusesARegularFile(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	target := filepath.Join(dir, "config.staging.yaml")
	if err := os.WriteFile(target, []byte("staging: true\n"), 0o600); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(link, []byte("the real configuration\n"), 0o600); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}

	_, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(target, link))
	if err == nil {
		t.Fatal("a regular file was replaced by a link instead of being refused")
	}
	if !strings.Contains(err.Error(), "not a symbolic link") {
		t.Errorf("error = %q, want it to say what was in the way", err)
	}

	content, err := os.ReadFile(link) // #nosec G304 -- a path this test created under t.TempDir
	if err != nil {
		t.Fatalf("ReadFile %s: %v", link, err)
	}
	if string(content) != "the real configuration\n" {
		t.Errorf("%s now holds %q, so the refusal did not actually protect it", link, content)
	}
}

// TestSymlink_RefusesADirectory is the case the refusal exists for.
// Replacing a directory with a link destroys everything under it, and
// nothing in this platform could put it back.
func TestSymlink_RefusesADirectory(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	target := filepath.Join(dir, "release-1")
	if err := os.Mkdir(target, 0o750); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	link := filepath.Join(dir, "current")
	if err := os.Mkdir(link, 0o750); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}
	inside := filepath.Join(link, "data.txt")
	if err := os.WriteFile(inside, []byte("irreplaceable\n"), 0o600); err != nil {
		t.Fatalf("creating the fixture: %v", err)
	}

	_, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(target, link))
	if err == nil {
		t.Fatal("a directory was replaced by a link instead of being refused")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error = %q, want it to name what was in the way", err)
	}

	content, err := os.ReadFile(inside) // #nosec G304 -- a path this test created under t.TempDir
	if err != nil {
		t.Fatalf("ReadFile %s: %v", inside, err)
	}
	if string(content) != "irreplaceable\n" {
		t.Errorf("%s now holds %q, so the directory's contents were not protected", inside, content)
	}
}

// TestSymlink_RefusesAnUnreachableDevice covers the connect failure path,
// with a device that implements no SSH transport at all.
func TestSymlink_RefusesAnUnreachableDevice(t *testing.T) {
	rc := &symlinkContext{secrets: map[string]string{}, stats: map[string]any{}}

	_, err := file.Symlink(context.Background(), rc, newSymlinkUnreachableDevice(), symlinkParams("/opt/a", "/opt/b"))
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestSymlink_StatFailureIsReported covers the branch where the
// connection authenticates and then cannot open the session the state
// read needs.
//
// A budget of zero is what that looks like from the client's side, and it
// is the shape a real device under session pressure produces. The method
// must fail rather than treat an unreadable path as an empty one, since
// treating it as empty is what would then write a link over something it
// never saw.
func TestSymlink_StatFailureIsReported(t *testing.T) {
	server := startSymlinkServerWithSessionBudget(t, 0)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	_, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server),
		symlinkParams(filepath.Join(dir, "target"), filepath.Join(dir, "link")))
	if err == nil {
		t.Fatal("a state read that never ran was reported as success")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
}

// TestSymlink_LinkFailureIsReported covers the branch where the device
// refuses the ln itself, here because the link's parent directory does
// not exist.
//
// This method creates a link and never the directories above it, so the
// device's own refusal is the right answer to report.
func TestSymlink_LinkFailureIsReported(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	link := filepath.Join(dir, "no-such-directory", "current")

	_, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(dir, link))
	if err == nil {
		t.Fatal("a failed ln was reported as success")
	}
	if !strings.Contains(err.Error(), "link "+link) {
		t.Errorf("error = %q, want it to name the link it could not create", err)
	}
}

// TestSymlink_ReadBackFailureIsReported covers the branch where the link
// was written and the confirming read could not run.
//
// The budget is three commands short by one: the state read takes one
// session against a path with nothing at it, the ln takes the second, and
// the read-back is refused. Reporting that as success would put an
// invented after state into the diff a rollback later reads.
func TestSymlink_ReadBackFailureIsReported(t *testing.T) {
	server := startSymlinkServerWithSessionBudget(t, 2)
	rc := newSymlinkContext(server)

	dir := t.TempDir()
	link := filepath.Join(dir, "current")

	_, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(dir, link))
	if err == nil {
		t.Fatal("a read-back that never ran was reported as success")
	}
	if !strings.Contains(err.Error(), "reading "+link+" back") {
		t.Errorf("error = %q, want it to say the read-back failed", err)
	}
	// The link really was written before the read-back failed, which is
	// why this branch cannot simply be folded into the ln failure above.
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Errorf("Lstat %s: %v, want the link the failed run had already created", link, statErr)
	}
}

// TestSymlink_DiffRecordingFailureIsReported covers the branch where the
// link was written and recording the before-and-after was refused.
//
// It must fail the task. A run whose diff never landed has left a change
// on the device that no journal knows about, and reporting success would
// hide exactly that.
func TestSymlink_DiffRecordingFailureIsReported(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)
	rc.statErr = errSymlinkStat

	dir := t.TempDir()
	link := filepath.Join(dir, "current")

	_, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(dir, link))
	if err == nil {
		t.Fatal("a failure to record the diff was swallowed")
	}
	if !errors.Is(err, errSymlinkStat) {
		t.Errorf("error = %q, want it to carry what the recording refused with", err)
	}
}

// TestSymlink_InverseRecordingFailureIsReported covers the same swallowing
// question at the second recording call site, which the diff test above
// can never reach because it fails before this one runs.
//
// A swallowed failure here is the worst of the three outcomes: the link
// really moved, the diff says so, and the one instruction that would move
// it back is missing without anything having reported a problem.
func TestSymlink_InverseRecordingFailureIsReported(t *testing.T) {
	server := startSymlinkServer(t)
	rc := newSymlinkContext(server)
	rc.statErr = errSymlinkStat
	rc.failKey = sdk.StatInverse

	dir := t.TempDir()
	link := filepath.Join(dir, "current")

	_, err := file.Symlink(context.Background(), rc, newSymlinkDevice(server), symlinkParams(dir, link))
	if err == nil {
		t.Fatal("a failure to record the inverse was swallowed")
	}
	if !errors.Is(err, errSymlinkStat) {
		t.Errorf("error = %q, want it to carry what the recording refused with", err)
	}
	// The diff got through, which is what proves the failure landed on the
	// later call rather than on the one the test above already covers.
	if _, recorded := rc.stats["diff"]; !recorded {
		t.Error("the diff was not recorded, so this test failed at the same call site as the diff test")
	}
}
