package file_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
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

// These tests run file.directory against a REAL SSH server, in this
// process, that hands every command it receives to a real /bin/sh
// (pkg/remoteexec/remoteexectest). Neither half is a mock: the SSH side
// does a genuine key exchange and a genuine session channel, and the
// shell side is a shell, so mkdir -p, chmod, chown and stat behave the way
// they behave on a device.
//
// Every assertion about an effect is made against the FILESYSTEM with
// os.Stat or os.Lstat, not against what the method says about itself. The
// difference matters: a module that returned Changed true and did nothing
// would pass a test that only read its return value, and "the directory is
// there with mode 0750" is the thing a runbook author is actually buying.
//
// Every identifier here is prefixed with dir or directory on purpose. Six
// methods share package file, their tests share package file_test, and Go
// has no file-level scope, so an unprefixed newDevice here would collide
// with the next method's.

// errDirectoryStat is the failure a test injects into SetStat, to drive
// the branches where the work succeeded and recording it did not.
var errDirectoryStat = errors.New("recording the stat failed")

// dirServer is where the in-process SSH harness is listening, and the one
// credential it accepts.
type dirServer struct {
	host     string
	port     int
	username string
	password string
}

// startDirServer starts a real SSH server that runs every command through
// a real /bin/sh, and stops it when the test ends.
func startDirServer(t *testing.T) dirServer {
	return startDirServerWithSessionBudget(t, -1)
}

// startDirServerWithSessionBudget is startDirServer with a cap on how many
// session channels the server accepts before refusing every further one.
//
// It is how a test reaches the branches that only a connection failing
// PARTWAY through a task can produce. This method opens several sessions
// in a fixed order (the first stat, the mkdir, the chmod or chown, the
// re-read), so a budget picks exactly which one fails, and the refusal is
// a real protocol-level rejection rather than an injected Go error.
func startDirServerWithSessionBudget(t *testing.T, budget int) dirServer {
	t.Helper()

	// A negative budget is this file's spelling of "unlimited", and the
	// harness spells that as an absent limit.
	opts := remoteexectest.Options{}
	if budget >= 0 {
		opts.SessionLimit = remoteexectest.Limit(budget)
	}

	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	return dirServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// dirDevice is a target this method can act on: the shared
// pkg/inventory/inventorytest.Stub plus the accessors
// capability.SSHTransportCapable and capability.POSIXFileSystemCapable
// require, which that stub deliberately does not provide.
type dirDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *dirDevice) SSHHost() string  { return d.host }
func (d *dirDevice) SSHPort() int     { return d.port }
func (d *dirDevice) RootPath() string { return "/" }

// newDirStub builds the base inventory item the device double wraps.
func newDirStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "file-1",
		StubName:  "file-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newDirDevice builds the SSH-reachable target a test runs against.
func newDirDevice(server dirServer) *dirDevice {
	return &dirDevice{Stub: newDirStub(), host: server.host, port: server.port}
}

// newUnreachableDirDevice builds a target this method cannot reach at all:
// the bare stub, which implements InventoryItem and nothing else.
func newUnreachableDirDevice() inventory.InventoryItem {
	return newDirStub()
}

// dirContext is a minimal sdk.RunbookContext carrying a fixed secret set,
// standing in for the real one the composition root builds from the
// credential store (Walk tier) or the dispatch payload (Crawl tier).
type dirContext struct {
	secrets map[string]string
	stats   map[string]any

	// failKey, when set, makes SetStat fail for that one key. Naming the
	// key rather than failing everything is what lets a test reach the
	// SECOND recording branch, since failing the first would return before
	// the second ran.
	failKey string
}

func newDirContext(server dirServer) *dirContext {
	return &dirContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *dirContext) InjectSecrets() map[string]string { return c.secrets }

func (c *dirContext) SetStat(key string, value any) error {
	if c.failKey != "" && c.failKey == key {
		return errDirectoryStat
	}
	c.stats[key] = value
	return nil
}

func (c *dirContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// dirDiffHalf returns one half of the recorded diff stat, failing the test
// when the shape is not the one every state-changing method must record.
func dirDiffHalf(t *testing.T, rc *dirContext, half string) map[string]any {
	t.Helper()

	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("stats[%q] = %#v, want the before-and-after map every changing method records", sdk.StatDiff, rc.stats[sdk.StatDiff])
	}
	side, ok := diff[half].(map[string]any)
	if !ok {
		t.Fatalf("diff[%q] = %#v, want a map", half, diff[half])
	}
	return side
}

