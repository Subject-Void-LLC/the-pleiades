package file_test

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/file"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These tests run file.permissions against a REAL SSH server, in this
// process, that hands every command it receives to a real /bin/sh on this
// machine (pkg/remoteexec/remoteexectest). Nothing about the transport,
// the shell, stat, chmod or chown is stubbed.
//
// That matters twice over here. RULE 0 says a test only counts when it
// runs the path the platform runs, and this method's entire subject is
// what a real stat reports and what a real chmod does with it. And every
// assertion below reads the FILESYSTEM back with os.Stat rather than
// believing the method's own report: a module that returned the right
// Changed flag while changing nothing would pass a test that only read
// its answer, and that failure is the exact one this whole namespace has
// to be proof against.
//
// Every helper in this file is prefixed "permissions" because the sibling
// methods in this package are being written alongside it and share one
// test package. The shared harness belongs in a common file the way
// internal/catalog/exec/sshd_test.go holds that namespace's, and moving
// these there is a merge step rather than a rewrite.

// errPermissionsStat is what a stub context returns when a test wants
// recording to fail, so the branches where the change landed and the
// record did not are reachable.
var errPermissionsStat = errors.New("recording the result failed")

// permissionsServer is where the in-process SSH harness is listening,
// plus the one credential it accepts.
type permissionsServer struct {
	host     string
	port     int
	username string
	password string
}

// startPermissionsServer starts a real SSH server that runs every command
// through a real /bin/sh, and stops it when the test ends.
func startPermissionsServer(t *testing.T) permissionsServer {
	return startPermissionsServerWithSessionBudget(t, -1)
}

// startPermissionsServerWithSessionBudget is startPermissionsServer with
// a cap on how many session channels it accepts before refusing.
//
// A budget of zero is the only way to reach the branch where the
// connection authenticates and then cannot carry the first probe, which
// is what a device under session pressure looks like from this side.
func startPermissionsServerWithSessionBudget(t *testing.T, budget int) permissionsServer {
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

	return permissionsServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// permissionsTarget is a device reachable over SSH: the shared
// pkg/inventory/inventorytest.Stub plus the two accessors
// capability.SSHTransportCapable requires, which that stub deliberately
// does not provide.
type permissionsTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *permissionsTarget) SSHHost() string { return d.host }
func (d *permissionsTarget) SSHPort() int    { return d.port }

// newPermissionsStub builds the base inventory item both device shapes
// wrap.
func newPermissionsStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "file-1",
		StubName:  "file-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NamePOSIXFileSystem},
		StubState: inventory.StateActive,
	}
}

// newPermissionsTarget builds the SSH-reachable device a test runs
// against.
func newPermissionsTarget(server permissionsServer) *permissionsTarget {
	return &permissionsTarget{Stub: newPermissionsStub(), host: server.host, port: server.port}
}

// newPermissionsUnreachable builds a device this method cannot reach at
// all: the bare stub, which implements InventoryItem and nothing else.
func newPermissionsUnreachable() inventory.InventoryItem {
	return newPermissionsStub()
}

// permissionsContext is a minimal sdk.RunbookContext carrying a fixed
// secret set, standing in for the real one the composition root builds
// from the credential store on the Walk tier or the dispatch payload on
// the Crawl tier.
type permissionsContext struct {
	secrets map[string]string
	stats   map[string]any

	// failOn, when set, makes SetStat fail for that one key. One key
	// rather than all of them because this method records under two
	// separate call sites, the diff and then the returned stats, and a
	// context that failed on everything could only ever reach the first
	// of them.
	failOn string
}

func newPermissionsContext(server permissionsServer) *permissionsContext {
	return &permissionsContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		stats:   map[string]any{},
	}
}

func (c *permissionsContext) InjectSecrets() map[string]string { return c.secrets }

func (c *permissionsContext) SetStat(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errPermissionsStat
	}
	c.stats[key] = value
	return nil
}

