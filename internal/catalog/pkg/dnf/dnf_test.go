package dnf_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with dnf and rpm on PATH as shell scripts. The SSH transport,
// the shell, the quoting, the argument vector and the exit status are
// all genuine; only the package database at the far end is not. Proving
// these against a real dnf is a container Release Gate's job;
// svc.systemd.* ships at this same tier with no such gate, and this
// namespace matches it.

// ---------- harness ----------

type ctxStub struct {
	secrets map[string]string
	stats   map[string]any

	// failOnKey, when set, makes SetStat fail for exactly that key and
	// succeed for every other one. recordState and sdk.RecordInverse each
	// write under a specific key ("name", "version", sdk.StatDiff's
	// "diff", sdk.StatInverse's "inverse"), so this reaches each of their
	// own error-return branches individually rather than failing every
	// SetStat call and only ever reaching the first one.
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

// noSSHDevice satisfies capability.PackageManagerCapable's structural
// needs for these tests (via Caps) but implements no SSH accessor at
// all, so sdk.Connect refuses it before anything is dialed. It is what
// reaches every method's own "conn, err := sdk.Connect(...)" failure
// branch, the same way exec.command's own tests reach it.
func noSSHDevice() inventory.InventoryItem {
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NameDnf}}
}

// pkgState is what the fakes report for rpm -q and dnf check-update.
type pkgState struct {
	installed       bool
	version         string
	updateExit      int  // dnf check-update's own exit code: 0 none, 100 available, anything else a real error
	dnfExit         int  // exit code dnf itself returns for a mutating call, default 0
	rpmEmptyVersion bool // rpm -q exits 0 (it knows the package) but prints no version at all
}

var (
	absent           = pkgState{}
	installedCurrent = pkgState{installed: true, version: "1.0-1", updateExit: 0}
	installedOld     = pkgState{installed: true, version: "1.0-1", updateExit: 100}
	// installedNoVersionReported is a real rpm database edge case: the
	// query succeeds, but the format string it was asked for comes back
	// empty (a corrupt or partial database entry). Distinct from rpm -q
	// exiting non-zero, which is the ordinary "not installed" case.
	installedNoVersionReported = pkgState{rpmEmptyVersion: true}
)

// harness wires a real SSH server, fake dnf/rpm and a device, and
// returns everything a method call needs plus a way to read back which
// dnf commands were actually sent.
type harness struct {
	rc     *ctxStub
	device inventory.InventoryItem
	record string
}

func newHarness(t *testing.T, state pkgState) *harness {
	t.Helper()
	return newHarnessBudgeted(t, state, -1)
}

// newHarnessBudgeted is newHarness with a cap on how many SSH session
// channels the server accepts before refusing every further one, which
// is how a test reaches the branches that only a connection failing
// partway through a task can produce (a real protocol-level refusal,
// the same shape a device dropping the connection mid-task would
// produce, not an injected Go error). A negative budget means
// unlimited. Each of queryRPM, hasUpdate and runDnf opens exactly one
// session per call, in the order the method under test calls them, so
// the budget names which call in that sequence is the first to fail.
func newHarnessBudgeted(t *testing.T, state pkgState, budget int) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	rpmExit := "1"
	version := state.version
	if state.installed {
		rpmExit = "0"
	}
	if state.rpmEmptyVersion {
		rpmExit = "0"
		version = ""
	}

	dnfScript := `#!/bin/sh
if [ "$1" = "check-update" ]; then
  exit "$FAKE_UPDATE_EXIT"
fi
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit_code="${FAKE_DNF_EXIT:-0}"
if [ "$exit_code" != "0" ]; then
  printf 'dnf: fake failure acting on %s\n' "$*" >&2
fi
exit "$exit_code"
`
	rpmScript := `#!/bin/sh
printf '%s' "$FAKE_VERSION"
exit "$FAKE_RPM_EXIT"
`
	writeScript(t, dir, "dnf", dnfScript)
	writeScript(t, dir, "rpm", rpmScript)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_VERSION", version)
	t.Setenv("FAKE_RPM_EXIT", rpmExit)
	t.Setenv("FAKE_UPDATE_EXIT", strconv.Itoa(state.updateExit))
	t.Setenv("FAKE_DNF_EXIT", strconv.Itoa(state.dnfExit))

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
			Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameDnf}},
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