// dirParams is the parameter map every test starts from: the path, plus
// the host key opt-out the in-process harness needs because its key is
// generated fresh on every run and is in nobody's known_hosts.
func dirParams(path string) map[string]any {
	return map[string]any{
		"path":                          path,
		"insecure_skip_host_key_verify": true,
	}
}

// TestDirectory_Registered proves the method registered itself as
// implemented and declaring that it can be undone.
//
// The manifest deliberately does NOT name which method undoes this one,
// and there is nothing here to assert about that any more. Whether the
// undo is a removal or a restore of attributes depends on what the run
// found, so it is emitted per run and the two branches are proven by
// TestDirectory_CreatedEmitsARemoval and
// TestDirectory_AttributeChangeEmitsAPermissionsRestore below.
func TestDirectory_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.directory")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.directory")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, want true: a created directory can be removed and a changed mode can be put back")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says which of the two inverses a run emits, or when")
	}
}

// TestDirectory_CreatedEmitsARemoval proves the branch where this run made
// the directory: the undo is to remove it.
//
// The removal is deliberately emitted WITHOUT recurse. What this run
// created was an empty directory, so removing an empty directory is its
// exact inverse; if something has since been put inside, file.remove
// refuses rather than destroying content this run never created. Asserting
// the parameter is absent is the only way to catch a future edit that
// "helpfully" adds it.
func TestDirectory_CreatedEmitsARemoval(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	path := filepath.Join(t.TempDir(), "made-here")

	result, err := file.Directory(context.Background(), rc, newDirDevice(server), dirParams(path))
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}
	if !result.Changed {
		t.Fatal("creating a directory reported no change")
	}

	fqcn, params, description := fileInverse(t, rc.stats)
	if fqcn != "file.remove" {
		t.Errorf("the inverse names %q, want file.remove: this run created the directory", fqcn)
	}
	if got := params["path"]; got != path {
		t.Errorf("the inverse would remove %v, want %q", got, path)
	}
	if _, present := params["recurse"]; present {
		t.Error("the inverse sets recurse, so a rollback would delete whatever had been put inside the directory since")
	}
	// The run really did create it, which is what makes a removal the right
	// undo. Reading it back out of the record rather than assuming it.
	if got := dirDiffHalf(t, rc, "before")["exists"]; got != false {
		t.Errorf("diff.before.exists = %v, want false: this test did not exercise the create branch at all", got)
	}
	if !strings.Contains(description, path) {
		t.Errorf("description = %q, want it to name the directory a rollback would remove", description)
	}
}

// TestDirectory_AttributeChangeEmitsAPermissionsRestore proves the branch
// that matters most, and the one a static declaration could never express.
//
// The directory was already there and this run only tightened its mode. An
// undo must put the mode back and MUST NOT remove the path: removing it
// would destroy a directory the run never created, along with everything
// under it. Same method, same parameters, different inverse from the test
// above, decided entirely by what the run found.
func TestDirectory_AttributeChangeEmitsAPermissionsRestore(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	path := filepath.Join(t.TempDir(), "already-here")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	params := dirParams(path)
	params["mode"] = "0755"

	result, err := file.Directory(context.Background(), rc, newDirDevice(server), params)
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}
	if !result.Changed {
		t.Fatal("a mode that differed reported no change")
	}

	fqcn, inverse, description := fileInverse(t, rc.stats)
	if fqcn == "file.remove" {
		t.Fatalf("the inverse names file.remove for a directory this run did not create: rolling back would delete %q "+
			"and everything under it", path)
	}
	if fqcn != "file.permissions" {
		t.Errorf("the inverse names %q, want file.permissions", fqcn)
	}

	// Read the expected values out of the recorded prior state rather than
	// writing them in, so the assertion proves the inverse carries what the
	// run observed.
	before := dirDiffHalf(t, rc, "before")
	for _, key := range []string{"mode", "owner", "group"} {
		if got, want := inverse[key], before[key]; got != want {
			t.Errorf("the inverse would set %s to %v, want %v, which is what the run found", key, got, want)
		}
	}
	if got := inverse["mode"]; got != "0700" {
		t.Errorf("the inverse would set the mode to %v, want 0700: it carries the mode this run APPLIED, not the one it found", got)
	}
	if got := inverse["path"]; got != path {
		t.Errorf("the inverse points at %v, want %q", got, path)
	}
	if !strings.Contains(description, path) {
		t.Errorf("description = %q, want it to name the directory", description)
	}
}