func (c *permissionsContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// permissionsDiffHalf returns one half of the recorded diff stat, failing
// the test when the method recorded nothing or recorded a shape a
// rollback engine could not read.
func permissionsDiffHalf(t *testing.T, rc *permissionsContext, half string) map[string]any {
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

// fileInverse reads the undo instruction a run emitted, and proves it is
// something a rollback engine could really dispatch.
//
// It carries no method prefix, unlike every other helper in this file, and
// that is deliberate: the five methods of this namespace emit inverses
// naming EACH OTHER, so an assertion about the emitted shape belongs to
// the namespace rather than to any one method. Copying it five times with
// five prefixes would let five copies disagree about what a runnable
// instruction is, which is the one thing they all have to agree on.
//
// The runnability check is the part worth having. An inverse is a task, so
// the test that matters is not "some map was recorded" but "this names a
// real implemented method and passes it parameters that method declares."
// A typo in an FQCN or in a parameter name produces a record that looks
// perfectly well formed and fails the first time anybody rolls back, which
// is the worst moment to discover it.
func fileInverse(t *testing.T, stats map[string]any) (fqcn string, params map[string]any, description string) {
	t.Helper()

	raw, emitted := stats[sdk.StatInverse]
	if !emitted {
		t.Fatal("no inverse was emitted, so nothing recorded how to undo this run")
	}
	record, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("the inverse stat is %T, want a map a rollback engine can decode", raw)
	}

	fqcn, ok = record["fqcn"].(string)
	if !ok || fqcn == "" {
		t.Fatalf("the inverse names no method, got %#v", record["fqcn"])
	}
	params, ok = record["params"].(map[string]any)
	if !ok {
		t.Fatalf("the inverse params are %T, want a map", record["params"])
	}
	description, _ = record["description"].(string)
	if description == "" {
		t.Error("the inverse carries no description, so an operator approving a rollback plan reads a bare FQCN and a parameter map")
	}

	assertFileInverseRunnable(t, fqcn, params)
	return fqcn, params, description
}

// assertFileInverseRunnable proves an emitted instruction could really be
// dispatched: it names a registered, implemented method, it passes only
// parameters that method declares, and it leaves none of that method's
// required ones out.
//
// Checking against the target's own Doc is what makes this catch a typo.
// Nothing else in the build compares an emitted parameter name against the
// method that would receive it, so "pth" instead of "path" would sit in
// the record looking correct until a rollback ran it.
func assertFileInverseRunnable(t *testing.T, fqcn string, params map[string]any) {
	t.Helper()

	target, found := collection.Lookup(fqcn)
	if !found {
		t.Fatalf("the inverse names %q, which no registered method answers to", fqcn)
	}
	if target.Manifest.Status != collection.StatusImplemented {
		t.Errorf("the inverse names %q, which is declared but not implemented, so running it would refuse", fqcn)
	}

	declared := make(map[string]collection.Param, len(target.Manifest.Doc.Params))
	for _, p := range target.Manifest.Doc.Params {
		declared[p.Name] = p
	}
	for name := range params {
		if _, ok := declared[name]; !ok {
			t.Errorf("the inverse passes %q to %s, which declares no such parameter", name, fqcn)
		}
	}
	for name, p := range declared {
		if !p.Required {
			continue
		}
		if value, ok := params[name]; !ok || value == "" {
			t.Errorf("the inverse omits %s's required %q, so a rollback engine could not run it", fqcn, name)
		}
	}
}

// assertNoFileInverse proves a run emitted NOTHING to undo.
//
// That absence is a real assertion rather than the lack of one. It is how
// the record says "this task changed nothing, so undoing it means doing
// nothing", and an inverse emitted anyway would make a rollback perform
// work the forward run never did.
func assertNoFileInverse(t *testing.T, stats map[string]any) {
	t.Helper()

	if raw, emitted := stats[sdk.StatInverse]; emitted {
		t.Errorf("this run emitted the inverse %#v, but it changed nothing that an undo could put back: "+
			"running that would be work the forward run never did", raw)
	}
}

// permissionsFileMode reads a path's permission bits straight from the
// filesystem, as four octal digits, which is the form the module reports.
func permissionsFileMode(t *testing.T, path string) string {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return "0" + strconv.FormatUint(uint64(info.Mode().Perm()), 8)
}

// permissionsFileIDs returns the numeric owner and group of a path.
func permissionsFileIDs(t *testing.T, path string) (int, int) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("this platform does not report POSIX owner and group ids")
	}
	return int(sys.Uid), int(sys.Gid)
}

