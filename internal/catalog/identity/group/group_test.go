package group_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/group"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with getent, groupadd, groupmod and groupdel on PATH as
// shell scripts, the same tier internal/catalog/identity/user's own
// tests run at.

// ---------- harness ----------

type ctxStub struct {
	secrets map[string]string
	stats   map[string]any

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
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NamePosixAccount}}
}

// groupState is what the fake getent/groupadd/groupmod/groupdel report.
type groupState struct {
	exists bool
	gid    int

	exitOverride int    // forces getent's exit code away from the exists-derived default (0/2)
	rawLine      string // overrides getent's successful output verbatim

	mutateExit       int
	mutateFailStream string // "" (stderr), "stdout", or "none"
}

var (
	absentGroup  = groupState{}
	presentGroup = groupState{exists: true, gid: 2000}
)

type harness struct {
	rc     *ctxStub
	device inventory.InventoryItem
	record string
}

func newHarness(t *testing.T, state groupState) *harness {
	t.Helper()
	return newHarnessBudgeted(t, state, -1)
}

func newHarnessBudgeted(t *testing.T, state groupState, budget int) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	exit := 2
	if state.exists {
		exit = 0
	}
	if state.exitOverride != 0 {
		exit = state.exitOverride
	}

	getentScript := `#!/bin/sh
key="$2"
exit_code="$FAKE_EXIT"
if [ "$exit_code" = "0" ]; then
  if [ -n "$FAKE_RAW" ]; then
    printf '%s\n' "$FAKE_RAW"
  else
    printf '%s:x:%s:\n' "$key" "$FAKE_GID"
  fi
elif [ "$exit_code" != "2" ]; then
  printf 'getent: fake failure resolving group %s\n' "$key" >&2
fi
exit "$exit_code"
`
	recorderScript := `#!/bin/sh
printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit_code="${FAKE_MUTATE_EXIT:-0}"
if [ "$exit_code" != "0" ]; then
  case "$FAKE_MUTATE_FAIL_STREAM" in
    stdout) printf 'fake failure\n' ;;
    none) : ;;
    *) printf 'fake failure\n' >&2 ;;
  esac
fi
exit "$exit_code"
`
	writeScript(t, dir, "getent", getentScript)
	writeScript(t, dir, "groupadd", recorderScript)
	writeScript(t, dir, "groupmod", recorderScript)
	writeScript(t, dir, "groupdel", recorderScript)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_EXIT", strconv.Itoa(exit))
	t.Setenv("FAKE_GID", strconv.Itoa(state.gid))
	t.Setenv("FAKE_RAW", state.rawLine)
	t.Setenv("FAKE_MUTATE_EXIT", strconv.Itoa(state.mutateExit))
	t.Setenv("FAKE_MUTATE_FAIL_STREAM", state.mutateFailStream)

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
			Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NamePosixAccount}},
			host: srv.Host, port: srv.Port,
		},
		record: record,
	}
}

func writeScript(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake %s: %v", name, err)
	}
}