// invocations returns the dnf argv of every mutating call actually sent
// (check-update is excluded, being a query rather than an action), one
// entry per call joined by spaces.
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
		fqcn string
		cap  capability.Name
	}{
		{"pkg.dnf.install", capability.NameDnf},
		{"pkg.dnf.remove", capability.NameDnf},
		{"pkg.dnf.upgrade", capability.NameDnf},
	}
	for _, tc := range tests {
		t.Run(tc.fqcn, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok {
				t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", tc.fqcn)
			}
			if d.Manifest.Status != collection.StatusImplemented {
				t.Errorf("Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
			}
			if d.Invoke == nil {
				t.Error("Invoke is nil")
			}
			if len(d.Manifest.RequiredCapabilities) != 1 || d.Manifest.RequiredCapabilities[0] != tc.cap {
				t.Errorf("RequiredCapabilities = %v, want exactly [%s]", d.Manifest.RequiredCapabilities, tc.cap)
			}
		})
	}
}

func TestUpgrade_RegisteredIrreversible(t *testing.T) {
	d, _ := collection.Lookup("pkg.dnf.upgrade")
	if d.Manifest.Reversibility.Reversible {
		t.Error("pkg.dnf.upgrade claims reversible")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversible: false with no Notes")
	}
}

// ---------- bad parameters, refused before connecting ----------

func TestInstall_RequiresName(t *testing.T) {
	_, err := dnf.Install(context.Background(), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("err = %v, want it to mention the missing name param", err)
	}
}

func TestRemove_RequiresName(t *testing.T) {
	_, err := dnf.Remove(context.Background(), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("err = %v, want it to mention the missing name param", err)
	}
}

func TestUpgrade_RequiresName(t *testing.T) {
	_, err := dnf.Upgrade(context.Background(), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("err = %v, want it to mention the missing name param", err)
	}
}

// ---------- install ----------

func TestInstall_AbsentPackageIsInstalledAndInverseRecorded(t *testing.T) {
	h := newHarness(t, absent)
	result, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Error("expected an absent package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "install") {
		t.Errorf("dnf calls = %v, want exactly one install", calls)
	}
	inv, ok := h.rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatal("expected an inverse to be recorded for a genuine install")
	}
	params, _ := inv["params"].(map[string]any)
	if inv["fqcn"] != "pkg.dnf.remove" || params["name"] != "curl" {
		t.Errorf("inverse = %+v, want pkg.dnf.remove naming curl", inv)
	}
}

func TestInstall_AlreadyPresentConverges(t *testing.T) {
	h := newHarness(t, installedCurrent)
	result, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Changed {
		t.Error("expected an already-present package to report no change")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("dnf calls = %v, want none for a converged install", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("a converged run must not record an inverse")
	}
}

func TestInstall_VersionMismatchReinstallsWithNoInverse(t *testing.T) {
	h := newHarness(t, installedCurrent) // installed at 1.0-1
	result, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", map[string]any{"version": "2.0-1"}))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Error("expected a version mismatch to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "curl-2.0-1") {
		t.Errorf("dnf calls = %v, want one install pinning curl-2.0-1", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("a version change on an already-present package must not record an inverse: " +
			"the version that was there before this run is gone the moment dnf replaces it")
	}
}

func TestInstall_NonZeroExitIsAnError(t *testing.T) {
	state := absent
	state.dnfExit = 1
	h := newHarness(t, state)
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("err = %v, want it to mention the non-zero exit", err)
	}
}

// ---------- remove ----------