// permissionsFileNames returns the owner and group NAMES of a path, which
// is what the module compares against and what a runbook writes.
func permissionsFileNames(t *testing.T, path string) (string, string) {
	t.Helper()

	uid, gid := permissionsFileIDs(t, path)
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

// permissionsOtherGroup names a group this process may hand path to,
// different from the one it already has, or "" when there is none.
//
// A real group change needs a real second group, and which ones are
// available depends on who is running the tests: root may use any, and
// anyone else may use a group they belong to. Both are searched so the
// test proving a chgrp really happens runs in as many environments as
// possible rather than only under root.
func permissionsOtherGroup(t *testing.T, path string) string {
	t.Helper()

	_, current := permissionsFileIDs(t, path)

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

// permissionsFile creates a file with exactly the mode given, defeating
// the process umask, and returns its path.
//
// os.WriteFile's own permission argument is masked, so a file asked for
// as 0600 can arrive as something else; the explicit chmod is what makes
// the starting state of every test below a known one.
func permissionsFile(t *testing.T, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(path, []byte("contents\n"), mode); err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	return path
}

// TestPermissions_Registered proves the method registered itself as
// implemented and declaring that it can be undone.
//
// Reversible is pinned rather than merely read. This method's whole effect
// is a triple of values on one path, so re-applying the triple it found
// undoes it exactly; silently becoming false later would tell an operator
// reading the reference that a mode change cannot be taken back, and would
// leave the emission below as code nothing claims to do.
func TestPermissions_Registered(t *testing.T) {
	d, ok := collection.Lookup("file.permissions")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "file.permissions")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if !d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = false, want true: re-applying the mode, owner and group it found restores the path exactly")
	}
	// Worth writing even for a reversible method, because what an inverse
	// does NOT restore is the thing an operator has to know before trusting
	// one, and here that is the partial-failure case.
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, so nothing says what the emitted inverse does and does not restore")
	}
}

// TestPermissions_EmitsTheInverseThatRestoresWhatWasThere is the worked
// example for this whole namespace: a run that changed something records a
// concrete, already-parameterized task that puts it back.
//
// The parameters are read out of the recorded diff rather than written
// into the assertion as literals. That is the property that actually
// matters: the inverse has to carry what this run OBSERVED, not what the
// test happened to set up, and comparing it against the captured before
// state is what proves the two come from the same read. A method that
// emitted the mode it applied rather than the mode it found would pass an
// assertion written against a literal only because the literal was chosen
// by someone who already knew the answer.
func TestPermissions_EmitsTheInverseThatRestoresWhatWasThere(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	path := permissionsFile(t, 0o600)

	result, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if !result.Changed {
		t.Fatal("the run reported no change, so there was nothing for it to emit an inverse for")
	}

	fqcn, params, description := fileInverse(t, rc.stats)
	if fqcn != "file.permissions" {
		t.Errorf("the inverse names %q, want file.permissions: this method undoes itself", fqcn)
	}

	// Every parameter has to equal what the run recorded seeing, which is
	// the prior state and nothing else.
	before := permissionsDiffHalf(t, rc, "before")
	for _, key := range []string{"mode", "owner", "group"} {
		if got, want := params[key], before[key]; got != want {
			t.Errorf("the inverse would set %s to %v, want %v, which is what the run found", key, got, want)
		}
	}
	if got := params["path"]; got != path {
		t.Errorf("the inverse points at %v, want %q", got, path)
	}
	// The mode is the one an undo would really apply, so pin it against the
	// filesystem too: 0600 was there before the run, not the 0644 it set.
	if got := params["mode"]; got != "0600" {
		t.Errorf("the inverse would set the mode to %v, want 0600: it carries the mode this run APPLIED, not the one it found", got)
	}
	if !strings.Contains(description, path) {
		t.Errorf("description = %q, want it to name the path an operator would be approving a change to", description)
	}
}

