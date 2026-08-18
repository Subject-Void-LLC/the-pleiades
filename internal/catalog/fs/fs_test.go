package fs_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	fsmod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fs"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with findmnt, mount and umount on PATH as fake shell scripts
// (mounting a real filesystem is neither safe nor possible from a test).
// fstab persistence, by contrast, runs for real: it is a plain text file
// read and written through pkg/remotefile against a real temp path, the
// same technique internal/catalog/file/line's own tests use, and every
// assertion below reads that file back from the real filesystem rather
// than trusting the method's own report.

// ---------- harness ----------

type ctxStub struct {
	secrets   map[string]string
	stats     map[string]any
	failOnKey string
}

func (c *ctxStub) InjectSecrets() map[string]string { return c.secrets }
func (c *ctxStub) SetStat(key string, value any) error {
	if c.failOnKey != "" && key == c.failOnKey {
		return fmt.Errorf("ctxStub: injected failure recording %q", key)
	}
	c.stats[key] = value
	return nil
}
func (c *ctxStub) EmitFact(key string, value any) error { return c.SetStat(key, value) }

type target struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *target) SSHHost() string { return d.host }
func (d *target) SSHPort() int    { return d.port }

func noSSHDevice() inventory.InventoryItem {
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NameLinux}}
}

// mountState is what the fake findmnt reports.
type mountState struct {
	mounted bool
	source  string
	fstype  string
	options string

	findmntRaw string // overrides the computed line verbatim, reaching the malformed-output branch

	mountExit         int
	mountFailStream   string
	unmountExit       int
	unmountFailStream string
}

var (
	notMounted = mountState{}
	mounted    = mountState{mounted: true, source: "/dev/sdb1", fstype: "ext4", options: "rw,relatime"}
)

type harness struct {
	rc     *ctxStub
	device inventory.InventoryItem
	record string
	fstab  string
}

func newHarness(t *testing.T, state mountState, fstabContent string) *harness {
	t.Helper()
	return newHarnessBudgeted(t, state, fstabContent, -1)
}

func newHarnessBudgeted(t *testing.T, state mountState, fstabContent string, budget int) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	fstabPath := filepath.Join(dir, "fstab")
	if err := os.WriteFile(fstabPath, []byte(fstabContent), 0o644); err != nil { // #nosec G306 -- test fixture
		t.Fatalf("writing the fake fstab: %v", err)
	}

	findmntScript := `#!/bin/sh
if [ "$FAKE_MOUNTED" = "1" ]; then
  if [ -n "$FAKE_FINDMNT_RAW" ]; then
    printf '%s\n' "$FAKE_FINDMNT_RAW"
  else
    printf '%s %s %s\n' "$FAKE_SOURCE" "$FAKE_FSTYPE" "$FAKE_OPTIONS"
  fi
  exit 0
fi
exit 1
`
	mountScript := `#!/bin/sh
printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit_code="${FAKE_MOUNT_EXIT:-0}"
if [ "$exit_code" != "0" ]; then
  case "$FAKE_MOUNT_FAIL_STREAM" in
    stdout) printf 'fake mount failure\n' ;;
    none) : ;;
    *) printf 'fake mount failure\n' >&2 ;;
  esac
fi
exit "$exit_code"
`
	umountScript := `#!/bin/sh
printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit_code="${FAKE_UMOUNT_EXIT:-0}"
if [ "$exit_code" != "0" ]; then
  case "$FAKE_UMOUNT_FAIL_STREAM" in
    stdout) printf 'fake umount failure\n' ;;
    none) : ;;
    *) printf 'fake umount failure\n' >&2 ;;
  esac
fi
exit "$exit_code"
`
	writeScript(t, dir, "findmnt", findmntScript)
	writeScript(t, dir, "mount", mountScript)
	writeScript(t, dir, "umount", umountScript)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	mountedFlag := "0"
	if state.mounted {
		mountedFlag = "1"
	}
	t.Setenv("FAKE_MOUNTED", mountedFlag)
	t.Setenv("FAKE_SOURCE", state.source)
	t.Setenv("FAKE_FSTYPE", state.fstype)
	t.Setenv("FAKE_OPTIONS", state.options)
	t.Setenv("FAKE_FINDMNT_RAW", state.findmntRaw)
	t.Setenv("FAKE_MOUNT_EXIT", strconv.Itoa(state.mountExit))
	t.Setenv("FAKE_MOUNT_FAIL_STREAM", state.mountFailStream)
	t.Setenv("FAKE_UMOUNT_EXIT", strconv.Itoa(state.unmountExit))
	t.Setenv("FAKE_UMOUNT_FAIL_STREAM", state.unmountFailStream)

	opts := remoteexectest.Options{}
	if budget >= 0 {
		opts.SessionLimit = remoteexectest.Limit(budget)
	}
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)

	return &harness{
		rc: &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}},
		device: &target{
			Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameLinux}},
			host: srv.Host, port: srv.Port,
		},
		record: record,
		fstab:  fstabPath,
	}
}

