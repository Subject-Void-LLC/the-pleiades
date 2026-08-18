package apt_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// These run against a real in-process SSH server executing a real
// /bin/sh, with apt-get, dpkg-query and apt-cache on PATH as shell
// scripts. The SSH transport, the shell, the quoting, the argument
// vector and the exit status are all genuine; only the package database
// at the far end is not. Proving these against a real apt-get is a
// container Release Gate's job; svc.systemd.* ships at this same tier
// with no such gate, and this namespace matches it.

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
	return &inventorytest.Stub{StubName: "no-ssh", Caps: []capability.Name{capability.NameApt}}
}

// pkgState is what the fakes report for dpkg-query and apt-cache.
type pkgState struct {
	installed          bool
	version            string
	candidate          string // apt-cache policy's Candidate:; "" means "(none)"
	aptExit            int    // exit code apt-get itself returns, default 0
	candidateExit      int    // exit code apt-cache itself returns, default 0
	noCandidateLine    bool   // apt-cache's output has no Candidate: line at all
	dpkgStatusOverride string // when set, used verbatim as dpkg-query's Status line, with a successful exit
}

var (
	absent                 = pkgState{}
	installedCurrent       = pkgState{installed: true, version: "1.0", candidate: "1.0"}
	installedOld           = pkgState{installed: true, version: "1.0", candidate: "1.1"}
	installedUnknownToRepo = pkgState{installed: true, version: "1.0", candidate: ""}
	// configFilesRemnant is dpkg's own record of a package apt removed
	// but did not purge: config files are still on disk, but the package
	// itself is absent, distinct from a name dpkg has never heard of at
	// all (which reports via a non-zero exit instead, not this status
	// line). Both must be read as "not installed."
	configFilesRemnant = pkgState{dpkgStatusOverride: "deinstall ok config-files", version: "0.9"}
)