func TestRemove_PresentPackageIsRemovedAndInverseCapturesVersion(t *testing.T) {
	h := newHarness(t, installedCurrent) // installed at 1.0-1
	result, err := dnf.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Error("expected a present package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "remove") {
		t.Errorf("dnf calls = %v, want exactly one remove", calls)
	}
	inv, ok := h.rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatal("expected an inverse to be recorded for a genuine removal")
	}
	params, _ := inv["params"].(map[string]any)
	if inv["fqcn"] != "pkg.dnf.install" || params["name"] != "curl" || params["version"] != "1.0-1" {
		t.Errorf("inverse = %+v, want pkg.dnf.install pinning curl to version 1.0-1", inv)
	}
}

func TestRemove_AbsentPackageConverges(t *testing.T) {
	h := newHarness(t, absent)
	result, err := dnf.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.Changed {
		t.Error("expected an already-absent package to report no change")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("dnf calls = %v, want none for a converged remove", calls)
	}
}

func TestRemove_NonZeroExitIsAnError(t *testing.T) {
	state := installedCurrent
	state.dnfExit = 1
	h := newHarness(t, state)
	_, err := dnf.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("err = %v, want it to mention the non-zero exit", err)
	}
}

// ---------- upgrade ----------

func TestUpgrade_AbsentPackageIsInstalledFresh(t *testing.T) {
	h := newHarness(t, absent)
	result, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !result.Changed {
		t.Error("expected an absent package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "install") {
		t.Errorf("dnf calls = %v, want exactly one plain install for an absent package", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("pkg.dnf.upgrade must never record an inverse, even on its install-fresh path")
	}
}

func TestUpgrade_AlreadyCurrentConverges(t *testing.T) {
	h := newHarness(t, installedCurrent) // check-update reports none available
	result, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if result.Changed {
		t.Error("expected an already-current package to report no change")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("dnf calls = %v, want none for a converged upgrade", calls)
	}
}

func TestUpgrade_UpdateAvailableUpgrades(t *testing.T) {
	h := newHarness(t, installedOld) // check-update reports 100: available
	result, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !result.Changed {
		t.Error("expected an outdated package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "upgrade") {
		t.Errorf("dnf calls = %v, want one upgrade", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("pkg.dnf.upgrade must never record an inverse")
	}
}

func TestUpgrade_CheckUpdateRealErrorSurfaces(t *testing.T) {
	state := installedCurrent
	state.updateExit = 1 // neither 0 (none) nor 100 (available): a real dnf failure
	h := newHarness(t, state)
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "check-update") {
		t.Errorf("err = %v, want it to mention check-update's own failure", err)
	}
}

// ---------- rpm's own edge cases ----------

func TestInstall_EmptyVersionReportedIsTreatedAsAbsent(t *testing.T) {
	h := newHarness(t, installedNoVersionReported)
	result, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Error("expected a query that succeeded but reported no version to be treated as absent and installed")
	}
	if calls := h.invocations(t); len(calls) != 1 || !strings.HasPrefix(calls[0], "install") {
		t.Errorf("dnf calls = %v, want exactly one install", calls)
	}
}

// ---------- failureDetail's two message sources ----------

func TestInstall_NonZeroExitPrefersStderr(t *testing.T) {
	// The default fake dnf (via newHarness) writes its failure message
	// to stderr, so TestInstall_NonZeroExitIsAnError already covers this
	// in practice; this test asserts it directly rather than leaving it
	// implicit.
	state := absent
	state.dnfExit = 1
	h := newHarness(t, state)
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Errorf("err = %v, want it to carry dnf's stderr message", err)
	}
}

func TestInstall_NonZeroExitFallsBackToStdout(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	// This dnf writes its explanation to stdout only. failureDetail must
	// fall back to it when stderr is empty.
	dnfScript := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
printf 'stdout-only failure explanation\n'
exit 1
`
	rpmScript := `#!/bin/sh
exit 1
`
	writeScript(t, dir, "dnf", dnfScript)
	writeScript(t, dir, "rpm", rpmScript)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	rc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
	dev := &target{Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameDnf}}, host: srv.Host, port: srv.Port}

	_, err = dnf.Install(context.Background(), rc, dev, map[string]any{"name": "curl", "insecure_skip_host_key_verify": true})
	if err == nil || !strings.Contains(err.Error(), "stdout-only failure explanation") {
		t.Errorf("err = %v, want it to carry dnf's stdout message", err)
	}
}

func TestInstall_NonZeroExitWithNoOutputAtAllSaysSo(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	// This dnf fails silently on both streams, which does happen (a
	// scriptlet killed by a signal, for one). failureDetail must say
	// plainly that nothing was said, rather than an empty string a
	// reader would mistake for a missing error message.
	dnfScript := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit 1
`
	rpmScript := `#!/bin/sh
exit 1
`
	writeScript(t, dir, "dnf", dnfScript)
	writeScript(t, dir, "rpm", rpmScript)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	rc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
	dev := &target{Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameDnf}}, host: srv.Host, port: srv.Port}

	_, err = dnf.Install(context.Background(), rc, dev, map[string]any{"name": "curl", "insecure_skip_host_key_verify": true})
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Errorf("err = %v, want it to say plainly that dnf said nothing", err)
	}
}