func writeScript(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake %s: %v", name, err)
	}
}

func (h *harness) params(path string, extra map[string]any) map[string]any {
	p := map[string]any{"path": path, "fstab": h.fstab, "insecure_skip_host_key_verify": true}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func (h *harness) invocations(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(h.record) // #nosec G304 -- path built by this test
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading recorded invocations: %v", err)
	}
	var calls []string
	var current []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "---" {
			if len(current) > 0 {
				calls = append(calls, strings.Join(current, " "))
			}
			current = nil
			continue
		}
		current = append(current, line)
	}
	return calls
}

func (h *harness) fstabContent(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(h.fstab) // #nosec G304 -- path built by this test
	if err != nil {
		t.Fatalf("reading the fake fstab: %v", err)
	}
	return string(data)
}

func inverseOf(rc *ctxStub) (fqcn string, params map[string]any, ok bool) {
	raw, present := rc.stats[sdk.StatInverse]
	if !present {
		return "", nil, false
	}
	record := raw.(map[string]any)
	fqcn, _ = record["fqcn"].(string)
	params, _ = record["params"].(map[string]any)
	return fqcn, params, true
}

func lookup(t *testing.T, fqcn string) collection.Descriptor {
	t.Helper()
	desc, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	return desc
}

// ---------- registration ----------

func TestRegistration(t *testing.T) {
	for _, fqcn := range []string{"fs.mount", "fs.unmount"} {
		desc := lookup(t, fqcn)
		if desc.Manifest.Status != collection.StatusImplemented {
			t.Errorf("%s: Status = %v, want StatusImplemented", fqcn, desc.Manifest.Status)
		}
		if desc.Invoke == nil {
			t.Errorf("%s: Invoke is nil", fqcn)
		}
		if !desc.Manifest.Reversibility.Reversible {
			t.Errorf("%s: expected Reversible: true", fqcn)
		}
		if desc.Manifest.Doc.Summary == "" {
			t.Errorf("%s: Doc.Summary is empty", fqcn)
		}
	}
}

// ---------- Mount: no-ssh / connect failure ----------

func TestMount_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := fsmod.Mount(context.Background(), rc, noSSHDevice(), map[string]any{"path": "/data", "src": "/dev/sdb1", "fstype": "ext4"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestMount_MissingRequiredParams(t *testing.T) {
	h := newHarness(t, notMounted, "")
	for _, params := range []map[string]any{
		{"src": "/dev/sdb1", "fstype": "ext4"},
		{"path": "/data", "fstype": "ext4"},
		{"path": "/data", "src": "/dev/sdb1"},
	} {
		if _, err := fsmod.Mount(context.Background(), h.rc, h.device, params); err == nil {
			t.Errorf("params %v: expected a required-param error", params)
		}
	}
}

// ---------- Mount: fresh mount ----------

func TestMount_AbsentMountsAndPersistsWithDefaults(t *testing.T) {
	h := newHarness(t, notMounted, "")
	result, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{"src": "/dev/sdb1", "fstype": "ext4"}))
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "mount -t ext4 -o defaults /dev/sdb1 /data" {
		t.Fatalf("mount invocations = %v", calls)
	}
	content := h.fstabContent(t)
	if !strings.Contains(content, "/dev/sdb1\t/data\text4\tdefaults\t0\t0") {
		t.Fatalf("fstab content = %q, want a defaults entry", content)
	}
	fqcn, params, ok := inverseOf(h.rc)
	if !ok || fqcn != "fs.unmount" {
		t.Fatalf("inverse = %q, %v, ok=%v", fqcn, params, ok)
	}
	if params["path"] != "/data" || params["persist"] != true {
		t.Fatalf("inverse params = %v", params)
	}
}

func TestMount_AbsentWithoutPersistLeavesFstabUntouched(t *testing.T) {
	h := newHarness(t, notMounted, "")
	result, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4", "persist": false,
	}))
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	if content := h.fstabContent(t); content != "" {
		t.Fatalf("fstab content = %q, want untouched (empty)", content)
	}
	fqcn, params, ok := inverseOf(h.rc)
	if !ok || fqcn != "fs.unmount" || params["persist"] != false {
		t.Fatalf("inverse = %q, %v, ok=%v", fqcn, params, ok)
	}
}