// harness wires a real SSH server, fake apt-get/dpkg-query/apt-cache and
// a device, and returns everything a method call needs plus a way to
// read back which apt-get commands were actually sent.
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
// unlimited. Each of queryDpkg, queryCandidate and runAptGet opens
// exactly one session per call, in the order the method under test
// calls them, so the budget names which call in that sequence is the
// first to fail.
func newHarnessBudgeted(t *testing.T, state pkgState, budget int) *harness {
	t.Helper()

	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")

	status := "unknown ok not-installed"
	dpkgExit := "1"
	if state.installed {
		status = "install ok installed"
		dpkgExit = "0"
	}
	if state.dpkgStatusOverride != "" {
		// dpkg-query succeeds (it knows the package's history); the
		// Status line itself is what says the package is not currently
		// installed.
		status = state.dpkgStatusOverride
		dpkgExit = "0"
	}
	candidate := state.candidate
	if candidate == "" {
		candidate = "(none)"
	}

	aptGetScript := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit_code="${FAKE_APT_EXIT:-0}"
if [ "$exit_code" != "0" ]; then
  printf 'apt-get: E: fake failure acting on %s\n' "$*" >&2
fi
exit "$exit_code"
`
	dpkgQueryScript := `#!/bin/sh
printf '%s\t%s' "$FAKE_STATUS" "$FAKE_VERSION"
exit "$FAKE_DPKG_EXIT"
`
	// Package: precedes Candidate: unconditionally, the way apt-cache
	// policy's real output always carries more than one line, so
	// queryCandidate's own loop has a non-Candidate line to skip over on
	// every call, not just a contrived one.
	aptCacheScript := `#!/bin/sh
printf 'Package: %s\n' "$2"
if [ "$FAKE_NO_CANDIDATE_LINE" != "1" ]; then
  printf 'Candidate: %s\n' "$FAKE_CANDIDATE"
fi
exit "${FAKE_CANDIDATE_EXIT:-0}"
`
	writeScript(t, dir, "apt-get", aptGetScript)
	writeScript(t, dir, "dpkg-query", dpkgQueryScript)
	writeScript(t, dir, "apt-cache", aptCacheScript)

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)
	t.Setenv("FAKE_STATUS", status)
	t.Setenv("FAKE_VERSION", state.version)
	t.Setenv("FAKE_DPKG_EXIT", dpkgExit)
	t.Setenv("FAKE_CANDIDATE", candidate)
	t.Setenv("FAKE_APT_EXIT", strconv.Itoa(state.aptExit))
	t.Setenv("FAKE_CANDIDATE_EXIT", strconv.Itoa(state.candidateExit))
	if state.noCandidateLine {
		t.Setenv("FAKE_NO_CANDIDATE_LINE", "1")
	} else {
		t.Setenv("FAKE_NO_CANDIDATE_LINE", "0")
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
			Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameApt}},
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

// invocations returns the apt-get argv of every call actually sent, one
// entry per call joined by spaces, which is what proves a converged run
// sent nothing and an acting run sent exactly the command expected.
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
		{"pkg.apt.install", capability.NameApt},
		{"pkg.apt.remove", capability.NameApt},
		{"pkg.apt.upgrade", capability.NameApt},
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
	d, _ := collection.Lookup("pkg.apt.upgrade")
	if d.Manifest.Reversibility.Reversible {
		t.Error("pkg.apt.upgrade claims reversible")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversible: false with no Notes")
	}
}

// ---------- bad parameters, refused before connecting ----------

func TestInstall_RequiresName(t *testing.T) {
	_, err := apt.Install(context.Background(), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("err = %v, want it to mention the missing name param", err)
	}
}

func TestRemove_RequiresName(t *testing.T) {
	_, err := apt.Remove(context.Background(), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("err = %v, want it to mention the missing name param", err)
	}
}

func TestUpgrade_RequiresName(t *testing.T) {
	_, err := apt.Upgrade(context.Background(), nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Errorf("err = %v, want it to mention the missing name param", err)
	}
}

// ---------- install ----------

func TestInstall_AbsentPackageIsInstalledAndInverseRecorded(t *testing.T) {
	h := newHarness(t, absent)
	result, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Error("expected an absent package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "install") {
		t.Errorf("apt-get calls = %v, want exactly one install", calls)
	}
	inv, ok := h.rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatal("expected an inverse to be recorded for a genuine install")
	}
	params, _ := inv["params"].(map[string]any)
	if inv["fqcn"] != "pkg.apt.remove" || params["name"] != "curl" {
		t.Errorf("inverse = %+v, want pkg.apt.remove naming curl", inv)
	}
}

func TestInstall_AlreadyPresentConverges(t *testing.T) {
	h := newHarness(t, installedCurrent)
	result, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if result.Changed {
		t.Error("expected an already-present package to report no change")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("apt-get calls = %v, want none for a converged install", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("a converged run must not record an inverse")
	}
}

func TestInstall_VersionMismatchReinstallsWithNoInverse(t *testing.T) {
	h := newHarness(t, installedCurrent) // installed at 1.0
	result, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", map[string]any{"version": "2.0"}))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Error("expected a version mismatch to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "curl=2.0") {
		t.Errorf("apt-get calls = %v, want one install pinning curl=2.0", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("a version change on an already-present package must not record an inverse: " +
			"the version that was there before this run is gone the moment apt-get replaces it")
	}
}

func TestInstall_NonZeroExitIsAnError(t *testing.T) {
	state := absent
	state.aptExit = 100
	h := newHarness(t, state)
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "exited 100") {
		t.Errorf("err = %v, want it to mention the non-zero exit", err)
	}
}

// ---------- remove ----------

func TestRemove_PresentPackageIsRemovedAndInverseCapturesVersion(t *testing.T) {
	h := newHarness(t, installedCurrent) // installed at version 1.0
	result, err := apt.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Changed {
		t.Error("expected a present package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "remove") {
		t.Errorf("apt-get calls = %v, want exactly one remove", calls)
	}
	inv, ok := h.rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatal("expected an inverse to be recorded for a genuine removal")
	}
	params, _ := inv["params"].(map[string]any)
	if inv["fqcn"] != "pkg.apt.install" || params["name"] != "curl" || params["version"] != "1.0" {
		t.Errorf("inverse = %+v, want pkg.apt.install pinning curl to version 1.0", inv)
	}
}

func TestRemove_AbsentPackageConverges(t *testing.T) {
	h := newHarness(t, absent)
	result, err := apt.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if result.Changed {
		t.Error("expected an already-absent package to report no change")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("apt-get calls = %v, want none for a converged remove", calls)
	}
}

func TestRemove_NonZeroExitIsAnError(t *testing.T) {
	state := installedCurrent
	state.aptExit = 1
	h := newHarness(t, state)
	_, err := apt.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("err = %v, want it to mention the non-zero exit", err)
	}
}

// ---------- upgrade ----------

func TestUpgrade_AbsentPackageIsInstalledFresh(t *testing.T) {
	h := newHarness(t, absent)
	result, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !result.Changed {
		t.Error("expected an absent package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "install") || strings.Contains(calls[0], "apt-cache") {
		t.Errorf("apt-get calls = %v, want exactly one plain install, no candidate check for an absent package", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("pkg.apt.upgrade must never record an inverse, even on its install-fresh path")
	}
}

func TestUpgrade_AlreadyCurrentConverges(t *testing.T) {
	h := newHarness(t, installedCurrent) // version == candidate
	result, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if result.Changed {
		t.Error("expected an already-current package to report no change")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("apt-get calls = %v, want none for a converged upgrade", calls)
	}
}

func TestUpgrade_OlderThanCandidateUpgrades(t *testing.T) {
	h := newHarness(t, installedOld) // version 1.0, candidate 1.1
	result, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if !result.Changed {
		t.Error("expected an outdated package to report changed")
	}
	calls := h.invocations(t)
	if len(calls) != 1 || !strings.Contains(calls[0], "--only-upgrade") {
		t.Errorf("apt-get calls = %v, want one --only-upgrade install", calls)
	}
	if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
		t.Error("pkg.apt.upgrade must never record an inverse")
	}
}

// ---------- dpkg's own edge cases ----------

func TestInstall_ConfigFilesRemnantIsTreatedAsAbsent(t *testing.T) {
	h := newHarness(t, configFilesRemnant)
	result, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !result.Changed {
		t.Error("expected a config-files remnant (removed but not purged) to be treated as absent and installed")
	}
	if calls := h.invocations(t); len(calls) != 1 || !strings.HasPrefix(calls[0], "install") {
		t.Errorf("apt-get calls = %v, want exactly one install", calls)
	}
}

// ---------- apt-cache policy's own edge cases ----------

func TestUpgrade_CandidateUnknownToRepoConverges(t *testing.T) {
	h := newHarness(t, installedUnknownToRepo) // dpkg knows it, apt-cache reports "(none)"
	result, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err != nil {
		t.Fatalf("Upgrade: %v", err)
	}
	if result.Changed {
		t.Error("expected a package apt-cache has no candidate for to report no change rather than guess")
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("apt-get calls = %v, want none", calls)
	}
}

func TestUpgrade_CandidateQueryNonZeroExitSurfaces(t *testing.T) {
	state := installedCurrent
	state.candidateExit = 1
	h := newHarness(t, state)
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "apt-cache policy") || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("err = %v, want it to name apt-cache policy's own non-zero exit", err)
	}
}

func TestUpgrade_CandidateQueryMissingLineSurfaces(t *testing.T) {
	state := installedCurrent
	state.noCandidateLine = true
	h := newHarness(t, state)
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "no Candidate line") {
		t.Errorf("err = %v, want it to say apt-cache's output had no Candidate line", err)
	}
}

// ---------- failureDetail's two message sources ----------

func TestInstall_NonZeroExitPrefersStderr(t *testing.T) {
	// The default fake apt-get (via newHarness) writes its failure
	// message to stderr, so TestInstall_NonZeroExitIsAnError already
	// covers this in practice; this test asserts it directly rather
	// than leaving it implicit.
	state := absent
	state.aptExit = 100
	h := newHarness(t, state)
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Errorf("err = %v, want it to carry apt-get's stderr message", err)
	}
}

func TestInstall_NonZeroExitFallsBackToStdout(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	// This apt-get writes its explanation to stdout only, the way some
	// real packaging tools do for a subset of their own failures.
	// failureDetail must fall back to it when stderr is empty.
	aptGetScript := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
printf 'stdout-only failure explanation\n'
exit 1
`
	dpkgQueryScript := `#!/bin/sh
printf 'unknown ok not-installed\t'
exit 1
`
	writeScript(t, dir, "apt-get", aptGetScript)
	writeScript(t, dir, "dpkg-query", dpkgQueryScript)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	rc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
	dev := &target{Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameApt}}, host: srv.Host, port: srv.Port}

	_, err = apt.Install(context.Background(), rc, dev, map[string]any{"name": "curl", "insecure_skip_host_key_verify": true})
	if err == nil || !strings.Contains(err.Error(), "stdout-only failure explanation") {
		t.Errorf("err = %v, want it to carry apt-get's stdout message", err)
	}
}