// ---------- connection failures ----------

func TestInstall_UnreachableDeviceConnectFailure(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := dnf.Install(context.Background(), rc, noSSHDevice(), map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("err = %v, want it to mention the device not being reachable over SSH", err)
	}
}

func TestRemove_UnreachableDeviceConnectFailure(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := dnf.Remove(context.Background(), rc, noSSHDevice(), map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("err = %v, want it to mention the device not being reachable over SSH", err)
	}
}

func TestUpgrade_UnreachableDeviceConnectFailure(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := dnf.Upgrade(context.Background(), rc, noSSHDevice(), map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("err = %v, want it to mention the device not being reachable over SSH", err)
	}
}

// ---------- a connection dying partway through, per call site ----------
//
// Each case names how many SSH sessions succeed before the server
// starts refusing them, which is how it targets one specific call site
// in the sequence a method makes: queryRPM (the initial read),
// hasUpdate (upgrade's own extra read), runDnf (the write) and the
// re-query that follows a write. budget is the count that succeeds; the
// next one is always the one that fails.

func TestInstall_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 0)
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestInstall_ApplyConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 1) // the initial query succeeds, the install apply does not
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestInstall_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 2) // the query and the install succeed, the re-query does not
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestRemove_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 0)
	_, err := dnf.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestRemove_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 2) // the query and the remove succeed, the re-query does not
	_, err := dnf.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 0)
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_CheckUpdateConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 1) // the initial query succeeds, check-update does not
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_FreshInstallApplyConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 1) // the initial query succeeds, the fresh install does not
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_FreshInstallRequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 2) // the query and the fresh install succeed, the re-query does not
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_ApplyConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedOld, 2) // the query and check-update succeed, the upgrade apply does not
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedOld, 3) // query, check-update and upgrade succeed, the re-query does not
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

// ---------- recording failures ----------

func TestInstall_RecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = "version"
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface")
	}
}

func TestInstall_RecordInverseFailureSurfaces(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = sdk.StatInverse
	_, err := dnf.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected RecordInverse's own SetStat failure to surface")
	}
}

func TestRemove_RecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, installedCurrent)
	h.rc.failOnKey = "name"
	_, err := dnf.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface")
	}
}

func TestRemove_RecordInverseFailureSurfaces(t *testing.T) {
	h := newHarness(t, installedCurrent)
	h.rc.failOnKey = sdk.StatInverse
	_, err := dnf.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected RecordInverse's own SetStat failure to surface")
	}
}

func TestUpgrade_FreshInstallRecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = "name"
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface on the fresh-install path")
	}
}

func TestUpgrade_RecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, installedCurrent)
	h.rc.failOnKey = "name"
	_, err := dnf.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface on the already-current path")
	}
}