func TestMount_AbsentWithExplicitOpts(t *testing.T) {
	h := newHarness(t, notMounted, "")
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4", "opts": "ro,noatime",
	})); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "mount -t ext4 -o ro,noatime /dev/sdb1 /data" {
		t.Fatalf("mount invocations = %v", calls)
	}
	if content := h.fstabContent(t); !strings.Contains(content, "ro,noatime") {
		t.Fatalf("fstab content = %q, want ro,noatime", content)
	}
}

func TestMount_UseraddFailure(t *testing.T) {
	state := notMounted
	state.mountExit = 1
	h := newHarness(t, state, "")
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected the mount failure to surface as an error")
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded on failure")
	}
}

func TestMount_FailsSilently(t *testing.T) {
	state := notMounted
	state.mountExit = 1
	state.mountFailStream = "none"
	h := newHarness(t, state, "")
	_, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	}))
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("Mount error = %v, want it to mention 'no output'", err)
	}
}

func TestMount_FailsWithStdoutOnly(t *testing.T) {
	state := notMounted
	state.mountExit = 1
	state.mountFailStream = "stdout"
	h := newHarness(t, state, "")
	_, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	}))
	if err == nil || !strings.Contains(err.Error(), "fake mount failure") {
		t.Fatalf("Mount error = %v, want it to surface the fake stdout failure", err)
	}
}

// ---------- Mount: already mounted ----------

func TestMount_AlreadyMountedMatchingNoOptsGivenNoFstabWanted(t *testing.T) {
	h := newHarness(t, mounted, "")
	result, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype, "persist": false,
	}))
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false: already mounted, no fstab requested")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no mount/umount invocations, got %v", calls)
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a fully converged run")
	}
}

func TestMount_AlreadyMountedAddsFstabEntryOnly(t *testing.T) {
	h := newHarness(t, mounted, "")
	result, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	}))
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true: fstab entry was added")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no mount invocation (already mounted), got %v", calls)
	}
	// No inverse: this run did not mount anything, only touched fstab, and
	// this namespace does not attempt an inverse for that case (fs.go's own
	// doc comment and the manifest's Reversibility.Notes explain why).
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a fstab-only change")
	}
}

func TestMount_AlreadyMountedFstabPreservesExistingOptionsWhenNoneGiven(t *testing.T) {
	existing := "UUID=abc\t/data\text4\tro,noatime\t0\t0\n"
	h := newHarness(t, mounted, existing)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	})); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	content := h.fstabContent(t)
	if !strings.Contains(content, mounted.source+"\t/data\t"+mounted.fstype+"\tro,noatime\t0\t0") {
		t.Fatalf("fstab content = %q, want the existing options preserved", content)
	}
}

func TestMount_DriftingSourceRefuses(t *testing.T) {
	h := newHarness(t, mounted, "")
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/other", "fstype": mounted.fstype,
	})); err == nil {
		t.Fatal("expected a refusal for a mismatched source")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no commands sent on refusal, got %v", calls)
	}
}

func TestMount_DriftingFstypeRefuses(t *testing.T) {
	h := newHarness(t, mounted, "")
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": "xfs",
	})); err == nil {
		t.Fatal("expected a refusal for a mismatched fstype")
	}
}

func TestMount_DriftingOptsRefusesOnlyWhenExplicit(t *testing.T) {
	h := newHarness(t, mounted, "")
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype, "opts": "ro",
	})); err == nil {
		t.Fatal("expected a refusal for mismatched, explicitly-given opts")
	}
}

// ---------- Mount: fstab source ----------

func TestMount_MalformedFindmntOutput(t *testing.T) {
	state := mounted
	state.findmntRaw = "only-one-field"
	h := newHarness(t, state, "")
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	})); err == nil {
		t.Fatal("expected an error on malformed findmnt output")
	}
}

func TestMount_MissingFstabFile(t *testing.T) {
	h := newHarness(t, notMounted, "")
	if err := os.Remove(h.fstab); err != nil {
		t.Fatalf("removing the fake fstab: %v", err)
	}
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected an error when the fstab file does not exist")
	}
}

