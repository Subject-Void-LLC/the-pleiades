package docker_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	dockermod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/container/docker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with docker on PATH as a fake shell script, so a test can set
// any state and fail any command. The fake dispatches on its own first
// argument (inspect/run/stop/rm) the way a real docker CLI would. The
// checks' control (docker_check_test.go) runs the real docker CLI against
// this machine's own daemon instead, on a disposable container, since only
// a real daemon shows what a real run leaves.

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
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NameDocker}}
}

// containerFixture is what the fake docker inspect reports, and how the
// fake behaves when asked to mutate.
type containerFixture struct {
	exists bool
	status string

	runExit        int
	runFailStream  string
	stopExit       int
	stopFailStream string
	rmExit         int
	rmFailStream   string

	// inspectUnreachable makes docker inspect fail the way it does when the
	// CLI cannot reach its daemon, which is not an absent container.
	inspectUnreachable bool

	// runLeftover is the status of the container a failed docker run leaves
	// under the name ("created" for a start that failed); empty leaves none.
	runLeftover string
	// runFailMessage replaces the fake's failure text, such as docker's own
	// "is already in use" conflict.
	runFailMessage string
	// runStdout, when set, replaces the container id a successful docker run
	// prints.
	runStdout *string
}

// fakeContainerID is the full id the fake docker inspect reports for any
// container that exists.
const fakeContainerID = "4c1f0e6a9b2d3c5e7f8a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d1e"

var (
	absent  = containerFixture{}
	running = containerFixture{exists: true, status: "running"}
	stopped = containerFixture{exists: true, status: "exited"}
)

type harness struct {
	rc     *ctxStub
	device inventory.InventoryItem
	record string
}

func newHarness(t *testing.T, fixture containerFixture) *harness {
	t.Helper()
	return newHarnessBudgeted(t, fixture, -1)
}