func TestInstall_NonZeroExitWithNoOutputAtAllSaysSo(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "invocations")
	// This apt-get fails silently on both streams, which does happen
	// (a maintainer script killed by a signal, for one). failureDetail
	// must say plainly that nothing was said, rather than an empty
	// string a reader would mistake for a missing error message.
	aptGetScript := `#!/bin/sh
for arg in "$@"; do printf '%s\n' "$arg" >> "$FAKE_RECORD"; done
printf -- '---\n' >> "$FAKE_RECORD"
exit 1
`
	dpkgQueryScript := `#!/bin/sh
printf 'unknown ok not-installed\t'
exit 1
`
	writeScript(t, dir, "apt-get", aptGetScript)
	writeScript(t, dir, "dpkg-query", dpkgQueryScript)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_RECORD", record)

	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	rc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
	dev := &target{Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameApt}}, host: srv.Host, port: srv.Port}

	_, err = apt.Install(context.Background(), rc, dev, map[string]any{"name": "curl", "insecure_skip_host_key_verify": true})
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Errorf("err = %v, want it to say plainly that apt-get said nothing", err)
	}
}

// ---------- connection failures ----------

func TestInstall_UnreachableDeviceConnectFailure(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := apt.Install(context.Background(), rc, noSSHDevice(), map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("err = %v, want it to mention the device not being reachable over SSH", err)
	}
}