func (h *harness) params(name string, extra map[string]any) map[string]any {
	p := map[string]any{"name": name, "insecure_skip_host_key_verify": true}
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

// ---------- registration ----------

func TestRegistered(t *testing.T) {
	for _, fqcn := range []string{"identity.group.create", "identity.group.modify", "identity.group.remove"} {
		t.Run(fqcn, func(t *testing.T) {
			d, ok := collection.Lookup(fqcn)
			if !ok {
				t.Fatalf("collection.Lookup(%q) found nothing", fqcn)
			}
			if d.Manifest.Status != collection.StatusImplemented {
				t.Errorf("Status = %v, want StatusImplemented", d.Manifest.Status)
			}
			if !containsCap(d.Manifest.RequiredCapabilities, capability.NamePosixAccount) {
				t.Errorf("RequiredCapabilities = %v, want it to include %v", d.Manifest.RequiredCapabilities, capability.NamePosixAccount)
			}
			if !d.Manifest.Reversibility.Reversible || d.Manifest.Reversibility.Notes == "" {
				t.Errorf("Reversibility = %+v, want Reversible with Notes", d.Manifest.Reversibility)
			}
		})
	}
}

func containsCap(caps []capability.Name, want capability.Name) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// ---------- connection failures ----------

func TestCreate_NoSSHConnection(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
	_, err := group.Create(context.Background(), rc, noSSHDevice(), map[string]any{"name": "admins"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestModify_NoSSHConnection(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
	_, err := group.Modify(context.Background(), rc, noSSHDevice(), map[string]any{"name": "admins", "gid": 1})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestRemove_NoSSHConnection(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
	_, err := group.Remove(context.Background(), rc, noSSHDevice(), map[string]any{"name": "admins"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

// ---------- required params ----------

func TestCreate_MissingName(t *testing.T) {
	h := newHarness(t, absentGroup)
	_, err := group.Create(context.Background(), h.rc, h.device, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want it to say name is required", err)
	}
}

func TestModify_MissingName(t *testing.T) {
	h := newHarness(t, absentGroup)
	_, err := group.Modify(context.Background(), h.rc, h.device, map[string]any{"gid": 1})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want it to say name is required", err)
	}
}

func TestRemove_MissingName(t *testing.T) {
	h := newHarness(t, absentGroup)
	_, err := group.Remove(context.Background(), h.rc, h.device, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want it to say name is required", err)
	}
}

func TestModify_MissingGID(t *testing.T) {
	h := newHarness(t, presentGroup)
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "gid is required") {
		t.Errorf("err = %v, want it to say gid is required", err)
	}
}

// ---------- Create ----------

func TestCreate_AbsentGroupIsCreated(t *testing.T) {
	h := newHarness(t, absentGroup)
	res, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": 4000}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "groupadd -g 4000 admins" {
		t.Errorf("invocations = %v, want a single groupadd -g call", calls)
	}
	if h.rc.stats["name"] != "admins" {
		t.Errorf("name stat = %v, want admins", h.rc.stats["name"])
	}
	inv := h.rc.stats[sdk.StatInverse].(map[string]any)
	if inv["fqcn"] != "identity.group.remove" {
		t.Errorf("inverse fqcn = %v, want identity.group.remove", inv["fqcn"])
	}
}

func TestCreate_AbsentGroupNoGidNoSystem(t *testing.T) {
	h := newHarness(t, absentGroup)
	if _, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "groupadd admins" {
		t.Errorf("invocations = %v, want a plain groupadd", calls)
	}
}

func TestCreate_AbsentGroupSystem(t *testing.T) {
	h := newHarness(t, absentGroup)
	if _, err := group.Create(context.Background(), h.rc, h.device, h.params("svc", map[string]any{"system": true})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "groupadd -r svc" {
		t.Errorf("invocations = %v, want groupadd -r", calls)
	}
}

func TestCreate_PresentGroupMatchingGidIsNoop(t *testing.T) {
	h := newHarness(t, presentGroup)
	res, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("invocations = %v, want none", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("an inverse was recorded on a no-op run")
	}
}

func TestCreate_PresentGroupNoGidRequestedIsNoop(t *testing.T) {
	h := newHarness(t, presentGroup)
	res, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false")
	}
}

func TestCreate_PresentGroupConvergesGid(t *testing.T) {
	h := newHarness(t, presentGroup)
	res, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	want := fmt.Sprintf("groupmod -g %d admins", presentGroup.gid+1)
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("invocations = %v, want %q", calls, want)
	}
	inv := h.rc.stats[sdk.StatInverse].(map[string]any)
	if inv["fqcn"] != "identity.group.modify" {
		t.Errorf("inverse fqcn = %v, want identity.group.modify", inv["fqcn"])
	}
	if inv["params"].(map[string]any)["gid"] != presentGroup.gid {
		t.Errorf("inverse gid = %v, want the old gid %d", inv["params"].(map[string]any)["gid"], presentGroup.gid)
	}
}

func TestCreate_GroupaddFails(t *testing.T) {
	state := absentGroup
	state.mutateExit = 1
	h := newHarness(t, state)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "groupadd") || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("err = %v, want it to name the failed groupadd invocation", err)
	}
}

func TestCreate_GroupmodFails(t *testing.T) {
	state := presentGroup
	state.mutateExit = 1
	h := newHarness(t, state)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err == nil || !strings.Contains(err.Error(), "groupmod") {
		t.Errorf("err = %v, want it to name the failed groupmod invocation", err)
	}
}

func TestCreate_InitialQueryGetentFailure(t *testing.T) {
	state := absentGroup
	state.exitOverride = 1
	h := newHarness(t, state)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "getent group") {
		t.Errorf("err = %v, want it to mention the getent group failure", err)
	}
}

func TestCreate_MalformedGetentOutput(t *testing.T) {
	state := absentGroup
	state.exists = true
	state.rawLine = "admins:x"
	h := newHarness(t, state)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "unexpected output") {
		t.Errorf("err = %v, want it to reject the malformed getent line", err)
	}
}