// TestPermissions_ConvergedRunEmitsNoInverse proves the absence, which is
// as much a part of the record as anything written into it.
//
// A run that found the path already correct changed nothing, so undoing it
// means doing nothing. An inverse emitted here would make a rollback chmod
// a path this run never touched, and it would do it while looking exactly
// like a legitimate record of a real change.
func TestPermissions_ConvergedRunEmitsNoInverse(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	path := permissionsFile(t, 0o644)

	result, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if result.Changed {
		t.Fatal("the run reported a change against a path that already had the mode it asked for")
	}
	assertNoFileInverse(t, rc.stats)

	// The diff is still recorded, and that is the distinction: a diff whose
	// halves match says "reached, nothing to do", while the missing inverse
	// says "undoing this means doing nothing". An absent diff would instead
	// say the task was never reached at all.
	if !reflect.DeepEqual(permissionsDiffHalf(t, rc, "before"), permissionsDiffHalf(t, rc, "after")) {
		t.Error("a converged run recorded two different diff halves")
	}
}

// TestPermissions_InverseRecordFailureIsReported proves a refusal to
// record the inverse fails the task rather than being swallowed.
//
// Swallowing it would leave a rollback engine with no way to undo a change
// that really happened, which is the same failure as not recording the
// diff and is silent in the same direction.
func TestPermissions_InverseRecordFailureIsReported(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	rc.failOn = sdk.StatInverse

	_, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          permissionsFile(t, 0o600),
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the inverse was swallowed")
	}
	if !errors.Is(err, errPermissionsStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
}

// TestPermissions_RefusesAMissingPath proves the one required parameter
// is really required, and that the refusal happens before any connection.
//
// A nil device would fail in connect, so reaching the end of this test
// with the expected message is itself the proof that nothing tried to
// dial.
func TestPermissions_RefusesAMissingPath(t *testing.T) {
	for _, params := range []map[string]any{
		nil,
		{"mode": "0644"},
		{"path": "", "mode": "0644"},
		{"path": nil, "mode": "0644"},
	} {
		_, err := file.Permissions(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "path is required") {
			t.Errorf("params %v: error = %q, want it to name the missing parameter", params, err)
		}
	}
}

// TestPermissions_RefusesATaskThatAsksForNothing proves a task naming a
// path and no attribute is a refusal rather than a quiet success.
//
// Treating it as a no-op would hide the mistake that actually produced
// it, which is nearly always a mode YAML swallowed or a parameter spelled
// wrongly, behind a green task.
func TestPermissions_RefusesATaskThatAsksForNothing(t *testing.T) {
	for _, params := range []map[string]any{
		{"path": "/etc/hosts"},
		{"path": "/etc/hosts", "mode": nil, "owner": nil, "group": nil},
		{"path": "/etc/hosts", "modes": "0644"},
	} {
		_, err := file.Permissions(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("params %v: expected a refusal, got nil", params)
		}
		if !strings.Contains(err.Error(), "at least one of mode, owner or group") {
			t.Errorf("params %v: error = %q, want it to say what to add", params, err)
		}
	}
}

