package user_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/user"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with getent, useradd, usermod and userdel on PATH as shell
// scripts. The SSH transport, the shell, the quoting, the argument
// vector and the exit status are all genuine; only the account database
// at the far end is not. Proving these against a real getent/useradd is
// a container Release Gate's job; pkg.apt.* already ships at this same
// tier with no such gate, and this namespace matches it.

// ---------- harness ----------

type ctxStub struct {
	secrets map[string]string
	stats   map[string]any

	// failOnKey, when set, makes SetStat fail for exactly that key and
	// succeed for every other one.
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

// noSSHDevice satisfies capability.PosixAccountCapable's structural
// needs for these tests (via Caps) but implements no SSH accessor at
// all, so sdk.Connect refuses it before anything is dialed.
func noSSHDevice() inventory.InventoryItem {
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NamePosixAccount}}
}

// userState is what the fake getent/useradd/usermod/userdel report.
type userState struct {
	exists  bool
	uid     int
	gid     int
	comment string
	home    string
	shell   string

	// passwdExitOverride forces getent passwd's exit code to something
	// other than the exists-derived default (0 present, 2 absent); used
	// only to reach the genuine getent-failure branch (a code that is
	// neither 0 nor 2).
	passwdExitOverride int

	groupGID          int
	groupExitOverride int // 0 means success; set to reach not-found (2) or a real failure (1)

	mutateExit       int    // exit code useradd/usermod/userdel themselves return
	mutateFailStream string // "" (stderr, default), "stdout", or "none" - which stream a mutate failure writes to

	// passwdRawLine and groupRawLine, when set, replace the computed
	// getent output verbatim, reaching queryUser's and resolveGroupGID's
	// own malformed-output and not-a-number branches, which no
	// successful, well-formed fixture can reach.
	passwdRawLine string
	groupRawLine  string
}

var (
	absentUser  = userState{}
	presentUser = userState{
		exists: true, uid: 1000, gid: 1000, comment: "Deploy", home: "/home/deploy", shell: "/bin/bash",
	}
)

// harness wires a real SSH server, fake getent/useradd/usermod/userdel
// and a device, and returns everything a method call needs plus a way
// to read back which commands were actually sent.
type harness struct {
	rc     *ctxStub
	device inventory.InventoryItem
	record string
}

func newHarness(t *testing.T, state userState) *harness {
	t.Helper()
	return newHarnessBudgeted(t, state, -1)
}