func newHarnessBudgeted(t *testing.T, fixture containerFixture, budget int) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	script := `#!/bin/sh
case "$1" in
  inspect)
    # A daemon the CLI cannot reach, worded as Docker 29 words it.
    if [ "$FAKE_INSPECT_UNREACHABLE" = "1" ]; then
      printf 'failed to connect to the docker API at unix:///var/run/docker.sock\n' >&2
      exit 1
    fi
    status="$FAKE_STATUS"
    # A container a successful run created exists afterwards, running, as
    # it would on a real daemon.
    if [ -f "$FAKE_RECORD.created" ]; then
      FAKE_EXISTS=1
      status=running
    fi
    # A container a failed run left behind, in the status the test chose.
    if [ -f "$FAKE_RECORD.leftover" ]; then
      FAKE_EXISTS=1
      status="$(cat "$FAKE_RECORD.leftover")"
    fi
    template=""
    last=""
    for a in "$@"; do
      case "$a" in --format=*) template="${a#--format=}" ;; esac
      last="$a"
    done
    if [ "$FAKE_EXISTS" = "1" ]; then
      # Render --format as the real CLI does.
      printf '%s\n' "$template" | sed -e "s/{{.Id}}/$FAKE_ID/" -e "s/{{.State.Status}}/$status/"
      exit 0
    fi
    printf 'Error response from daemon: No such container: %s\n' "$last" >&2
    exit 1
    ;;
  run)
    # Read an env file from standard input the way the real CLI does, so a
    # test sees exactly what reached it there.
    case " $* " in
      *" --env-file=/dev/stdin "*) cat > "$FAKE_RECORD.env" ;;
    esac
    printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
    for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_RECORD"; done
    printf -- '---\n' >> "$FAKE_RECORD"
    exit_code="${FAKE_RUN_EXIT:-0}"
    if [ "$exit_code" = "0" ]; then
      : > "$FAKE_RECORD.created"
      # docker run -d prints the new container's id, as the real CLI does.
      printf '%s\n' "${FAKE_RUN_STDOUT-$FAKE_ID}"
    fi
    if [ "$exit_code" != "0" ]; then
      if [ -n "$FAKE_RUN_LEFTOVER" ]; then
        printf '%s' "$FAKE_RUN_LEFTOVER" > "$FAKE_RECORD.leftover"
      fi
      message="${FAKE_RUN_FAIL_MESSAGE:-fake failure}"
      case "$FAKE_RUN_FAIL_STREAM" in
        stdout) printf '%s\n' "$message" ;;
        none) : ;;
        *) printf '%s\n' "$message" >&2 ;;
      esac
    fi
    exit "$exit_code"
    ;;
  stop)
    printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
    for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_RECORD"; done
    printf -- '---\n' >> "$FAKE_RECORD"
    exit_code="${FAKE_STOP_EXIT:-0}"
    if [ "$exit_code" != "0" ]; then
      case "$FAKE_STOP_FAIL_STREAM" in
        stdout) printf 'fake failure\n' ;;
        none) : ;;
        *) printf 'fake failure\n' >&2 ;;
      esac
    fi
    exit "$exit_code"
    ;;
  rm)
    printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
    for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_RECORD"; done
    printf -- '---\n' >> "$FAKE_RECORD"
    exit_code="${FAKE_RM_EXIT:-0}"
    if [ "$exit_code" = "0" ]; then
      rm -f "$FAKE_RECORD.leftover"
    fi
    if [ "$exit_code" != "0" ]; then
      case "$FAKE_RM_FAIL_STREAM" in
        stdout) printf 'fake failure\n' ;;
        none) : ;;
        *) printf 'fake failure\n' >&2 ;;
      esac
    fi
    exit "$exit_code"
    ;;
  *)
    exit 1
    ;;
esac
`
	writeScript(t, dir, "docker", script)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_EXISTS", boolEnv(fixture.exists))
	t.Setenv("FAKE_STATUS", fixture.status)
	t.Setenv("FAKE_ID", fakeContainerID)
	t.Setenv("FAKE_RUN_EXIT", strconv.Itoa(fixture.runExit))
	t.Setenv("FAKE_RUN_FAIL_STREAM", fixture.runFailStream)
	t.Setenv("FAKE_STOP_EXIT", strconv.Itoa(fixture.stopExit))
	t.Setenv("FAKE_STOP_FAIL_STREAM", fixture.stopFailStream)
	t.Setenv("FAKE_RM_EXIT", strconv.Itoa(fixture.rmExit))
	t.Setenv("FAKE_RM_FAIL_STREAM", fixture.rmFailStream)
	t.Setenv("FAKE_INSPECT_UNREACHABLE", boolEnv(fixture.inspectUnreachable))
	t.Setenv("FAKE_RUN_LEFTOVER", fixture.runLeftover)
	t.Setenv("FAKE_RUN_FAIL_MESSAGE", fixture.runFailMessage)
	if fixture.runStdout != nil {
		t.Setenv("FAKE_RUN_STDOUT", *fixture.runStdout)
	}

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
			Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameDocker}},
			host: srv.Host, port: srv.Port,
		},
		record: record,
	}
}

func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func writeScript(t *testing.T, dir, name, script string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { // #nosec G306 -- test fixture that must be executable
		t.Fatalf("writing the fake %s: %v", name, err)
	}
}

func (h *harness) params(extra map[string]any) map[string]any {
	p := map[string]any{"insecure_skip_host_key_verify": true}
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
	for _, fqcn := range []string{"container.docker.run", "container.docker.stop", "container.docker.remove"} {
		desc := lookup(t, fqcn)
		if desc.Manifest.Status != collection.StatusImplemented {
			t.Errorf("%s: Status = %v, want StatusImplemented", fqcn, desc.Manifest.Status)
		}
		if desc.Invoke == nil {
			t.Errorf("%s: Invoke is nil", fqcn)
		}
		if desc.Manifest.Doc.Summary == "" {
			t.Errorf("%s: Doc.Summary is empty", fqcn)
		}
	}
	if !lookup(t, "container.docker.run").Manifest.Reversibility.Reversible {
		t.Error("container.docker.run: expected Reversible: true")
	}
	if lookup(t, "container.docker.stop").Manifest.Reversibility.Reversible {
		t.Error("container.docker.stop: expected Reversible: false")
	}
	if lookup(t, "container.docker.remove").Manifest.Reversibility.Reversible {
		t.Error("container.docker.remove: expected Reversible: false")
	}
}

// ---------- Run ----------

