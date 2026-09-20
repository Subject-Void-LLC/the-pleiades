package firewalld_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	fwmod "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fw/firewalld"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with firewall-cmd on PATH as a fake shell script (there is no
// safe way to run a real one in a test process). The fake interprets its
// own argv the way a real firewall-cmd would: --permanent selects which
// of the two configurations a query or mutation targets, and the
// query/add/remove/reload flag itself selects the action.

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
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NameFirewalld}}
}

// ruleFixture is what the fake firewall-cmd reports for a --query-port
// or --query-service, and how it behaves when asked to mutate.
type ruleFixture struct {
	permanentAllowed bool
	runtimeAllowed   bool

	queryExitOverride int // 0 means use the computed yes/no exit; set to reach the real-failure branch

	mutateExit       int
	mutateFailStream string
	reloadExit       int
}

var (
	deniedEverywhere  = ruleFixture{}
	allowedEverywhere = ruleFixture{permanentAllowed: true, runtimeAllowed: true}
)

type harness struct {
	rc     *ctxStub
	device inventory.InventoryItem
	record string
}

func newHarness(t *testing.T, fixture ruleFixture) *harness {
	t.Helper()
	return newHarnessBudgeted(t, fixture, -1)
}

func newHarnessBudgeted(t *testing.T, fixture ruleFixture, budget int) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	script := `#!/bin/sh
permanent=0
for arg in "$@"; do
  case "$arg" in
    --permanent) permanent=1 ;;
  esac
done
for arg in "$@"; do
  case "$arg" in
    --query-port=*|--query-service=*)
      if [ "$permanent" = "1" ]; then allowed="$FAKE_PERMANENT_ALLOWED"; else allowed="$FAKE_RUNTIME_ALLOWED"; fi
      if [ -n "$FAKE_QUERY_EXIT_OVERRIDE" ] && [ "$FAKE_QUERY_EXIT_OVERRIDE" != "0" ]; then
        printf 'fake query failure\n' >&2
        exit "$FAKE_QUERY_EXIT_OVERRIDE"
      fi
      if [ "$allowed" = "1" ]; then printf 'yes\n'; exit 0; else printf 'no\n'; exit 1; fi
      ;;
    --add-port=*|--add-service=*|--remove-port=*|--remove-service=*)
      printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
      for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_RECORD"; done
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
      ;;
    --state)
      # firewall-cmd's own answer: "running" and 0, or "not running" and 252.
      if [ "${FAKE_NOT_RUNNING:-0}" = "1" ]; then printf 'not running\n'; exit 252; fi
      printf 'running\n'; exit 0
      ;;
    --reload)
      printf '%s\n' "$(basename "$0")" >> "$FAKE_RECORD"
      for a in "$@"; do printf '%s\n' "$a" >> "$FAKE_RECORD"; done
      printf -- '---\n' >> "$FAKE_RECORD"
      exit_code="${FAKE_RELOAD_EXIT:-0}"
      if [ "$exit_code" != "0" ]; then printf 'fake reload failure\n' >&2; fi
      exit "$exit_code"
      ;;
  esac
done
exit 1
`
	writeScript(t, dir, "firewall-cmd", script)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_PERMANENT_ALLOWED", boolEnv(fixture.permanentAllowed))
	t.Setenv("FAKE_RUNTIME_ALLOWED", boolEnv(fixture.runtimeAllowed))
	t.Setenv("FAKE_QUERY_EXIT_OVERRIDE", strconv.Itoa(fixture.queryExitOverride))
	t.Setenv("FAKE_MUTATE_EXIT", strconv.Itoa(fixture.mutateExit))
	t.Setenv("FAKE_MUTATE_FAIL_STREAM", fixture.mutateFailStream)
	t.Setenv("FAKE_RELOAD_EXIT", strconv.Itoa(fixture.reloadExit))

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
			Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameFirewalld}},
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
	for _, fqcn := range []string{"fw.firewalld.allow", "fw.firewalld.deny", "fw.firewalld.reload"} {
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
	if !lookup(t, "fw.firewalld.allow").Manifest.Reversibility.Reversible {
		t.Error("fw.firewalld.allow: expected Reversible: true")
	}
	if !lookup(t, "fw.firewalld.deny").Manifest.Reversibility.Reversible {
		t.Error("fw.firewalld.deny: expected Reversible: true")
	}
	if lookup(t, "fw.firewalld.reload").Manifest.Reversibility.Reversible {
		t.Error("fw.firewalld.reload: expected Reversible: false")
	}
}

// ---------- Allow ----------

func TestAllow_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := fwmod.Allow(context.Background(), rc, noSSHDevice(), map[string]any{"port": 443})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestAllow_BothPortAndServiceRefuses(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{
		"port": 443, "service": "https",
	})); err == nil {
		t.Fatal("expected a refusal for both port and service given")
	}
}

func TestAllow_NeitherPortNorServiceRefuses(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(nil)); err == nil {
		t.Fatal("expected a refusal for neither port nor service given")
	}
}