// TestPermissions_RefusesAParameterThatIsNotText covers the single most
// common mistake anyone makes with Ansible's file module: writing
// mode: 0644 unquoted, which YAML reads as a number.
//
// The refusal has to name the parameter and say to quote it. The
// alternative, treating a non-string as absent the way sdk.StringParam
// does, would answer "at least one of mode, owner or group is required"
// and send the author hunting for a parameter they did write.
func TestPermissions_RefusesAParameterThatIsNotText(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{name: "path", params: map[string]any{"path": 42, "mode": "0644"}},
		{name: "mode", params: map[string]any{"path": "/etc/hosts", "mode": 420}},
		{name: "owner", params: map[string]any{"path": "/etc/hosts", "owner": 1000}},
		{name: "group", params: map[string]any{"path": "/etc/hosts", "group": 1000}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := file.Permissions(context.Background(), nil, nil, tt.params)
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

// TestPermissions_RefusesAModeItCannotCompare proves a mode that is not
// plain octal is refused up front.
//
// A symbolic mode is the case that matters. Applying u+x unconditionally
// is the only way to honor it without resolving it against the current
// bits, and that is a method that reports changed on every run forever,
// which is the one outcome this whole design exists to prevent.
func TestPermissions_RefusesAModeItCannotCompare(t *testing.T) {
	for _, mode := range []string{"u+x", "go-w", "0o644", "07777", "648", "rwx"} {
		params := map[string]any{"path": "/etc/hosts", "mode": mode}

		_, err := file.Permissions(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("mode %q: expected a refusal, got nil", mode)
		}
		if !strings.Contains(err.Error(), "octal digits") {
			t.Errorf("mode %q: error = %q, want it to say what a mode must look like", mode, err)
		}
	}
}

// TestPermissions_RefusesANumericOwnerOrGroup proves an id is refused
// where a name is required.
//
// chown reads an all-digit argument as an id and the device reports names
// back, so "1000" and "alice" would compare unequal on every run even
// when they are the same account. Both parameters are checked because
// each is its own call site, and a copied check can be wired to the wrong
// value.
func TestPermissions_RefusesANumericOwnerOrGroup(t *testing.T) {
	for _, key := range []string{"owner", "group"} {
		params := map[string]any{"path": "/etc/hosts", key: "1000"}

		_, err := file.Permissions(context.Background(), nil, nil, params)
		if err == nil {
			t.Fatalf("%s: expected a refusal, got nil", key)
		}
		if !strings.Contains(err.Error(), key+" \"1000\" is a numeric id") {
			t.Errorf("%s: error = %q, want it to name the parameter and the id", key, err)
		}
	}
}

// TestPermissions_SetsTheMode is the create-equivalent path for this
// method: the run that finds a difference and closes it.
//
// The assertion that counts is os.Stat, not the returned flag. It also
// checks that the owner and group the task never mentioned still come
// back in the stats, read from the device, because a later task's
// condition reading them should not have to care which attributes this
// one was asked for.
func TestPermissions_SetsTheMode(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	path := permissionsFile(t, 0o600)
	owner, group := permissionsFileNames(t, path)

	result, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if !result.Changed {
		t.Error("a run that changed the mode reported no change")
	}
	if got := permissionsFileMode(t, path); got != "0644" {
		t.Errorf("the file is %s on disk, want 0644: the mode was never applied", got)
	}

	if got := rc.stats["mode"]; got != "0644" {
		t.Errorf("mode stat = %v, want 0644", got)
	}
	if got := rc.stats["path"]; got != path {
		t.Errorf("path stat = %v, want %q", got, path)
	}
	if got := rc.stats["owner"]; got != owner {
		t.Errorf("owner stat = %v, want %q read back from the device", got, owner)
	}
	if got := rc.stats["group"]; got != group {
		t.Errorf("group stat = %v, want %q read back from the device", got, group)
	}

	before := permissionsDiffHalf(t, rc, "before")
	if got := before["mode"]; got != "0600" {
		t.Errorf("diff before mode = %v, want 0600: the prior state an inverse would restore is wrong", got)
	}
	after := permissionsDiffHalf(t, rc, "after")
	if got := after["mode"]; got != "0644" {
		t.Errorf("diff after mode = %v, want 0644", got)
	}
}

// TestPermissions_ConvergedRunReportsNoChange is the single most
// important property this method has, and the second run deliberately
// writes the mode unpadded.
//
// "644" from a runbook and "644" from stat are the same permission bits,
// and remotefile.NormalizeMode is what makes them compare equal. Without
// it every run reports changed forever, which is invisible until somebody
// notices their report never settles. Writing the second run's mode in
// the other spelling is what makes this test able to catch that.
func TestPermissions_ConvergedRunReportsNoChange(t *testing.T) {
	server := startPermissionsServer(t)
	path := permissionsFile(t, 0o600)
	device := newPermissionsTarget(server)

	first, err := file.Permissions(context.Background(), newPermissionsContext(server), device, map[string]any{
		"path":                          path,
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("first Permissions: %v", err)
	}
	if !first.Changed {
		t.Fatal("the first run reported no change, so this test could not prove anything about the second")
	}

	rc := newPermissionsContext(server)
	second, err := file.Permissions(context.Background(), rc, device, map[string]any{
		"path":                          path,
		"mode":                          "644",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("second Permissions: %v", err)
	}
	if second.Changed {
		t.Error("a converged run reported a change: 644 and 0644 did not compare equal")
	}
	if got := permissionsFileMode(t, path); got != "0644" {
		t.Errorf("the file is %s on disk, want 0644", got)
	}
	if got := rc.stats["mode"]; got != "0644" {
		t.Errorf("mode stat = %v, want the padded 0644 the device reports", got)
	}

	// A converged run still records a diff, and its two halves have to be
	// identical: that is what tells a rollback engine "this task changed
	// nothing, so undoing it means doing nothing", which an absent diff
	// cannot express.
	before := permissionsDiffHalf(t, rc, "before")
	after := permissionsDiffHalf(t, rc, "after")
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a converged run recorded before %v and after %v, want them identical", before, after)
	}
}

// TestPermissions_ConvergedOwnerAndGroupReportNoChange proves the owner
// and group comparisons converge too, using the names the path already
// carries.
//
// It runs anywhere, unlike a real ownership change, which needs either
// root or a second group. What it pins is that asking for the owner and
// group a path already has is not a change, which is what a runbook that
// declares the full triple does on every run after the first.
func TestPermissions_ConvergedOwnerAndGroupReportNoChange(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	path := permissionsFile(t, 0o640)
	owner, group := permissionsFileNames(t, path)

	result, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"mode":                          "0640",
		"owner":                         owner,
		"group":                         group,
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if result.Changed {
		t.Error("asking for the mode, owner and group a path already has reported a change")
	}
	if got := rc.stats["owner"]; got != owner {
		t.Errorf("owner stat = %v, want %q", got, owner)
	}
	if got := rc.stats["group"]; got != group {
		t.Errorf("group stat = %v, want %q", got, group)
	}
	if got := permissionsFileMode(t, path); got != "0640" {
		t.Errorf("the file is %s on disk, want it untouched at 0640", got)
	}
}

// TestPermissions_ChangesTheGroup proves a real chgrp reaches the
// filesystem, which the converged test above cannot show.
//
// It needs a second group this process may use, so it skips where there
// is none rather than pretending. The verification reads the gid from the
// filesystem and compares it against the one the name resolves to, so a
// method that reported a group it never set would fail here.
func TestPermissions_ChangesTheGroup(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	path := permissionsFile(t, 0o600)

	target := permissionsOtherGroup(t, path)
	if target == "" {
		t.Skip("this process belongs to no second group, so a real chgrp cannot be proven here")
	}
	wanted, err := user.LookupGroup(target)
	if err != nil {
		t.Fatalf("looking up group %q: %v", target, err)
	}

	result, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"group":                         target,
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if !result.Changed {
		t.Error("a run that changed the group reported no change")
	}

	_, gid := permissionsFileIDs(t, path)
	if strconv.Itoa(gid) != wanted.Gid {
		t.Errorf("the file's gid is %d on disk, want %s: the group was never applied", gid, wanted.Gid)
	}
	if got := rc.stats["group"]; got != target {
		t.Errorf("group stat = %v, want %q", got, target)
	}
	// The mode was not asked for, so it must still be what it was.
	if got := permissionsFileMode(t, path); got != "0600" {
		t.Errorf("the file is %s on disk, want the untouched 0600: a task changed something it was not asked to", got)
	}
}

// TestPermissions_WorksOnADirectory proves a directory is a legitimate
// target, since a directory's mode is the thing a runbook most often
// needs to set after creating one.
func TestPermissions_WorksOnADirectory(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)

	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}

	result, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          dir,
		"mode":                          "0750",
		"insecure_skip_host_key_verify": true,
	})
	if err != nil {
		t.Fatalf("Permissions: %v", err)
	}
	if !result.Changed {
		t.Error("a run that changed a directory's mode reported no change")
	}
	if got := permissionsFileMode(t, dir); got != "0750" {
		t.Errorf("the directory is %s on disk, want 0750", got)
	}
	if got := permissionsDiffHalf(t, rc, "before")["kind"]; got != "directory" {
		t.Errorf("diff before kind = %v, want directory", got)
	}
	// What is reported has to describe what is on disk. Checking only the
	// disk would pass against a method that changed the right thing and
	// then told the runbook something else, which is the failure a later
	// task reading these values would inherit.
	if got := rc.stats["mode"]; got != "0750" {
		t.Errorf("mode stat = %v, want 0750", got)
	}
	if got := permissionsDiffHalf(t, rc, "after")["mode"]; got != "0750" {
		t.Errorf("diff after mode = %v, want 0750", got)
	}
}