func TestCreate_GetentGidNotANumber(t *testing.T) {
	state := absentGroup
	state.exists = true
	state.rawLine = "admins:x:notanumber:"
	h := newHarness(t, state)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "gid field") {
		t.Errorf("err = %v, want it to reject the non-numeric gid field", err)
	}
}

func TestCreate_GroupaddFailsStdoutOnly(t *testing.T) {
	state := absentGroup
	state.mutateExit = 1
	state.mutateFailStream = "stdout"
	h := newHarness(t, state)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Errorf("err = %v, want the stdout-only failure detail", err)
	}
}

func TestCreate_GroupaddFailsSilently(t *testing.T) {
	state := absentGroup
	state.mutateExit = 1
	state.mutateFailStream = "none"
	h := newHarness(t, state)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Errorf("err = %v, want \"no output\"", err)
	}
}

func TestCreate_BadGIDType(t *testing.T) {
	h := newHarness(t, absentGroup)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": 4000.5}))
	if err == nil || !strings.Contains(err.Error(), "not a whole number") {
		t.Errorf("err = %v, want it to refuse a fractional gid", err)
	}
}

func TestCreate_BadSystemType(t *testing.T) {
	h := newHarness(t, absentGroup)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"system": "yes"}))
	if err == nil {
		t.Fatal("expected system to refuse a non-boolean value")
	}
}

func TestCreate_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absentGroup, 0)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the initial getent query")
	}
}

func TestCreate_GroupaddConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absentGroup, 1)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the groupadd call")
	}
}

func TestCreate_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absentGroup, 2)
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the post-create requery")
	}
}

func TestCreate_RecordStateNameFailure(t *testing.T) {
	h := newHarness(t, absentGroup)
	h.rc.failOnKey = "name"
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), `injected failure recording "name"`) {
		t.Errorf("err = %v, want the injected name-stat failure", err)
	}
}

func TestCreate_RecordStateDiffFailure(t *testing.T) {
	h := newHarness(t, absentGroup)
	h.rc.failOnKey = sdk.StatDiff
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatDiff)) {
		t.Errorf("err = %v, want the injected diff-stat failure", err)
	}
}

func TestCreate_RecordInverseFailureOnCreatePath(t *testing.T) {
	h := newHarness(t, absentGroup)
	h.rc.failOnKey = sdk.StatInverse
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}

func TestCreate_RecordInverseFailureOnConvergePath(t *testing.T) {
	h := newHarness(t, presentGroup)
	h.rc.failOnKey = sdk.StatInverse
	_, err := group.Create(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}

// ---------- Modify ----------

func TestModify_AbsentGroupIsRefused(t *testing.T) {
	h := newHarness(t, absentGroup)
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("ghosts", map[string]any{"gid": 1}))
	if err == nil || !strings.Contains(err.Error(), "no such group") {
		t.Errorf("err = %v, want it to refuse a non-existent group", err)
	}
}

func TestModify_MatchingGidIsNoop(t *testing.T) {
	h := newHarness(t, presentGroup)
	res, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("invocations = %v, want none", calls)
	}
}

func TestModify_ConvergesGid(t *testing.T) {
	h := newHarness(t, presentGroup)
	res, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	want := fmt.Sprintf("groupmod -g %d admins", presentGroup.gid+1)
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("invocations = %v, want %q", calls, want)
	}
	inv := h.rc.stats[sdk.StatInverse].(map[string]any)
	if inv["params"].(map[string]any)["gid"] != presentGroup.gid {
		t.Errorf("inverse gid = %v, want the old gid %d", inv["params"].(map[string]any)["gid"], presentGroup.gid)
	}
}