func TestMount_SkipsCommentsAndBlankLines(t *testing.T) {
	existing := "# a comment\n\n/dev/other\t/other\text4\tdefaults\t0\t0\n"
	h := newHarness(t, notMounted, existing)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	content := h.fstabContent(t)
	if !strings.Contains(content, "# a comment") || !strings.Contains(content, "/other") || !strings.Contains(content, "/data") {
		t.Fatalf("fstab content = %q, want the comment, the unrelated entry and the new entry all present", content)
	}
}

// ---------- Mount: connection failures at each call site ----------

func TestMount_ConnectionDiesQueryingMount(t *testing.T) {
	h := newHarnessBudgeted(t, notMounted, "", 0)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected a connection failure querying findmnt")
	}
}

func TestMount_ConnectionDiesQueryingFstab(t *testing.T) {
	h := newHarnessBudgeted(t, notMounted, "", 1)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected a connection failure reading fstab")
	}
}

func TestMount_ConnectionDiesRunningMount(t *testing.T) {
	// findmnt (1) + fstab stat (2) + fstab read (3) succeed, mount itself (4th session) is refused.
	h := newHarnessBudgeted(t, notMounted, "", 3)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected a connection failure running mount")
	}
}

func TestMount_PersistNotABool(t *testing.T) {
	h := newHarness(t, notMounted, "")
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4", "persist": "yes",
	})); err == nil {
		t.Fatal("expected an error for a non-bool persist")
	}
}

func TestMount_FullyConvergedReportsNoChange(t *testing.T) {
	existing := mounted.source + "\t/data\t" + mounted.fstype + "\t" + mounted.options + "\t0\t0\n"
	h := newHarness(t, mounted, existing)
	result, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	}))
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false: the mount and its fstab entry already matched exactly")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no commands sent, got %v", calls)
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a fully converged run")
	}
}

func TestMount_ConnectionDiesReadingFstabDuringQuery(t *testing.T) {
	h := newHarnessBudgeted(t, mounted, "", 2)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	})); err == nil {
		t.Fatal("expected a connection failure reading fstab content while querying state")
	}
}

func TestMount_ConnectionDiesInSyncFstabStat(t *testing.T) {
	existing := "/dev/stale\t/data\text4\tro\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 3)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	})); err == nil {
		t.Fatal("expected a connection failure in syncFstab's own stat")
	}
}

func TestMount_ConnectionDiesInSyncFstabRead(t *testing.T) {
	existing := "/dev/stale\t/data\text4\tro\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 5)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	})); err == nil {
		t.Fatal("expected a connection failure reading fstab content inside syncFstab")
	}
}

func TestMount_ConnectionDiesWritingFstab(t *testing.T) {
	existing := "/dev/stale\t/data\text4\tro\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 6)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	})); err == nil {
		t.Fatal("expected a connection failure writing the new fstab content")
	}
}

func TestMount_ConnectionDiesConfirmingFstabWrite(t *testing.T) {
	existing := "/dev/stale\t/data\text4\tro\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 7)
	_, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	}))
	if err == nil || !strings.Contains(err.Error(), "already in place") {
		t.Fatalf("Mount error = %v, want it to mention the write already being in place", err)
	}
}

func TestMount_ConnectionDiesRestoringFstabPermissions(t *testing.T) {
	existing := "/dev/stale\t/data\text4\tro\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 8)
	_, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	}))
	if err == nil || !strings.Contains(err.Error(), "carries the permissions") {
		t.Fatalf("Mount error = %v, want it to mention the temporary's permissions", err)
	}
}

func TestMount_ConnectionDiesInAfterQuery(t *testing.T) {
	existing := "/dev/stale\t/data\text4\tro\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 9)
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": mounted.source, "fstype": mounted.fstype,
	})); err == nil {
		t.Fatal("expected a connection failure re-querying state after the change")
	}
}

// ---------- Mount: recording failures ----------

func TestMount_RecordStatFails(t *testing.T) {
	h := newHarness(t, notMounted, "")
	h.rc.failOnKey = "path"
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestMount_RecordDiffFails(t *testing.T) {
	h := newHarness(t, notMounted, "")
	h.rc.failOnKey = sdk.StatDiff
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected the injected diff-recording failure to surface")
	}
}

func TestMount_RecordInverseFails(t *testing.T) {
	h := newHarness(t, notMounted, "")
	h.rc.failOnKey = sdk.StatInverse
	if _, err := fsmod.Mount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{
		"src": "/dev/sdb1", "fstype": "ext4",
	})); err == nil {
		t.Fatal("expected the injected inverse-recording failure to surface")
	}
}

// ---------- Unmount ----------