// TestPermissions_RefusesAnAbsentPath proves this method changes a path
// and never creates one.
//
// The refusal has to name what does create, because "does not exist" on
// its own reads as a bug in the runbook's ordering rather than as a
// deliberate boundary between two methods.
func TestPermissions_RefusesAnAbsentPath(t *testing.T) {
	server := startPermissionsServer(t)
	path := filepath.Join(t.TempDir(), "not-there")

	_, err := file.Permissions(context.Background(), newPermissionsContext(server), newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("an absent path was accepted, so this method created something")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want it to say the path is not there", err)
	}
	if !strings.Contains(err.Error(), "file.touch") {
		t.Errorf("error = %q, want it to name what does create a path", err)
	}
	if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
		t.Errorf("os.Lstat(%s) = %v, want the path still absent", path, statErr)
	}
}

// TestPermissions_RefusesASymbolicLink proves a link is refused rather
// than followed, and that the refusal happens BEFORE anything is applied.
//
// The second half is the real assertion. chmod follows a link while stat
// reports the link itself, so an implementation that went ahead would
// change the target's mode while comparing against the link's constant
// 0777 and report changed forever, all while the operator was reading
// the wrong path's state. The target keeping its original mode is what
// proves nothing was applied.
func TestPermissions_RefusesASymbolicLink(t *testing.T) {
	server := startPermissionsServer(t)
	target := permissionsFile(t, 0o600)
	link := filepath.Join(filepath.Dir(target), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("creating the symlink: %v", err)
	}

	_, err := file.Permissions(context.Background(), newPermissionsContext(server), newPermissionsTarget(server), map[string]any{
		"path":                          link,
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a symbolic link was accepted, so the task changed a path it was not pointed at")
	}
	if !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("error = %q, want it to say the path is a link", err)
	}
	if !strings.Contains(err.Error(), target) {
		t.Errorf("error = %q, want it to name the target to point at instead", err)
	}
	if got := permissionsFileMode(t, target); got != "0600" {
		t.Errorf("the link's target is %s on disk, want the untouched 0600", got)
	}
}