func TestRun_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := dockermod.Run(context.Background(), rc, noSSHDevice(), map[string]any{"name": "web", "image": "nginx"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestRun_MissingRequiredParams(t *testing.T) {
	h := newHarness(t, absent)
	for _, params := range []map[string]any{
		{"image": "nginx"},
		{"name": "web"},
	} {
		if _, err := dockermod.Run(context.Background(), h.rc, h.device, params); err == nil {
			t.Errorf("params %v: expected a required-param error", params)
		}
	}
}

func TestRun_AbsentCreatesContainer(t *testing.T) {
	h := newHarness(t, absent)
	result, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx:1.27", "ports": []any{"8080:80"}, "volumes": []any{"/data:/data"},
		"env": map[string]any{"FOO": "bar"}, "restart_policy": "unless-stopped",
		"command": []any{"nginx", "-g", "daemon off;"},
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 {
		t.Fatalf("invocations = %v", calls)
	}
	call := calls[0]
	for _, want := range []string{"-d", "--name=web", "--publish=8080:80", "--volume=/data:/data", "--env-file=/dev/stdin", "--restart=unless-stopped", " -- nginx:1.27 nginx -g daemon off;"} {
		if !strings.Contains(call, want) {
			t.Errorf("call = %q, want it to contain %q", call, want)
		}
	}
	// Finding S2: a value on the command line is readable by anyone on the
	// host with ps, and is quoted back in a failed run's error.
	if strings.Contains(call, "bar") {
		t.Errorf("call = %q, which carries an environment value on the command line", call)
	}
	envFile, err := os.ReadFile(h.record + ".env")
	if err != nil {
		t.Fatalf("reading the env file the fake docker received on stdin: %v", err)
	}
	if string(envFile) != "FOO=bar\n" {
		t.Errorf("env file on stdin = %q, want %q", envFile, "FOO=bar\n")
	}
	fqcn, params, ok := inverseOf(h.rc)
	if !ok || fqcn != "container.docker.remove" || params["name"] != "web" || params["force"] != true || params["id"] != fakeContainerID {
		t.Fatalf("inverse = %q, %v, ok=%v; want it pinned to the container run made", fqcn, params, ok)
	}
}

// TestRemove_TheIDGuardRefusesAnotherContainerOfTheName is B3's identity
// guard: the undo of container.docker.run names the container it made by
// id, so a container run later under the same name survives a rollback of
// the earlier run, in a check and for real, while the right id removes it.
func TestRemove_TheIDGuardRefusesAnotherContainerOfTheName(t *testing.T) {
	h := newHarness(t, stopped)
	stranger := h.params(map[string]any{"name": "web", "id": strings.Repeat("f", 64)})
	for _, remove := range []func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error){dockermod.CheckRemove, dockermod.Remove} {
		if _, err := remove(context.Background(), h.rc, h.device, stranger); err == nil || !strings.Contains(err.Error(), "nothing was removed") {
			t.Fatalf("remove with another container's id = %v, want it refused", err)
		}
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("a refused remove sent %v", calls)
	}
	result, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "id": fakeContainerID}))
	if err != nil || !result.Changed {
		t.Fatalf("remove with the container's own id = %+v, %v", result, err)
	}
	if calls := h.invocations(t); len(calls) != 1 || calls[0] != "docker rm -- web" {
		t.Fatalf("invocations = %v", calls)
	}
}

func TestRun_AlreadyExistsIsNoOp(t *testing.T) {
	h := newHarness(t, running)
	result, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no docker run invocation, got %v", calls)
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a no-op")
	}
}

func TestRun_EnvNotAMap(t *testing.T) {
	h := newHarness(t, absent)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx", "env": "not-a-map",
	})); err == nil {
		t.Fatal("expected an error for a non-map env")
	}
}

func TestRun_EnvValueNotAString(t *testing.T) {
	h := newHarness(t, absent)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx", "env": map[string]any{"FOO": 123},
	})); err == nil {
		t.Fatal("expected an error for a non-string env value")
	}
}