func TestUnmount_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := fsmod.Unmount(context.Background(), rc, noSSHDevice(), map[string]any{"path": "/data"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestUnmount_MissingPath(t *testing.T) {
	h := newHarness(t, notMounted, "")
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, map[string]any{}); err == nil {
		t.Fatal("expected a required-param error")
	}
}

func TestUnmount_AbsentIsNoOp(t *testing.T) {
	h := newHarness(t, notMounted, "")
	result, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil))
	if err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a no-op")
	}
}

func TestUnmount_MountedUnmountsAndRemovesFstabEntry(t *testing.T) {
	existing := mounted.source + "\t/data\t" + mounted.fstype + "\t" + mounted.options + "\t0\t0\n"
	h := newHarness(t, mounted, existing)
	result, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil))
	if err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "umount /data" {
		t.Fatalf("umount invocations = %v", calls)
	}
	if content := h.fstabContent(t); strings.Contains(content, "/data") {
		t.Fatalf("fstab content = %q, want the entry removed", content)
	}
	fqcn, params, ok := inverseOf(h.rc)
	if !ok || fqcn != "fs.mount" {
		t.Fatalf("inverse = %q, %v, ok=%v", fqcn, params, ok)
	}
	if params["src"] != mounted.source || params["fstype"] != mounted.fstype || params["opts"] != mounted.options || params["persist"] != true {
		t.Fatalf("inverse params = %v", params)
	}
}

func TestUnmount_MountedWithoutPersistLeavesFstabAlone(t *testing.T) {
	existing := mounted.source + "\t/data\t" + mounted.fstype + "\t" + mounted.options + "\t0\t0\n"
	h := newHarness(t, mounted, existing)
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{"persist": false})); err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if content := h.fstabContent(t); !strings.Contains(content, "/data") {
		t.Fatalf("fstab content = %q, want the entry left alone", content)
	}
	_, params, ok := inverseOf(h.rc)
	if !ok || params["persist"] != false {
		t.Fatalf("inverse params = %v, ok=%v", params, ok)
	}
}

func TestUnmount_NotMountedRemovesStaleFstabEntryOnly(t *testing.T) {
	existing := "/dev/stale\t/data\text4\tdefaults\t0\t0\n"
	h := newHarness(t, notMounted, existing)
	result, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil))
	if err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true: a stale fstab entry was removed")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no umount invocation, got %v", calls)
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a fstab-only change")
	}
}

func TestUnmount_UmountFailure(t *testing.T) {
	state := mounted
	state.unmountExit = 1
	h := newHarness(t, state, "")
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil)); err == nil {
		t.Fatal("expected the umount failure to surface as an error")
	}
}

func TestUnmount_ConnectionDiesRunningUmount(t *testing.T) {
	// findmnt (1) + fstab stat (2) + fstab read (3) succeed, umount itself (4th session) is refused.
	h := newHarnessBudgeted(t, mounted, "", 3)
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil)); err == nil {
		t.Fatal("expected a connection failure running umount")
	}
}

func TestUnmount_PersistNotABool(t *testing.T) {
	h := newHarness(t, notMounted, "")
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", map[string]any{"persist": "yes"})); err == nil {
		t.Fatal("expected an error for a non-bool persist")
	}
}

func TestUnmount_ConnectionDiesQueryingMount(t *testing.T) {
	h := newHarnessBudgeted(t, mounted, "", 0)
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil)); err == nil {
		t.Fatal("expected a connection failure querying findmnt")
	}
}

func TestUnmount_ConnectionDiesInSyncFstab(t *testing.T) {
	existing := mounted.source + "\t/data\t" + mounted.fstype + "\t" + mounted.options + "\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 7)
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil)); err == nil {
		t.Fatal("expected a connection failure writing the new fstab content")
	}
}

func TestUnmount_ConnectionDiesInAfterQuery(t *testing.T) {
	existing := mounted.source + "\t/data\t" + mounted.fstype + "\t" + mounted.options + "\t0\t0\n"
	h := newHarnessBudgeted(t, mounted, existing, 10)
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil)); err == nil {
		t.Fatal("expected a connection failure re-querying state after the change")
	}
}

func TestUnmount_RecordStatFails(t *testing.T) {
	h := newHarness(t, mounted, "")
	h.rc.failOnKey = "path"
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil)); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestUnmount_RecordInverseFails(t *testing.T) {
	h := newHarness(t, mounted, "")
	h.rc.failOnKey = sdk.StatInverse
	if _, err := fsmod.Unmount(context.Background(), h.rc, h.device, h.params("/data", nil)); err == nil {
		t.Fatal("expected the injected inverse-recording failure to surface")
	}
}