// newHarnessBudgeted is newHarness with a cap on how many SSH session
// channels the server accepts before refusing every further one, the
// same technique internal/catalog/pkg/apt's own tests use to reach a
// connection dying at one specific call site in a method's own
// sequence. A negative budget means unlimited.
func newHarnessBudgeted(t *testing.T, state userState, budget int) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	passwdExit := 2
	if state.exists {
		passwdExit = 0
	}
	if state.passwdExitOverride != 0 {
		passwdExit = state.passwdExitOverride
	}

	getentScript := `#!/bin/sh
db="$1"
key="$2"
if [ "$db" = "passwd" ]; then
  exit_code="$FAKE_PASSWD_EXIT"
  if [ "$exit_code" = "0" ]; then
    if [ -n "$FAKE_PASSWD_RAW" ]; then
      printf '%s\n' "$FAKE_PASSWD_RAW"
    else
      printf '%s:x:%s:%s:%s:%s:%s\n' "$key" "$FAKE_UID" "$FAKE_GID" "$FAKE_COMMENT" "$FAKE_HOME" "$FAKE_SHELL"
    fi
  elif [ "$exit_code" != "2" ]; then
    printf 'getent: fake failure resolving passwd %s\n' "$key" >&2
  fi
  exit "$exit_code"
fi
if [ "$db" = "group" ]; then
  exit_code="$FAKE_GROUP_EXIT"
  if [ "$exit_code" = "0" ]; then
    if [ -n "$FAKE_GROUP_RAW" ]; then
      printf '%s\n' "$FAKE_GROUP_RAW"
    else
      printf '%s:x:%s:\n' "$key" "$FAKE_GROUP_GID"
    fi
  elif [ "$exit_code" != "2" ]; then
    printf 'getent: fake failure resolving group %s\n' "$key" >&2
  fi
  exit "$exit_code"
fi
exit 1
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
	writeScript(t, dir, "useradd", recorderScript)
	writeScript(t, dir, "usermod", recorderScript)
	writeScript(t, dir, "userdel", recorderScript)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_PASSWD_EXIT", strconv.Itoa(passwdExit))
	t.Setenv("FAKE_UID", strconv.Itoa(state.uid))
	t.Setenv("FAKE_GID", strconv.Itoa(state.gid))
	t.Setenv("FAKE_COMMENT", state.comment)
	t.Setenv("FAKE_HOME", state.home)
	t.Setenv("FAKE_SHELL", state.shell)
	t.Setenv("FAKE_GROUP_GID", strconv.Itoa(state.groupGID))
	t.Setenv("FAKE_GROUP_EXIT", strconv.Itoa(state.groupExitOverride))
	t.Setenv("FAKE_MUTATE_EXIT", strconv.Itoa(state.mutateExit))
	t.Setenv("FAKE_MUTATE_FAIL_STREAM", state.mutateFailStream)
	t.Setenv("FAKE_PASSWD_RAW", state.passwdRawLine)
	t.Setenv("FAKE_GROUP_RAW", state.groupRawLine)

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

// invocations returns the argv of every useradd/usermod/userdel call
// actually sent, one entry per call joined by spaces (first token is
// the binary name), which is what proves a converged run sent nothing
// and an acting run sent exactly the command expected.
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
	tests := []struct {
		fqcn       string
		reversible bool
	}{
		{"identity.user.create", true},
		{"identity.user.modify", true},
		{"identity.user.remove", true},
	}
	for _, tc := range tests {
		t.Run(tc.fqcn, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok {
				t.Fatalf("collection.Lookup(%q) found nothing", tc.fqcn)
			}
			if d.Manifest.Status != collection.StatusImplemented {
				t.Errorf("Status = %v, want StatusImplemented", d.Manifest.Status)
			}
			if !containsCap(d.Manifest.RequiredCapabilities, capability.NamePosixAccount) {
				t.Errorf("RequiredCapabilities = %v, want it to include %v", d.Manifest.RequiredCapabilities, capability.NamePosixAccount)
			}
			if !d.Manifest.ExecutionContext.RequiresElevation {
				t.Errorf("RequiresElevation = false, want true")
			}
			if d.Manifest.Reversibility.Reversible != tc.reversible {
				t.Errorf("Reversibility.Reversible = %v, want %v", d.Manifest.Reversibility.Reversible, tc.reversible)
			}
			if d.Manifest.Reversibility.Reversible && d.Manifest.Reversibility.Notes == "" {
				t.Errorf("Reversibility.Notes is empty on a reversible method")
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
	_, err := user.Create(context.Background(), rc, noSSHDevice(), map[string]any{"name": "deploy"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestModify_NoSSHConnection(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
	_, err := user.Modify(context.Background(), rc, noSSHDevice(), map[string]any{"name": "deploy", "shell": "/bin/zsh"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestRemove_NoSSHConnection(t *testing.T) {
	rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
	_, err := user.Remove(context.Background(), rc, noSSHDevice(), map[string]any{"name": "deploy"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

// ---------- required params ----------

func TestCreate_MissingName(t *testing.T) {
	h := newHarness(t, absentUser)
	_, err := user.Create(context.Background(), h.rc, h.device, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want it to say name is required", err)
	}
}

func TestModify_MissingName(t *testing.T) {
	h := newHarness(t, absentUser)
	_, err := user.Modify(context.Background(), h.rc, h.device, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want it to say name is required", err)
	}
}

func TestRemove_MissingName(t *testing.T) {
	h := newHarness(t, absentUser)
	_, err := user.Remove(context.Background(), h.rc, h.device, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("err = %v, want it to say name is required", err)
	}
}

// ---------- Create ----------

func TestCreate_AbsentAccountIsCreated(t *testing.T) {
	h := newHarness(t, absentUser)
	res, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{
		"uid": 5000, "group": "admins", "shell": "/bin/bash", "home": "/home/deploy", "comment": "Deploy",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 {
		t.Fatalf("invocations = %v, want exactly one useradd call", calls)
	}
	want := "useradd -u 5000 -g admins -s /bin/bash -d /home/deploy -c Deploy -m deploy"
	if calls[0] != want {
		t.Errorf("useradd call = %q, want %q", calls[0], want)
	}
	if h.rc.stats["name"] != "deploy" {
		t.Errorf("name stat = %v, want deploy", h.rc.stats["name"])
	}
	inv, ok := h.rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("no inverse recorded")
	}
	if inv["fqcn"] != "identity.user.remove" {
		t.Errorf("inverse fqcn = %v, want identity.user.remove", inv["fqcn"])
	}
	if inv["params"].(map[string]any)["name"] != "deploy" {
		t.Errorf("inverse params = %v, want name=deploy", inv["params"])
	}
}

func TestCreate_AbsentAccountDefaultsCreateHomeAndSystemFalse(t *testing.T) {
	h := newHarness(t, absentUser)
	if _, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := h.invocations(t)
	if calls[0] != "useradd -m deploy" {
		t.Errorf("useradd call = %q, want %q", calls[0], "useradd -m deploy")
	}
}

func TestCreate_AbsentAccountCreateHomeFalseAndSystemTrue(t *testing.T) {
	h := newHarness(t, absentUser)
	if _, err := user.Create(context.Background(), h.rc, h.device, h.params("svc", map[string]any{
		"create_home": false, "system": true,
	})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	calls := h.invocations(t)
	want := "useradd -r -M svc"
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("useradd call = %v, want %q", calls, want)
	}
}

func TestCreate_PresentAccountAlreadyMatchingIsNoop(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{
		"uid": presentUser.uid, "shell": presentUser.shell, "home": presentUser.home, "comment": presentUser.comment,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false: every requested attribute already matched")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("invocations = %v, want none", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("an inverse was recorded on a no-op run")
	}
}

func TestCreate_PresentAccountConvergesShell(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{
		"shell": "/usr/sbin/nologin",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "usermod -s /usr/sbin/nologin deploy" {
		t.Errorf("invocations = %v, want a single usermod -s call", calls)
	}
	inv, ok := h.rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("no inverse recorded")
	}
	if inv["fqcn"] != "identity.user.modify" {
		t.Errorf("inverse fqcn = %v, want identity.user.modify", inv["fqcn"])
	}
	invParams := inv["params"].(map[string]any)
	if invParams["shell"] != presentUser.shell {
		t.Errorf("inverse shell = %v, want the old shell %q", invParams["shell"], presentUser.shell)
	}
	if _, hasUID := invParams["uid"]; hasUID {
		t.Errorf("inverse params = %v, want no uid entry since uid did not change", invParams)
	}
}

func TestCreate_PresentAccountConvergesGroupByResolvedGID(t *testing.T) {
	state := presentUser
	state.groupGID = 2000 // differs from presentUser.gid (1000)
	h := newHarness(t, state)
	res, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "admins"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "usermod -g admins deploy" {
		t.Errorf("invocations = %v, want a single usermod -g admins call", calls)
	}
}

func TestCreate_PresentAccountGroupAlreadyResolvesToCurrentGID(t *testing.T) {
	state := presentUser
	state.groupGID = presentUser.gid // matches, by a different name than getent passwd reports
	h := newHarness(t, state)
	res, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "deployers"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false: the requested group already resolves to the current gid")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("invocations = %v, want none", calls)
	}
}

func TestCreate_GroupDoesNotExist(t *testing.T) {
	state := presentUser
	state.groupExitOverride = 2
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "ghosts"}))
	if err == nil || !strings.Contains(err.Error(), `group "ghosts" does not exist`) {
		t.Errorf("err = %v, want it to say the group does not exist", err)
	}
}

func TestCreate_GroupLookupFails(t *testing.T) {
	state := presentUser
	state.groupExitOverride = 1
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "admins"}))
	if err == nil || !strings.Contains(err.Error(), "getent group") {
		t.Errorf("err = %v, want it to mention the getent group failure", err)
	}
}

func TestCreate_UseraddFails(t *testing.T) {
	state := absentUser
	state.mutateExit = 1
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "useradd") || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("err = %v, want it to name the failed useradd invocation", err)
	}
}

func TestCreate_UsermodFails(t *testing.T) {
	state := presentUser
	state.mutateExit = 1
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil || !strings.Contains(err.Error(), "usermod") || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("err = %v, want it to name the failed usermod invocation", err)
	}
}

func TestCreate_InitialQueryGetentFailure(t *testing.T) {
	state := absentUser
	state.passwdExitOverride = 1
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "getent passwd") {
		t.Errorf("err = %v, want it to mention the getent passwd failure", err)
	}
}

func TestCreate_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absentUser, 0)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the initial getent passwd query")
	}
}

func TestCreate_UseraddConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absentUser, 1)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the useradd call")
	}
}

func TestCreate_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absentUser, 2)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the post-create requery")
	}
}

func TestCreate_GroupResolutionConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentUser, 1)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "admins"}))
	if err == nil {
		t.Fatal("expected a connection failure resolving the group")
	}
}

func TestCreate_UsermodConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentUser, 1)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil {
		t.Fatal("expected a connection failure on the usermod call")
	}
}

func TestCreate_BadUIDType(t *testing.T) {
	h := newHarness(t, absentUser)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"uid": 5000.5}))
	if err == nil || !strings.Contains(err.Error(), "not a whole number") {
		t.Errorf("err = %v, want it to refuse a fractional uid", err)
	}
}

func TestCreate_BadCreateHomeType(t *testing.T) {
	h := newHarness(t, absentUser)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"create_home": "yes"}))
	if err == nil {
		t.Fatal("expected create_home to refuse a non-boolean value")
	}
}

func TestCreate_BadSystemType(t *testing.T) {
	h := newHarness(t, absentUser)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"system": "yes"}))
	if err == nil {
		t.Fatal("expected system to refuse a non-boolean value")
	}
}

func TestCreate_RecordStateNameFailure(t *testing.T) {
	h := newHarness(t, absentUser)
	h.rc.failOnKey = "name"
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), `injected failure recording "name"`) {
		t.Errorf("err = %v, want the injected name-stat failure", err)
	}
}

func TestCreate_RecordStateDiffFailure(t *testing.T) {
	h := newHarness(t, absentUser)
	h.rc.failOnKey = sdk.StatDiff
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatDiff)) {
		t.Errorf("err = %v, want the injected diff-stat failure", err)
	}
}

func TestCreate_RecordInverseFailureOnCreatePath(t *testing.T) {
	h := newHarness(t, absentUser)
	h.rc.failOnKey = sdk.StatInverse
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}

func TestCreate_MalformedPasswdOutput(t *testing.T) {
	state := absentUser
	state.exists = true
	state.passwdRawLine = "deploy:x:1000"
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "unexpected output") {
		t.Errorf("err = %v, want it to reject the malformed getent passwd line", err)
	}
}

func TestCreate_PasswdUIDNotANumber(t *testing.T) {
	state := absentUser
	state.exists = true
	state.passwdRawLine = "deploy:x:notanumber:1000:Deploy:/home/deploy:/bin/bash"
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "uid field") {
		t.Errorf("err = %v, want it to reject the non-numeric uid field", err)
	}
}

func TestCreate_PasswdGIDNotANumber(t *testing.T) {
	state := absentUser
	state.exists = true
	state.passwdRawLine = "deploy:x:1000:notanumber:Deploy:/home/deploy:/bin/bash"
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "gid field") {
		t.Errorf("err = %v, want it to reject the non-numeric gid field", err)
	}
}

func TestCreate_GroupMalformedOutput(t *testing.T) {
	state := presentUser
	state.groupRawLine = "admins:x"
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "admins"}))
	if err == nil || !strings.Contains(err.Error(), "unexpected output") {
		t.Errorf("err = %v, want it to reject the malformed getent group line", err)
	}
}

func TestCreate_GroupGIDNotANumber(t *testing.T) {
	state := presentUser
	state.groupRawLine = "admins:x:notanumber:"
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "admins"}))
	if err == nil || !strings.Contains(err.Error(), "gid field") {
		t.Errorf("err = %v, want it to reject the non-numeric gid field", err)
	}
}

func TestCreate_UseraddFailsWithStdoutOnly(t *testing.T) {
	state := absentUser
	state.mutateExit = 1
	state.mutateFailStream = "stdout"
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Errorf("err = %v, want it to report the stdout-only failure detail", err)
	}
}

func TestCreate_UseraddFailsSilently(t *testing.T) {
	state := absentUser
	state.mutateExit = 1
	state.mutateFailStream = "none"
	h := newHarness(t, state)
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Errorf("err = %v, want it to report \"no output\"", err)
	}
}

func TestCreate_RecordInverseFailureOnConvergePath(t *testing.T) {
	h := newHarness(t, presentUser)
	h.rc.failOnKey = sdk.StatInverse
	_, err := user.Create(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}

// ---------- Modify ----------

func TestModify_AbsentAccountIsRefused(t *testing.T) {
	h := newHarness(t, absentUser)
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("ghost", map[string]any{"shell": "/bin/zsh"}))
	if err == nil || !strings.Contains(err.Error(), "no such user") {
		t.Errorf("err = %v, want it to refuse a non-existent account", err)
	}
}

func TestModify_NoAttributesRequestedIsNoop(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Changed {
		t.Error("Changed = true, want false: nothing was requested")
	}
}

func TestModify_ConvergesMultipleAttributes(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{
		"shell": "/usr/sbin/nologin", "comment": "Retired",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 {
		t.Fatalf("invocations = %v, want exactly one usermod call", calls)
	}
	if !strings.Contains(calls[0], "-s /usr/sbin/nologin") || !strings.Contains(calls[0], "-c Retired") {
		t.Errorf("usermod call = %q, want both -s and -c", calls[0])
	}
	inv := h.rc.stats[sdk.StatInverse].(map[string]any)
	invParams := inv["params"].(map[string]any)
	if invParams["shell"] != presentUser.shell || invParams["comment"] != presentUser.comment {
		t.Errorf("inverse params = %v, want the old shell and comment", invParams)
	}
}

func TestModify_UIDConverges(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"uid": presentUser.uid + 1}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	want := fmt.Sprintf("usermod -u %d deploy", presentUser.uid+1)
	if len(calls) != 1 || calls[0] != want {
		t.Errorf("invocations = %v, want %q", calls, want)
	}
}

func TestModify_HomeConverges(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"home": "/srv/deploy"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "usermod -d /srv/deploy deploy" {
		t.Errorf("invocations = %v, want a single usermod -d call", calls)
	}
}

func TestModify_GroupDoesNotExist(t *testing.T) {
	state := presentUser
	state.groupExitOverride = 2
	h := newHarness(t, state)
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "ghosts"}))
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("err = %v, want it to say the group does not exist", err)
	}
}

func TestModify_UsermodFails(t *testing.T) {
	state := presentUser
	state.mutateExit = 1
	h := newHarness(t, state)
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil || !strings.Contains(err.Error(), "usermod") {
		t.Errorf("err = %v, want it to name the failed usermod invocation", err)
	}
}

func TestModify_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentUser, 0)
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil {
		t.Fatal("expected a connection failure on the initial getent passwd query")
	}
}

func TestModify_UsermodConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentUser, 1)
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil {
		t.Fatal("expected a connection failure on the usermod call")
	}
}

func TestModify_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentUser, 2)
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil {
		t.Fatal("expected a connection failure on the post-modify requery")
	}
}

func TestModify_RecordStateNameFailure(t *testing.T) {
	h := newHarness(t, presentUser)
	h.rc.failOnKey = "name"
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), `injected failure recording "name"`) {
		t.Errorf("err = %v, want the injected name-stat failure", err)
	}
}

func TestModify_RecordInverseFailure(t *testing.T) {
	h := newHarness(t, presentUser)
	h.rc.failOnKey = sdk.StatInverse
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/zsh"}))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}

func TestModify_BadUIDType(t *testing.T) {
	h := newHarness(t, presentUser)
	_, err := user.Modify(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"uid": 5000.5}))
	if err == nil || !strings.Contains(err.Error(), "not a whole number") {
		t.Errorf("err = %v, want it to refuse a fractional uid", err)
	}
}

// ---------- Remove ----------

func TestRemove_AbsentAccountIsNoop(t *testing.T) {
	h := newHarness(t, absentUser)
	res, err := user.Remove(context.Background(), h.rc, h.device, h.params("ghost", nil))
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
		t.Error("an inverse was recorded removing an account that was never there")
	}
}

func TestRemove_PresentAccountIsRemovedAndInverseCapturesAttributes(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "userdel deploy" {
		t.Errorf("invocations = %v, want a plain userdel call", calls)
	}
	inv := h.rc.stats[sdk.StatInverse].(map[string]any)
	if inv["fqcn"] != "identity.user.create" {
		t.Errorf("inverse fqcn = %v, want identity.user.create", inv["fqcn"])
	}
	// The removed user's password never comes back, so the undo is partial.
	if inv[sdk.InversePartialKey] != true {
		t.Errorf("the undo of removing a user is not marked partial: %v", inv)
	}
	invParams := inv["params"].(map[string]any)
	if invParams["uid"] != presentUser.uid {
		t.Errorf("inverse uid = %v, want %d", invParams["uid"], presentUser.uid)
	}
	if invParams["group"] != strconv.Itoa(presentUser.gid) {
		t.Errorf("inverse group = %v, want %q", invParams["group"], strconv.Itoa(presentUser.gid))
	}
	if invParams["shell"] != presentUser.shell || invParams["home"] != presentUser.home || invParams["comment"] != presentUser.comment {
		t.Errorf("inverse params = %v, want the captured shell/home/comment", invParams)
	}
}

func TestRemove_WithRemoveHomeFlag(t *testing.T) {
	h := newHarness(t, presentUser)
	res, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove": true}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Changed {
		t.Error("Changed = false, want true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "userdel -r deploy" {
		t.Errorf("invocations = %v, want userdel -r", calls)
	}
}

func TestRemove_BadRemoveType(t *testing.T) {
	h := newHarness(t, presentUser)
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"remove": "yes"}))
	if err == nil {
		t.Fatal("expected remove to refuse a non-boolean value")
	}
}

func TestRemove_UserdelFails(t *testing.T) {
	state := presentUser
	state.mutateExit = 8
	h := newHarness(t, state)
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), "userdel") || !strings.Contains(err.Error(), "exited 8") {
		t.Errorf("err = %v, want it to name the failed userdel invocation", err)
	}
}

func TestRemove_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentUser, 0)
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the initial getent passwd query")
	}
}

func TestRemove_UserdelConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, presentUser, 1)
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil {
		t.Fatal("expected a connection failure on the userdel call")
	}
}

func TestRemove_RecordStateNameFailure(t *testing.T) {
	h := newHarness(t, absentUser)
	h.rc.failOnKey = "name"
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("ghost", nil))
	if err == nil || !strings.Contains(err.Error(), `injected failure recording "name"`) {
		t.Errorf("err = %v, want the injected name-stat failure", err)
	}
}

func TestRemove_RecordStateDiffFailure(t *testing.T) {
	h := newHarness(t, presentUser)
	h.rc.failOnKey = sdk.StatDiff
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatDiff)) {
		t.Errorf("err = %v, want the injected diff-stat failure", err)
	}
}

func TestRemove_RecordInverseFailure(t *testing.T) {
	h := newHarness(t, presentUser)
	h.rc.failOnKey = sdk.StatInverse
	_, err := user.Remove(context.Background(), h.rc, h.device, h.params("deploy", nil))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("injected failure recording %q", sdk.StatInverse)) {
		t.Errorf("err = %v, want the injected inverse-stat failure", err)
	}
}