// TestDirectory_ConvergedRunEmitsNoInverse proves a run that found the
// directory already correct records nothing to undo.
//
// The absence is the statement. An inverse emitted here would tell a
// rollback to chmod a directory this run left exactly as it found it.
func TestDirectory_ConvergedRunEmitsNoInverse(t *testing.T) {
	server := startDirServer(t)
	path := filepath.Join(t.TempDir(), "settled")

	params := dirParams(path)
	params["mode"] = "0755"

	first := newDirContext(server)
	if _, err := file.Directory(context.Background(), first, newDirDevice(server), params); err != nil {
		t.Fatalf("first Directory: %v", err)
	}

	rc := newDirContext(server)
	result, err := file.Directory(context.Background(), rc, newDirDevice(server), params)
	if err != nil {
		t.Fatalf("second Directory: %v", err)
	}
	if result.Changed {
		t.Fatal("the second run reported a change, so it never reached the converged branch")
	}
	assertNoFileInverse(t, rc.stats)

	// The first run DID emit one, which is what proves the second run's
	// silence is a decision rather than a method that never emits anything.
	if _, emitted := first.stats[sdk.StatInverse]; !emitted {
		t.Error("the creating run emitted no inverse, so this test proves nothing about the converged one")
	}
}

// TestDirectory_InverseRecordingFailureIsReported proves a refusal to
// record the inverse fails the task rather than being swallowed, which
// would leave a real change with no way to undo it.
func TestDirectory_InverseRecordingFailureIsReported(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	rc.failKey = sdk.StatInverse

	_, err := file.Directory(context.Background(), rc, newDirDevice(server), dirParams(filepath.Join(t.TempDir(), "made")))
	if err == nil {
		t.Fatal("a failure to record the inverse was swallowed")
	}
	if !errors.Is(err, errDirectoryStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
}

// TestDirectory_RefusesAMissingPath proves the one required parameter is
// really required, and that the refusal happens before any connection.
//
// A nil device would fail inside Connect with a different message, and a
// nil context would panic there, so reaching a "path is required" error
// with both nil is itself the proof that nothing tried to dial.
func TestDirectory_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{nil, {}, {"path": ""}, {"mode": "0755"}} {
		_, err := file.Directory(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestDirectory_RefusesAModeItCannotCompare covers the two mode forms that
// would apply cleanly and then make this method report a change forever.
//
// The unquoted case is the one that bites in practice: YAML reads 0755 as
// the number 493, so an implementation using an ordinary string reader
// sees no mode at all and silently applies none. Every case here refuses
// before connecting, which the nil device again proves.
func TestDirectory_RefusesAModeItCannotCompare(t *testing.T) {
	tests := []struct {
		name string
		mode any
		want string
	}{
		{name: "unquoted octal became a number", mode: 493, want: "must be a quoted string"},
		{name: "float, as it arrives across the runner's JSON boundary", mode: 493.0, want: "must be a quoted string"},
		{name: "symbolic", mode: "u+rwx", want: "is not an octal mode"},
		{name: "a digit that is not octal", mode: "0798", want: "is not an octal mode"},
		{name: "too short", mode: "75", want: "is not an octal mode"},
		{name: "too long", mode: "07555", want: "is not an octal mode"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := dirParams("/tmp/does-not-matter")
			params["mode"] = tt.mode

			_, err := file.Directory(context.Background(), nil, nil, params)
			if err == nil {
				t.Fatalf("mode %v: expected a refusal, got nil", tt.mode)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestDirectory_RefusesANumericOwnerOrGroup covers the same idempotence
// argument for ownership: stat reports a name, so a uid could never
// compare equal and would chown on every run.
func TestDirectory_RefusesANumericOwnerOrGroup(t *testing.T) {
	for _, key := range []string{"owner", "group"} {
		params := dirParams("/tmp/does-not-matter")
		params[key] = 1000

		_, err := file.Directory(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("%s: expected a refusal, got nil", key)
		}
		if !strings.Contains(err.Error(), key+" must be a name") {
			t.Errorf("error = %q, want it to say %s takes a name", err, key)
		}
	}
}

// TestDirectory_CreatesTheDirectoryAndItsParents proves the create path,
// on the filesystem, including the mkdir -p behavior Ansible has and this
// method matches rather than putting behind a parameter.
func TestDirectory_CreatesTheDirectoryAndItsParents(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	path := filepath.Join(t.TempDir(), "one", "two", "three")

	result, err := file.Directory(context.Background(), rc, newDirDevice(server), dirParams(path))
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}
	if !result.Changed {
		t.Error("creating a directory reported no change")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v, so the directory was never created", path, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", path)
	}
	// The intermediate level, which only mkdir -p produces.
	if parent, err := os.Stat(filepath.Dir(path)); err != nil || !parent.IsDir() {
		t.Errorf("the parent %s was not created: %v", filepath.Dir(path), err)
	}

	if got := rc.stats["path"]; got != path {
		t.Errorf("stats[path] = %v, want %q", got, path)
	}
	if got := dirDiffHalf(t, rc, "before")["exists"]; got != false {
		t.Errorf("diff.before.exists = %v, want false", got)
	}
	if got := dirDiffHalf(t, rc, "after")["exists"]; got != true {
		t.Errorf("diff.after.exists = %v, want true", got)
	}
	if got := dirDiffHalf(t, rc, "after")["kind"]; got != "directory" {
		t.Errorf("diff.after.kind = %v, want %q", got, "directory")
	}
}

// TestDirectory_AppliesTheModeOnCreate proves the mode reaches the
// filesystem rather than being recorded and forgotten.
//
// 0750 deliberately: it is not what a default umask leaves behind, so a
// method that created the directory and never chmodded it would fail this.
func TestDirectory_AppliesTheModeOnCreate(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	path := filepath.Join(t.TempDir(), "private")

	params := dirParams(path)
	params["mode"] = "0750"

	if _, err := file.Directory(context.Background(), rc, newDirDevice(server), params); err != nil {
		t.Fatalf("Directory: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o750 {
		t.Errorf("mode on disk = %04o, want %04o", got, 0o750)
	}
	if got := dirDiffHalf(t, rc, "after")["mode"]; got != "0750" {
		t.Errorf("diff.after.mode = %v, want %q: the after half is read back from the device", got, "0750")
	}
}

// TestDirectory_ConvergedRunReportsNoChange is the single most important
// property this method has, so it asserts it three ways: the second run
// reports no change, the mode on disk did not move, and the diff records
// the two halves as equal rather than recording nothing.
//
// The owner and group for the second run are taken from what the first run
// read back, so this also proves ownership is COMPARED rather than always
// applied, without needing a second account on the machine running the
// test.
func TestDirectory_ConvergedRunReportsNoChange(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	path := filepath.Join(t.TempDir(), "stable")

	params := dirParams(path)
	params["mode"] = "0755"

	if _, err := file.Directory(context.Background(), rc, newDirDevice(server), params); err != nil {
		t.Fatalf("first Directory: %v", err)
	}

	after := dirDiffHalf(t, rc, "after")
	second := dirParams(path)
	second["mode"] = "0755"
	second["owner"] = after["owner"]
	second["group"] = after["group"]

	rc2 := newDirContext(server)
	result, err := file.Directory(context.Background(), rc2, newDirDevice(server), second)
	if err != nil {
		t.Fatalf("second Directory: %v", err)
	}
	if result.Changed {
		t.Error("the second run reported a change, so this method is not idempotent")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("mode on disk = %04o, want %04o", got, 0o755)
	}

	before2 := dirDiffHalf(t, rc2, "before")
	after2 := dirDiffHalf(t, rc2, "after")
	for _, key := range []string{"exists", "kind", "mode", "owner", "group"} {
		if before2[key] != after2[key] {
			t.Errorf("diff.before[%q] = %v but diff.after[%q] = %v: an unchanged run must record the two halves equal",
				key, before2[key], key, after2[key])
		}
	}
	if before2["exists"] != true {
		t.Errorf("diff.before.exists = %v, want true: the second run found the directory already there", before2["exists"])
	}
}

// TestDirectory_ChangesTheModeThatDiffers covers the attribute-change path
// on a directory that already exists, and then proves the run after it
// converges.
func TestDirectory_ChangesTheModeThatDiffers(t *testing.T) {
	server := startDirServer(t)
	path := filepath.Join(t.TempDir(), "tightened")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	params := dirParams(path)
	params["mode"] = "0755"

	rc := newDirContext(server)
	result, err := file.Directory(context.Background(), rc, newDirDevice(server), params)
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}
	if !result.Changed {
		t.Error("a mode that differed reported no change")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("mode on disk = %04o, want %04o", got, 0o755)
	}
	if got := dirDiffHalf(t, rc, "before")["mode"]; got != "0700" {
		t.Errorf("diff.before.mode = %v, want %q: the prior mode is what an inverse would restore", got, "0700")
	}

	rc2 := newDirContext(server)
	again, err := file.Directory(context.Background(), rc2, newDirDevice(server), params)
	if err != nil {
		t.Fatalf("second Directory: %v", err)
	}
	if again.Changed {
		t.Error("the run after the mode change reported a change")
	}
}

// TestDirectory_RefusesAFile proves the refusal that keeps this method
// from destroying data: something that is not a directory is reported, not
// replaced.
//
// The file's contents are read back afterward, because "the task failed"
// and "the task failed and the file is still there" are different claims
// and only the second one is worth anything.
func TestDirectory_RefusesAFile(t *testing.T) {
	server := startDirServer(t)
	path := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(path, []byte("do not lose me"), 0o600); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}

	_, err := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
	if err == nil {
		t.Fatal("a regular file at the path was not refused")
	}
	if !strings.Contains(err.Error(), "is a file") {
		t.Errorf("error = %q, want it to name what is actually there", err)
	}

	content, err := os.ReadFile(path)
	if err != nil || string(content) != "do not lose me" {
		t.Errorf("the file was damaged: content = %q, err = %v", content, err)
	}
}

// TestDirectory_RefusesASymlinkEvenToADirectory is the case a naive
// implementation gets wrong.
//
// `test -d` follows a symlink and answers yes, so a method built on it
// would decide the directory already exists and then chmod the LINK's
// TARGET, a path the runbook never named. Here the target keeps its own
// mode, which is what proves nothing followed the link.
func TestDirectory_RefusesASymlinkEvenToADirectory(t *testing.T) {
	server := startDirServer(t)
	root := t.TempDir()
	target := filepath.Join(root, "real")
	link := filepath.Join(root, "link")

	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("creating %s: %v", target, err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("linking %s: %v", link, err)
	}

	params := dirParams(link)
	params["mode"] = "0700"

	_, err := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), params)
	if err == nil {
		t.Fatal("a symlink at the path was not refused")
	}
	if !strings.Contains(err.Error(), "a symlink to") {
		t.Errorf("error = %q, want it to say a symlink is there", err)
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error = %q, want it to name where the link points", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("os.Stat(%s): %v", target, err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("the link's target was modified: mode = %04o, want the untouched %04o", got, 0o755)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("the link itself was removed: %v", err)
	}
}

// TestDirectory_RefusesSomethingThatIsNeither covers the third kind a path
// can hold. A unix socket is the cheapest one to make from a test, and it
// exercises the same branch a device node or a fifo would.
func TestDirectory_RefusesSomethingThatIsNeither(t *testing.T) {
	server := startDirServer(t)
	path := filepath.Join(t.TempDir(), "socket")

	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("creating a socket at %s: %v", path, err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	_, err = file.Directory(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
	if err == nil {
		t.Fatal("a socket at the path was not refused")
	}
	if !strings.Contains(err.Error(), "not a regular file, a directory or a symlink") {
		t.Errorf("error = %q, want it to say what is there is none of the three kinds it knows", err)
	}
}

// The remaining tests cover this method's failure paths. Each is a
// separate call site, and a swallowed error at any of them turns a task
// that failed on the device into one that reported success.

// TestDirectory_RefusesAnUnreachableDevice covers the connect failure
// path, with a device that declares no SSH transport at all.
func TestDirectory_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := file.Directory(context.Background(), newDirContext(dirServer{}), newUnreachableDirDevice(), dirParams("/tmp/anywhere"))
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestDirectory_StatFailureIsReported covers the first read: a device that
// authenticates and then cannot open a session must fail the task rather
// than be treated as "nothing is there", which would create a directory on
// top of whatever the read could not see.
func TestDirectory_StatFailureIsReported(t *testing.T) {
	server := startDirServerWithSessionBudget(t, 0)
	path := filepath.Join(t.TempDir(), "unreadable")

	_, err := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
	if err == nil {
		t.Fatal("a failed read was treated as an absent path")
	}
	if !strings.Contains(err.Error(), "stat") {
		t.Errorf("error = %q, want it to name the read that failed", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the directory was created even though the read failed")
	}
}

// TestDirectory_CreateFailureIsReported covers a real mkdir refusal: a
// parent that is a regular file, which is a shape a runbook typo produces
// routinely.
func TestDirectory_CreateFailureIsReported(t *testing.T) {
	server := startDirServer(t)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("creating %s: %v", blocker, err)
	}

	_, err := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), dirParams(filepath.Join(blocker, "child")))
	if err == nil {
		t.Fatal("a failed mkdir was reported as success")
	}
	if !strings.Contains(err.Error(), "create directory") {
		t.Errorf("error = %q, want it to say the directory could not be created", err)
	}
}

// TestDirectory_AttributeFailureOnAnExistingDirectoryIsReported covers the
// apply path's error branch. A user name nothing on the machine can
// resolve is a real chown refusal, and it fails the same way whether the
// test runs as root or not.
func TestDirectory_AttributeFailureOnAnExistingDirectoryIsReported(t *testing.T) {
	server := startDirServer(t)
	path := t.TempDir()

	params := dirParams(path)
	params["owner"] = "pleiades-no-such-user"

	_, err := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), params)
	if err == nil {
		t.Fatal("a failed chown was reported as success")
	}
	if !strings.Contains(err.Error(), "chown") {
		t.Errorf("error = %q, want it to name the operation that failed", err)
	}
	// The message must NOT claim a creation that did not happen.
	if strings.Contains(err.Error(), "created") {
		t.Errorf("error = %q, want no claim that the directory was created", err)
	}
}