// TestPermissions_RefusesAnUnreachableDevice covers the connect failure
// path, which is what a device with no SSH transport produces.
func TestPermissions_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := file.Permissions(context.Background(), newPermissionsContext(permissionsServer{}), newPermissionsUnreachable(), map[string]any{
		"path": "/etc/hosts",
		"mode": "0644",
	})
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestPermissions_StatFailureIsReported covers the branch where the
// connection authenticates and then cannot carry the read.
//
// A session budget of zero is what that looks like from this side, and it
// is a real protocol-level refusal rather than an injected Go error, so
// the branch sees the shape a device under session pressure produces. The
// point is that a read that failed is never mistaken for a path that is
// absent, which would turn a transport problem into a wrong answer about
// the device.
func TestPermissions_StatFailureIsReported(t *testing.T) {
	server := startPermissionsServerWithSessionBudget(t, 0)
	path := permissionsFile(t, 0o600)

	_, err := file.Permissions(context.Background(), newPermissionsContext(server), newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failed read was reported as success")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
	// It has to be the READ that is reported. Swallowing the read's error
	// leaves a zero-valued state that says nothing is there, and every
	// comparison then finds a difference, so the task carries on and fails
	// later at the chmod. The message would still mention the session and
	// would be about the wrong operation entirely.
	if !strings.Contains(err.Error(), "stat "+path) {
		t.Errorf("error = %q, want it to name the read that failed rather than a later command", err)
	}
	if strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error = %q, want a transport failure not to be reported as an absent path", err)
	}
}