func TestAllow_AbsentAllowsBothByDefault(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	result, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 8080}))
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 2 {
		t.Fatalf("invocations = %v, want 2 (permanent + runtime)", calls)
	}
	if !strings.Contains(calls[0], "--permanent") || !strings.Contains(calls[0], "--add-port=8080/tcp") {
		t.Fatalf("first call = %q", calls[0])
	}
	if strings.Contains(calls[1], "--permanent") || !strings.Contains(calls[1], "--add-port=8080/tcp") {
		t.Fatalf("second call = %q", calls[1])
	}
	fqcn, params, ok := inverseOf(h.rc)
	if !ok || fqcn != "fw.firewalld.deny" {
		t.Fatalf("inverse = %q, %v, ok=%v", fqcn, params, ok)
	}
	if params["port"] != 8080 || params["permanent"] != true || params["immediate"] != true {
		t.Fatalf("inverse params = %v", params)
	}
}

func TestAllow_ServiceCustomProtocolIgnored(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{
		"service": "http", "zone": "internal",
	})); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	calls := h.invocations(t)
	for _, c := range calls {
		if !strings.Contains(c, "--add-service=http") || !strings.Contains(c, "--zone=internal") {
			t.Errorf("call = %q, want --add-service=http and --zone=internal", c)
		}
	}
}

func TestAllow_CustomProtocol(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{
		"port": 53, "protocol": "udp", "immediate": false,
	})); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "--add-port=53/udp") {
		t.Fatalf("invocations = %v", calls)
	}
}

func TestAllow_AlreadyAllowedIsNoOp(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	result, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443}))
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no mutating invocations, got %v", calls)
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded for a no-op")
	}
}

func TestAllow_PermanentOnlyMissingConvergesOnlyPermanent(t *testing.T) {
	fixture := ruleFixture{permanentAllowed: false, runtimeAllowed: true}
	h := newHarness(t, fixture)
	result, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443}))
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "--permanent") {
		t.Fatalf("invocations = %v, want exactly one permanent add", calls)
	}
	_, params, ok := inverseOf(h.rc)
	if !ok || params["permanent"] != true || params["immediate"] != false {
		t.Fatalf("inverse params = %v, ok=%v", params, ok)
	}
}

func TestAllow_MutationFailurePermanent(t *testing.T) {
	fixture := deniedEverywhere
	fixture.mutateExit = 1
	h := newHarness(t, fixture)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected the permanent mutation failure to surface")
	}
	if _, _, ok := inverseOf(h.rc); ok {
		t.Fatal("expected no inverse recorded on failure")
	}
}

func TestAllow_MutationFailureRuntime(t *testing.T) {
	fixture := ruleFixture{permanentAllowed: true, runtimeAllowed: false, mutateExit: 1}
	h := newHarness(t, fixture)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected the runtime mutation failure to surface")
	}
}

func TestAllow_MutationFailsWithStdoutOnly(t *testing.T) {
	fixture := deniedEverywhere
	fixture.mutateExit = 1
	fixture.mutateFailStream = "stdout"
	h := newHarness(t, fixture)
	_, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443}))
	if err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Fatalf("Allow error = %v, want the fake stdout failure surfaced", err)
	}
}

func TestAllow_MutationFailsSilently(t *testing.T) {
	fixture := deniedEverywhere
	fixture.mutateExit = 1
	fixture.mutateFailStream = "none"
	h := newHarness(t, fixture)
	_, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443}))
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("Allow error = %v, want it to mention 'no output'", err)
	}
}

func TestAllow_QueryRealFailure(t *testing.T) {
	fixture := deniedEverywhere
	fixture.queryExitOverride = 3
	h := newHarness(t, fixture)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected the real query failure to surface")
	}
}

func TestAllow_PermanentNotABool(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443, "permanent": "yes"})); err == nil {
		t.Fatal("expected an error for a non-bool permanent")
	}
}

func TestAllow_ImmediateNotABool(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443, "immediate": "yes"})); err == nil {
		t.Fatal("expected an error for a non-bool immediate")
	}
}

func TestAllow_ConnectionDiesQueryingPermanent(t *testing.T) {
	h := newHarnessBudgeted(t, deniedEverywhere, 0)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected a connection failure querying the permanent configuration")
	}
}

func TestAllow_ConnectionDiesQueryingRuntime(t *testing.T) {
	h := newHarnessBudgeted(t, deniedEverywhere, 1)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected a connection failure querying the runtime configuration")
	}
}

func TestAllow_ConnectionDiesAddingPermanent(t *testing.T) {
	h := newHarnessBudgeted(t, deniedEverywhere, 2)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected a connection failure adding the permanent rule")
	}
}

func TestAllow_ConnectionDiesAddingRuntime(t *testing.T) {
	h := newHarnessBudgeted(t, deniedEverywhere, 3)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected a connection failure adding the runtime rule")
	}
}

func TestAllow_ConnectionDiesInAfterQuery(t *testing.T) {
	h := newHarnessBudgeted(t, deniedEverywhere, 4)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected a connection failure re-querying state after the change")
	}
}