func TestRemove_UnreachableDeviceConnectFailure(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := apt.Remove(context.Background(), rc, noSSHDevice(), map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("err = %v, want it to mention the device not being reachable over SSH", err)
	}
}

func TestUpgrade_UnreachableDeviceConnectFailure(t *testing.T) {
	rc := &ctxStub{stats: map[string]any{}}
	_, err := apt.Upgrade(context.Background(), rc, noSSHDevice(), map[string]any{"name": "curl"})
	if err == nil || !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("err = %v, want it to mention the device not being reachable over SSH", err)
	}
}

// ---------- a connection dying partway through, per call site ----------
//
// Each case names how many SSH sessions succeed before the server
// starts refusing them, which is how it targets one specific call site
// in the sequence a method makes: queryDpkg (the initial read),
// queryCandidate (upgrade's own extra read), runAptGet (the write) and
// the re-query that follows a write. budget is the count that succeeds;
// the next one is always the one that fails.

func TestInstall_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 0)
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestInstall_ApplyConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 1) // the initial query succeeds, the install apply does not
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestInstall_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 2) // the query and the install succeed, the re-query does not
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestRemove_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 0)
	_, err := apt.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestRemove_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 2) // the query and the remove succeed, the re-query does not
	_, err := apt.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_InitialQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 0)
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_CandidateQueryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedCurrent, 1) // the initial query succeeds, the candidate check does not
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_FreshInstallApplyConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 1) // the initial query succeeds, the fresh install does not
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_FreshInstallRequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, absent, 2) // the query and the fresh install succeed, the re-query does not
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_ApplyConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedOld, 2) // the query and the candidate check succeed, the upgrade apply does not
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

func TestUpgrade_RequeryConnectionFailure(t *testing.T) {
	h := newHarnessBudgeted(t, installedOld, 3) // query, candidate check and upgrade succeed, the re-query does not
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil || !strings.Contains(err.Error(), "open session") {
		t.Errorf("err = %v, want it to mention the session failing to open", err)
	}
}

// ---------- recording failures ----------

func TestInstall_RecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = "version"
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface")
	}
}

func TestInstall_RecordInverseFailureSurfaces(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = sdk.StatInverse
	_, err := apt.Install(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected RecordInverse's own SetStat failure to surface")
	}
}

func TestRemove_RecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, installedCurrent)
	h.rc.failOnKey = "name"
	_, err := apt.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface")
	}
}

func TestRemove_RecordInverseFailureSurfaces(t *testing.T) {
	h := newHarness(t, installedCurrent)
	h.rc.failOnKey = sdk.StatInverse
	_, err := apt.Remove(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected RecordInverse's own SetStat failure to surface")
	}
}

func TestUpgrade_FreshInstallRecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, absent)
	h.rc.failOnKey = "name"
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface on the fresh-install path")
	}
}

func TestUpgrade_RecordStateFailureSurfaces(t *testing.T) {
	h := newHarness(t, installedCurrent)
	h.rc.failOnKey = "name"
	_, err := apt.Upgrade(context.Background(), h.rc, h.device, h.params("curl", nil))
	if err == nil {
		t.Fatal("expected recordState's own SetStat failure to surface on the already-current path")
	}
}