// TestDirectory_AttributeFailureAfterCreateSaysSo covers the other half of
// that branch, and the reason the two are separate.
//
// The directory really is on the device when this fails, so an operator
// reading only "chown failed" would be told the task did nothing when it
// half-did something. The filesystem check is what makes that a fact
// rather than a claim about the error string.
func TestDirectory_AttributeFailureAfterCreateSaysSo(t *testing.T) {
	server := startDirServer(t)
	path := filepath.Join(t.TempDir(), "half-done")

	params := dirParams(path)
	params["owner"] = "pleiades-no-such-user"

	_, err := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), params)
	if err == nil {
		t.Fatal("a failed chown after a create was reported as success")
	}
	if !strings.Contains(err.Error(), "created "+path) {
		t.Errorf("error = %q, want it to say the directory was created before the attributes failed", err)
	}

	info, statErr := os.Stat(path)
	if statErr != nil || !info.IsDir() {
		t.Errorf("the error claimed the directory was created, but it is not there: %v", statErr)
	}
}

// TestDirectory_ReReadFailureIsReported covers the read-back after a
// change. Three sessions are needed on the create path (the first stat,
// the mkdir, the re-read), so a budget of two lets the work land and fails
// only the last one.
func TestDirectory_ReReadFailureIsReported(t *testing.T) {
	server := startDirServerWithSessionBudget(t, 2)
	path := filepath.Join(t.TempDir(), "made-then-unreadable")

	_, err := file.Directory(context.Background(), newDirContext(server), newDirDevice(server), dirParams(path))
	if err == nil {
		t.Fatal("a failed read-back was reported as success")
	}
	if !strings.Contains(err.Error(), "stat") {
		t.Errorf("error = %q, want it to name the read that failed", err)
	}
	// Proof the failure really was the LAST session rather than the first:
	// the mkdir in between happened.
	if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
		t.Errorf("the directory was not created, so the budget failed an earlier session than intended: %v", statErr)
	}
}

// TestDirectory_PathStatFailureIsReported covers the branch where the work
// succeeded and recording which path it touched did not.
func TestDirectory_PathStatFailureIsReported(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	rc.failKey = "path"

	_, err := file.Directory(context.Background(), rc, newDirDevice(server), dirParams(filepath.Join(t.TempDir(), "recorded")))
	if err == nil {
		t.Fatal("a failure to record the path was swallowed")
	}
	if !errors.Is(err, errDirectoryStat) {
		t.Errorf("error = %q, want it to wrap the recording failure", err)
	}
}

// TestDirectory_DiffRecordingFailureIsReported covers the same shape one
// step later, on the record every changing method owes a future rollback.
func TestDirectory_DiffRecordingFailureIsReported(t *testing.T) {
	server := startDirServer(t)
	rc := newDirContext(server)
	rc.failKey = sdk.StatDiff

	_, err := file.Directory(context.Background(), rc, newDirDevice(server), dirParams(filepath.Join(t.TempDir(), "recorded")))
	if err == nil {
		t.Fatal("a failure to record the diff was swallowed")
	}
	if !errors.Is(err, errDirectoryStat) {
		t.Errorf("error = %q, want it to wrap the recording failure", err)
	}
}