func TestModify_GroupmodFails(t *testing.T) {
	state := presentGroup
	state.mutateExit = 1
	h := newHarness(t, state)
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err == nil || !strings.Contains(err.Error(), "groupmod") {
		t.Errorf("err = %v, want it to name the failed groupmod invocation", err)
	}
}

func TestModify_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentGroup, 0)
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err == nil {
		t.Fatal("expected a connection failure on the initial getent query")
	}
}

func TestModify_GroupmodConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentGroup, 1)
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err == nil {
		t.Fatal("expected a connection failure on the groupmod call")
	}
}

func TestModify_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentGroup, 2)
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err == nil {
		t.Fatal("expected a connection failure on the post-modify requery")
	}
}

func TestModify_RecordStateNameFailure(t *testing.T) {
	h := newHarness(t, presentGroup)
	h.rc.failOnKey = "name"
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid}))
	if err == nil || !strings.Contains(err.Error(), `injected failure recording "name"`) {
		t.Errorf("err = %v, want the injected name-stat failure", err)
	}
}

func TestModify_RecordInverseFailure(t *testing.T) {
	h := newHarness(t, presentGroup)
	h.rc.failOnKey = sdk.StatInverse
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": presentGroup.gid + 1}))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}

func TestModify_BadGIDType(t *testing.T) {
	h := newHarness(t, presentGroup)
	_, err := group.Modify(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": 4000.5}))
	if err == nil || !strings.Contains(err.Error(), "not a whole number") {
		t.Errorf("err = %v, want it to refuse a fractional gid", err)
	}
}

// ---------- Remove ----------

func TestRemove_AbsentGroupIsNoop(t *testing.T) {
	h := newHarness(t, absentGroup)
	res, err := group.Remove(context.Background(), h.rc, h.device, h.params("ghosts", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("invocations = %v, want none", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("an inverse was recorded removing a group that was never there")
	}
}

func TestRemove_PresentGroupIsRemovedAndInverseCapturesGid(t *testing.T) {
	h := newHarness(t, presentGroup)
	res, err := group.Remove(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "groupdel admins" {
		t.Errorf("invocations = %v, want a plain groupdel call", calls)
	}
	inv := h.rc.stats[sdk.StatInverse].(map[string]any)
	if inv["fqcn"] != "identity.group.create" {
		t.Errorf("inverse fqcn = %v, want identity.group.create", inv["fqcn"])
	}
	if inv["params"].(map[string]any)["gid"] != presentGroup.gid {
		t.Errorf("inverse gid = %v, want %d", inv["params"].(map[string]any)["gid"], presentGroup.gid)
	}
}

func TestRemove_GroupdelFailsBecauseItIsAPrimaryGroup(t *testing.T) {
	state := presentGroup
	state.mutateExit = 8
	h := newHarness(t, state)
	_, err := group.Remove(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), "groupdel") || !strings.Contains(err.Error(), "exited 8") {
		t.Errorf("err = %v, want it to name the failed groupdel invocation", err)
	}
}

func TestRemove_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentGroup, 0)
	_, err := group.Remove(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the initial getent query")
	}
}

func TestRemove_GroupdelConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentGroup, 1)
	_, err := group.Remove(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the groupdel call")
	}
}

func TestRemove_RecordStateNameFailure(t *testing.T) {
	h := newHarness(t, absentGroup)
	h.rc.failOnKey = "name"
	_, err := group.Remove(context.Background(), h.rc, h.device, h.params("ghosts", nil))
	if err == nil || !strings.Contains(err.Error(), `injected failure recording "name"`) {
		t.Errorf("err = %v, want the injected name-stat failure", err)
	}
}

func TestRemove_RecordStateDiffFailure(t *testing.T) {
	h := newHarness(t, presentGroup)
	h.rc.failOnKey = sdk.StatDiff
	_, err := group.Remove(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatDiff)) {
		t.Errorf("err = %v, want the injected diff-stat failure", err)
	}
}

func TestRemove_RecordInverseFailure(t *testing.T) {
	h := newHarness(t, presentGroup)
	h.rc.failOnKey = sdk.StatInverse
	_, err := group.Remove(context.Background(), h.rc, h.device, h.params("admins", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}