// TestPermissions_ApplyFailureIsReported covers the branch where nothing
// had been changed yet when the change failed.
//
// An owner that does not exist is the realistic shape of it. The error
// must NOT claim a partial change, because nothing was applied and
// telling an operator to expect half a change where there is none sends
// them looking at the wrong path.
func TestPermissions_ApplyFailureIsReported(t *testing.T) {
	server := startPermissionsServer(t)
	path := permissionsFile(t, 0o600)

	_, err := file.Permissions(context.Background(), newPermissionsContext(server), newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"owner":                         "pleiades-no-such-user",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failed chown was reported as success")
	}
	if !strings.Contains(err.Error(), "chown") {
		t.Errorf("error = %q, want it to name the operation that failed", err)
	}
	if strings.Contains(err.Error(), "part of the change") {
		t.Errorf("error = %q, want no claim of a partial change: nothing had been applied", err)
	}
	if got := permissionsFileMode(t, path); got != "0600" {
		t.Errorf("the file is %s on disk, want the untouched 0600", got)
	}
}

// TestPermissions_PartialFailureIsReported covers the branch where the
// mode landed and the ownership change then failed.
//
// This is the one case where the device is left in a state neither the
// runbook nor a rollback recorded, so the error is the only place it can
// be said, and the filesystem assertion proves the claim is true rather
// than a fixed phrase.
func TestPermissions_PartialFailureIsReported(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	path := permissionsFile(t, 0o600)

	_, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          path,
		"mode":                          "0644",
		"owner":                         "pleiades-no-such-user",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failed chown after a successful chmod was reported as success")
	}
	// NOTHING was applied, and that is the point of the ordering this
	// method's Apply now uses. It sends ownership before mode, because
	// Linux clears the setuid and setgid bits on a regular file whenever
	// its owner or group changes, so chmod-then-chown silently threw away
	// a special bit the task had asked for and could never converge
	// (verified at a real shell: chmod 2755 followed by chgrp leaves 755).
	// A happy side effect is fewer partial states: the chown fails first,
	// so the mode is never touched.
	if strings.Contains(err.Error(), "part of the change was applied") {
		t.Errorf("error = %q, want no partial claim: ownership is applied first, so nothing landed", err)
	}
	if got := permissionsFileMode(t, path); got != "0600" {
		t.Errorf("the file is %s on disk, want the untouched 0600", got)
	}
	if _, recorded := rc.stats["diff"]; recorded {
		t.Error("a diff was recorded for a failed run, so its after state describes a change that never completed")
	}
}

// TestPermissions_DiffRecordFailureIsReported covers the branch where the
// change landed and recording the prior state did not.
//
// Swallowing it would leave a rollback engine with no record of a change
// that really happened, which is worse than a failed task.
func TestPermissions_DiffRecordFailureIsReported(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	rc.failOn = "diff"

	_, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          permissionsFile(t, 0o600),
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the diff was swallowed")
	}
	if !errors.Is(err, errPermissionsStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
}

// TestPermissions_StatRecordFailureIsReported covers the same swallowing
// question at the second recording call site, the returned stats, which
// the diff test above can never reach.
func TestPermissions_StatRecordFailureIsReported(t *testing.T) {
	server := startPermissionsServer(t)
	rc := newPermissionsContext(server)
	rc.failOn = "path"

	_, err := file.Permissions(context.Background(), rc, newPermissionsTarget(server), map[string]any{
		"path":                          permissionsFile(t, 0o600),
		"mode":                          "0644",
		"insecure_skip_host_key_verify": true,
	})
	if err == nil {
		t.Fatal("a failure to record the returned stats was swallowed")
	}
	if !errors.Is(err, errPermissionsStat) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
	if _, recorded := rc.stats["diff"]; !recorded {
		t.Error("the diff was not recorded before the stats, so the two call sites ran in the wrong order")
	}
}