// TestRun_EnvEntriesAnEnvFileCannotCarryAreRefused proves the names and
// values docker's env-file format would change are refused before anything
// connects, rather than arriving as something the runbook did not write.
func TestRun_EnvEntriesAnEnvFileCannotCarryAreRefused(t *testing.T) {
	for name, env := range map[string]map[string]any{
		"a line break in a value": {"FOO": "bar\nBAZ=evil"},
		"a carriage return":       {"FOO": "bar\r"},
		"a NUL byte in a value":   {"FOO": "bar\x00"},
		"a name starting with #":  {"#FOO": "bar"},
		"a name with a space":     {"FOO BAR": "x"},
		"a name with =":           {"FOO=BAR": "x"},
		"an empty name":           {"": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, absent)
			_, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx", "env": env}))
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("invocations = %v, want none", calls)
			}
		})
	}
}

func TestRun_CommandNotAList(t *testing.T) {
	h := newHarness(t, absent)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx", "command": "not-a-list",
	})); err == nil {
		t.Fatal("expected an error for a non-list command")
	}
}

func TestRun_PortsNotAList(t *testing.T) {
	h := newHarness(t, absent)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx", "ports": "not-a-list",
	})); err == nil {
		t.Fatal("expected an error for a non-list ports")
	}
}

func TestRun_VolumesNotAList(t *testing.T) {
	h := newHarness(t, absent)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx", "volumes": "not-a-list",
	})); err == nil {
		t.Fatal("expected an error for a non-list volumes")
	}
}

func TestRun_RunFailure(t *testing.T) {
	fixture := absent
	fixture.runExit = 1
	h := newHarness(t, fixture)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	})); err == nil {
		t.Fatal("expected the run failure to surface")
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded on failure")
	}
}

func TestRun_RunFailsWithStdoutOnly(t *testing.T) {
	fixture := absent
	fixture.runExit = 1
	fixture.runFailStream = "stdout"
	h := newHarness(t, fixture)
	_, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx"}))
	if err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Fatalf("Run error = %v, want the fake stdout failure surfaced", err)
	}
}

func TestRun_RunFailsSilently(t *testing.T) {
	fixture := absent
	fixture.runExit = 1
	fixture.runFailStream = "none"
	h := newHarness(t, fixture)
	_, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "image": "nginx"}))
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("Run error = %v, want it to mention 'no output'", err)
	}
}

func TestRun_ConnectionDiesQuerying(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 0)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	})); err == nil {
		t.Fatal("expected a connection failure querying the container")
	}
}

func TestRun_ConnectionDiesRunning(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 1)
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	})); err == nil {
		t.Fatal("expected a connection failure running the container")
	}
}

// TestRun_ConnectionDiesQueryingAfter: once docker run has created the
// container and printed its id, losing the connection before it is read back
// cannot fail the task, since the engine would drop the undo of a container
// that exists. It succeeds, says what it could not read, and keeps the undo
// pinned to the id docker printed.
func TestRun_ConnectionDiesQueryingAfter(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 2)
	result, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	}))
	if err != nil || !result.Changed {
		t.Fatalf("Run = %+v, %v; want a change reported", result, err)
	}
	if warnings, _ := h.rc.stats[sdk.StatWarnings].([]string); len(warnings) != 1 || !strings.Contains(warnings[0], "could not read it back") {
		t.Errorf("warnings = %v, want the failed read named", h.rc.stats[sdk.StatWarnings])
	}
	if _, params, ok := inverseOf(h.rc); !ok || params["id"] != fakeContainerID {
		t.Fatalf("inverse params = %v, ok=%v; want it pinned to the id docker run printed", params, ok)
	}
}

func TestRun_RecordStatFails(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = "name"
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestRun_RecordDiffFails(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = sdk.StatDiff
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	})); err == nil {
		t.Fatal("expected the injected diff-recording failure to surface")
	}
}

func TestRun_RecordInverseFails(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = sdk.StatInverse
	if _, err := dockermod.Run(context.Background(), h.rc, h.device, h.params(map[string]any{
		"name": "web", "image": "nginx",
	})); err == nil {
		t.Fatal("expected the injected inverse-recording failure to surface")
	}
}

// ---------- Stop ----------

func TestStop_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := dockermod.Stop(context.Background(), rc, noSSHDevice(), map[string]any{"name": "web"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestStop_MissingName(t *testing.T) {
	h := newHarness(t, running)
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, map[string]any{}); err == nil {
		t.Fatal("expected a required-param error")
	}
}

func TestStop_RunningStops(t *testing.T) {
	h := newHarness(t, running)
	result, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "docker stop -- web" {
		t.Fatalf("invocations = %v", calls)
	}
}

func TestStop_AlreadyStoppedIsNoOp(t *testing.T) {
	h := newHarness(t, stopped)
	result, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no docker stop invocation, got %v", calls)
	}
}