func TestAllow_RecordStatFails(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	h.rc.failOnKey = "zone"
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestAllow_RecordDiffFails(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	h.rc.failOnKey = sdk.StatDiff
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected the injected diff-recording failure to surface")
	}
}

func TestAllow_RecordInverseFails(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	h.rc.failOnKey = sdk.StatInverse
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443})); err == nil {
		t.Fatal("expected the injected inverse-recording failure to surface")
	}
}

func TestAllow_PortNotAWholeNumber(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	if _, err := fwmod.Allow(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 443.5})); err == nil {
		t.Fatal("expected an error for a fractional port")
	}
}

// ---------- Deny ----------

func TestDeny_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := fwmod.Deny(context.Background(), rc, noSSHDevice(), map[string]any{"port": 23})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestDeny_BothPortAndServiceRefuses(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{
		"port": 23, "service": "telnet",
	})); err == nil {
		t.Fatal("expected a refusal for both port and service given")
	}
}

func TestDeny_AllowedRemovesBothByDefault(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	result, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23}))
	if err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 2 {
		t.Fatalf("invocations = %v, want 2 (permanent + runtime)", calls)
	}
	if !strings.Contains(calls[0], "--remove-port=23/tcp") {
		t.Fatalf("first call = %q", calls[0])
	}
	fqcn, params, ok := inverseOf(h.rc)
	if !ok || fqcn != "fw.firewalld.allow" || params["port"] != 23 {
		t.Fatalf("inverse = %q, %v, ok=%v", fqcn, params, ok)
	}
}

func TestDeny_AlreadyDeniedIsNoOp(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	result, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23}))
	if err != nil {
		t.Fatalf("Deny: %v", err)
	}
	if result.Changed {
		t.Fatal("expected Changed = false")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Fatalf("expected no mutating invocations, got %v", calls)
	}
}

func TestDeny_MutationFailure(t *testing.T) {
	fixture := allowedEverywhere
	fixture.mutateExit = 1
	h := newHarness(t, fixture)
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23})); err == nil {
		t.Fatal("expected the mutation failure to surface")
	}
}

func TestDeny_ConnectionDiesQueryingPermanent(t *testing.T) {
	h := newHarnessBudgeted(t, allowedEverywhere, 0)
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23})); err == nil {
		t.Fatal("expected a connection failure querying the permanent configuration")
	}
}

func TestDeny_ConnectionDiesInAfterQuery(t *testing.T) {
	h := newHarnessBudgeted(t, allowedEverywhere, 4)
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23})); err == nil {
		t.Fatal("expected a connection failure re-querying state after the change")
	}
}

func TestDeny_RecordStatFails(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	h.rc.failOnKey = "zone"
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23})); err == nil {
		t.Fatal("expected the injected SetStat failure to surface")
	}
}

func TestDeny_RecordInverseFails(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	h.rc.failOnKey = sdk.StatInverse
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23})); err == nil {
		t.Fatal("expected the injected inverse-recording failure to surface")
	}
}

func TestDeny_PermanentNotABool(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23, "permanent": "yes"})); err == nil {
		t.Fatal("expected an error for a non-bool permanent")
	}
}

func TestDeny_ImmediateNotABool(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(map[string]any{"port": 23, "immediate": "yes"})); err == nil {
		t.Fatal("expected an error for a non-bool immediate")
	}
}

func TestDeny_NeitherPortNorServiceRefuses(t *testing.T) {
	h := newHarness(t, allowedEverywhere)
	if _, err := fwmod.Deny(context.Background(), h.rc, h.device, h.params(nil)); err == nil {
		t.Fatal("expected a refusal for neither port nor service given")
	}
}

// ---------- Reload ----------

func TestReload_NoSSHAccessor(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := fwmod.Reload(context.Background(), rc, noSSHDevice(), map[string]any{})
	if err == nil {
		t.Fatal("expected an error connecting to a device with no SSH accessor")
	}
}

func TestReload_AlwaysReportsChanged(t *testing.T) {
	h := newHarness(t, deniedEverywhere)
	result, err := fwmod.Reload(context.Background(), h.rc, h.device, h.params(nil))
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if !result.Changed {
		t.Fatal("expected Changed = true")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "--reload") {
		t.Fatalf("invocations = %v", calls)
	}
}

func TestReload_Failure(t *testing.T) {
	fixture := deniedEverywhere
	fixture.reloadExit = 1
	h := newHarness(t, fixture)
	if _, err := fwmod.Reload(context.Background(), h.rc, h.device, h.params(nil)); err == nil {
		t.Fatal("expected the reload failure to surface")
	}
}

func TestReload_ConnectionDies(t *testing.T) {
	h := newHarnessBudgeted(t, deniedEverywhere, 0)
	if _, err := fwmod.Reload(context.Background(), h.rc, h.device, h.params(nil)); err == nil {
		t.Fatal("expected a connection failure running reload")
	}
}