func TestStop_AbsentIsNoOp(t *testing.T) {
	h := newHarness(t, absent)
	result, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
}

func TestStop_Failure(t *testing.T) {
	fixture := running
	fixture.stopExit = 1
	h := newHarness(t, fixture)
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected the stop failure to surface")
	}
}

func TestStop_ConnectionDiesQuerying(t *testing.T) {
	h := newHarnessBudgeted(t, running, 0)
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected a connection failure querying the container")
	}
}

func TestStop_ConnectionDiesStopping(t *testing.T) {
	h := newHarnessBudgeted(t, running, 1)
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected a connection failure stopping the container")
	}
}

func TestStop_ConnectionDiesQueryingAfter(t *testing.T) {
	h := newHarnessBudgeted(t, running, 2)
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected a connection failure re-querying the container after stopping")
	}
}

func TestStop_RecordStatFails(t *testing.T) {
	h := newHarness(t, running)
	h.rc.failOnKey = "name"
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestStop_RecordDiffFails(t *testing.T) {
	h := newHarness(t, running)
	h.rc.failOnKey = sdk.StatDiff
	if _, err := dockermod.Stop(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected the injected diff-recording failure to surface")
	}
}

// ---------- Remove ----------

func TestRemove_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := dockermod.Remove(context.Background(), rc, noSSHDevice(), map[string]any{"name": "web"})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestRemove_MissingName(t *testing.T) {
	h := newHarness(t, stopped)
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, map[string]any{}); err == nil {
		t.Fatal("expected a required-param error")
	}
}

func TestRemove_PresentRemoves(t *testing.T) {
	h := newHarness(t, stopped)
	result, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "docker rm -- web" {
		t.Fatalf("invocations = %v", calls)
	}
}

func TestRemove_ForcePassesFlag(t *testing.T) {
	h := newHarness(t, running)
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "force": true})); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	calls := h.invocations(t)
	if len(calls) != 1 || calls[0] != "docker rm --force -- web" {
		t.Fatalf("invocations = %v", calls)
	}
}

func TestRemove_AbsentIsNoOp(t *testing.T) {
	h := newHarness(t, absent)
	result, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no docker rm invocation, got %v", calls)
	}
}

func TestRemove_ForceNotABool(t *testing.T) {
	h := newHarness(t, stopped)
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web", "force": "yes"})); err == nil {
		t.Fatal("expected an error for a non-bool force")
	}
}

func TestRemove_Failure(t *testing.T) {
	fixture := stopped
	fixture.rmExit = 1
	h := newHarness(t, fixture)
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected the rm failure to surface")
	}
}

func TestRemove_FailsWithStdoutOnly(t *testing.T) {
	fixture := stopped
	fixture.rmExit = 1
	fixture.rmFailStream = "stdout"
	h := newHarness(t, fixture)
	_, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Fatalf("Remove error = %v, want the fake stdout failure surfaced", err)
	}
}

func TestRemove_FailsSilently(t *testing.T) {
	fixture := stopped
	fixture.rmExit = 1
	fixture.rmFailStream = "none"
	h := newHarness(t, fixture)
	_, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("Remove error = %v, want it to mention 'no output'", err)
	}
}

func TestRemove_ConnectionDiesQuerying(t *testing.T) {
	h := newHarnessBudgeted(t, stopped, 0)
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected a connection failure querying the container")
	}
}

func TestRemove_ConnectionDiesRemoving(t *testing.T) {
	h := newHarnessBudgeted(t, stopped, 1)
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected a connection failure removing the container")
	}
}

func TestRemove_RecordStatFails(t *testing.T) {
	h := newHarness(t, stopped)
	h.rc.failOnKey = "name"
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestRemove_RecordDiffFails(t *testing.T) {
	h := newHarness(t, stopped)
	h.rc.failOnKey = sdk.StatDiff
	if _, err := dockermod.Remove(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"})); err == nil {
		t.Fatal("expected the injected diff-recording failure to surface")
	}
}
