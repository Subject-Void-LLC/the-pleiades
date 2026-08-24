// This file tests facts.gather against a REAL SSH server, in this
// process, that hands every command it receives to a real /bin/sh on this
// machine (pkg/remoteexec/remoteexectest). Nothing about the transport,
// the shell, uname or cat is stubbed.
//
// RULE 0 is why. This method's entire subject is what a real device
// answers to seven real commands and how that answer is read, so a server
// returning canned bytes would only prove the canned bytes were canned.
// Every expected value below is computed by reading the same source the
// method reads, with code that shares nothing with it, rather than by
// believing what the method reported about itself.
//
// Every identifier here carries the gather prefix, because a sibling
// method landing in this namespace later would share this test package
// and Go has no file-level scope.
package facts_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// errGatherEmit is what a stub context returns when a test wants
// recording to fail, so the branch where a probe answered and the record
// did not is reachable.
var errGatherEmit = errors.New("recording the fact failed")

// gatherServer is where the in-process SSH harness is listening, plus the
// one credential it accepts.
type gatherServer struct {
	host     string
	port     int
	username string
	password string
}

// startGatherServer starts a real SSH server that runs every command
// through a real /bin/sh, and stops it when the test ends.
func startGatherServer(t *testing.T) gatherServer {
	return startGatherServerWithSessionBudget(t, -1)
}

// startGatherServerWithSessionBudget is startGatherServer with a cap on
// how many session channels it accepts before refusing.
//
// A budget of zero reaches the branch where the connection authenticates
// and then cannot carry the first probe. A budget of one is what proves a
// narrowed filter really skips the commands it excluded, since the eighth
// session would be refused and a run that opened only one never asks for
// it.
func startGatherServerWithSessionBudget(t *testing.T, budget int) gatherServer {
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

	return gatherServer{host: srv.Host, port: srv.Port, username: srv.Username, password: srv.Password}
}

// gatherTarget is a device reachable over SSH that also answers
// capability.FactGathererCapable, which is the shape this method's
// manifest requires.
type gatherTarget struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *gatherTarget) SSHHost() string        { return d.host }
func (d *gatherTarget) SSHPort() int           { return d.port }
func (d *gatherTarget) FactSourceName() string { return "setup" }

// newGatherStub builds the base inventory item both device shapes wrap.
func newGatherStub() *inventorytest.Stub {
	return &inventorytest.Stub{
		StubID:    "facts-1",
		StubName:  "facts-target",
		Caps:      []capability.Name{capability.NameSSHTransport, capability.NameFactGatherer},
		StubState: inventory.StateActive,
	}
}

// newGatherTarget builds the SSH-reachable device a test runs against.
func newGatherTarget(server gatherServer) *gatherTarget {
	return &gatherTarget{Stub: newGatherStub(), host: server.host, port: server.port}
}

// newGatherUnreachable builds a device this method cannot reach at all:
// the bare stub, which implements InventoryItem and nothing else.
func newGatherUnreachable() inventory.InventoryItem {
	return newGatherStub()
}

// gatherContext is a minimal sdk.RunbookContext that keeps facts and
// stats in SEPARATE maps.
//
// That separation is the point rather than tidiness. This method must
// emit everything it learns as a fact, because a fact is kept as drift
// data and a stat expires with the run, and a context that folded the two
// together could not tell a method honoring that from one ignoring it.
type gatherContext struct {
	secrets map[string]string
	facts   map[string]any
	stats   map[string]any

	// failOn, when set, makes EmitFact fail for that one key, so the
	// branch where a probe answered and recording did not is reachable.
	failOn string
}

func newGatherContext(server gatherServer) *gatherContext {
	return &gatherContext{
		secrets: map[string]string{"username": server.username, "password": server.password},
		facts:   map[string]any{},
		stats:   map[string]any{},
	}
}

func (c *gatherContext) InjectSecrets() map[string]string { return c.secrets }

func (c *gatherContext) SetStat(key string, value any) error {
	c.stats[key] = value
	return nil
}

func (c *gatherContext) EmitFact(key string, value any) error {
	if c.failOn != "" && c.failOn == key {
		return errGatherEmit
	}
	c.facts[key] = value
	return nil
}

// gatherRun invokes the method against the harness with host key
// verification skipped, which is what every test here needs and none of
// them is about.
func gatherRun(t *testing.T, server gatherServer, rc *gatherContext, params map[string]any) (collection.Result, error) {
	t.Helper()

	full := map[string]any{"insecure_skip_host_key_verify": true}
	for key, value := range params {
		full[key] = value
	}
	return facts.Gather(context.Background(), rc, newGatherTarget(server), full)
}

// gatherCommandOutput runs a command on THIS machine and returns its
// trimmed output, which is how a test computes an expected value without
// asking the method what it thinks.
func gatherCommandOutput(t *testing.T, name string, args ...string) string {
	t.Helper()

	out, err := exec.Command(name, args...).Output()
	if err != nil {
		t.Skipf("%s is not usable on this machine: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// gatherReadFile returns a system file's contents, skipping the test when
// the machine does not have it.
func gatherReadFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path) // #nosec G304 -- a fixed system path written in this test
	if err != nil {
		t.Skipf("this machine has no %s: %v", path, err)
	}
	return string(data)
}

// gatherOSReleaseValue pulls one KEY=VALUE out of /etc/os-release, with
// its own parser rather than the method's, so a bug in the method's
// parser cannot make this test agree with it.
func gatherOSReleaseValue(t *testing.T, key string) string {
	t.Helper()

	for _, line := range strings.Split(gatherReadFile(t, "/etc/os-release"), "\n") {
		name, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found && name == key {
			return strings.Trim(value, "\"'")
		}
	}
	t.Skipf("this machine's /etc/os-release has no %s", key)
	return ""
}

// TestGather_LinuxAnswersEverythingThisSuiteWouldOtherwiseSkip is the
// negative control on every t.Skipf in this file.
//
// The skips are correct and load-bearing. This suite probes THIS machine
// through a real SSH server and a real /bin/sh, so on a host that has no
// /etc/os-release and no /proc -- macOS, which a developer may well be
// running this on -- four of the seven facts genuinely cannot be
// answered, and a test asserting them would be asserting nothing about
// the method.
//
// The danger is the identical skip firing on Linux, which is the platform
// every one of those facts was recorded against and the one every test
// result this repository has comes from. It would be completely silent:
// `go test` prints SKIP only under -v, neither `make test-race` nor
// `make test-no-docker` passes -v, the package still reports ok, and the
// coverage floor does not move either, because a skipped test's lines
// were never counted in the first place. A minimal container image with
// no /etc/os-release is all it would take, and the first anyone would
// know is a bug shipping in a fact nothing had exercised for months.
//
// So the same missing file that is a legitimate skip elsewhere is a
// failure here. That reasoning got sharper, not weaker, when
// .github/workflows/ci.yml stopped running tests altogether: this
// package's only run is a local one, so a silent skip has no second gate
// behind it to catch what it dropped.
//
// Every path and key below is one this file actually reads, taken from
// the gatherReadFile, gatherCommandOutput and gatherOSReleaseValue call
// sites rather than from gather.go's probe table: /proc/uptime is absent
// deliberately, because the method reads it but no test here does, so it
// cannot produce a skip.
func TestGather_LinuxAnswersEverythingThisSuiteWouldOtherwiseSkip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("%s is not expected to answer the Linux facts; this guard exists to catch Linux quietly stopping to", runtime.GOOS)
	}

	if _, err := exec.Command("uname", "-n").Output(); err != nil {
		t.Errorf("uname is not usable on this Linux machine, so every test reading it skipped instead of running: %v", err)
	}

	for _, path := range []string{"/etc/os-release", "/proc/meminfo", "/proc/cpuinfo"} {
		if _, err := os.ReadFile(path); err != nil { // #nosec G304 -- fixed system paths written in this test
			t.Errorf("this Linux machine cannot read %s, so every test reading it skipped instead of running: %v", path, err)
		}
	}

	release, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return // Already reported above; the key check below has nothing to read.
	}
	for _, key := range []string{"NAME", "VERSION_ID"} {
		found := false
		for _, line := range strings.Split(string(release), "\n") {
			if name, _, ok := strings.Cut(strings.TrimSpace(line), "="); ok && name == key {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("this Linux machine's /etc/os-release has no %s, so the tests comparing against it skipped instead of running", key)
		}
	}
}

// TestGather_Registered proves the method registered itself as
// implemented, and that it answers the reversibility question with the
// only answer a read-only method can give.
//
// The Notes are pinned as non-empty because registration refuses a
// not-reversible method without them, and a test that only checked the
// boolean would pass against a manifest that could never load.
func TestGather_Registered(t *testing.T) {
	d, ok := collection.Lookup("facts.gather")
	if !ok {
		t.Fatalf("collection.Lookup(%q) found nothing; did this package's init() run?", "facts.gather")
	}
	if d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("Manifest.Status = %v, want %v", d.Manifest.Status, collection.StatusImplemented)
	}
	if d.Invoke == nil {
		t.Error("Invoke is nil, so the dispatcher has nothing to call")
	}
	if d.Manifest.Reversibility.Reversible {
		t.Error("Reversibility.Reversible = true: reading changes nothing, so no run can ever emit an inverse")
	}
	if d.Manifest.Reversibility.Notes == "" {
		t.Error("Reversibility.Notes is empty, which registration itself refuses")
	}
}

// TestGather_EmitsEveryFactThisMachineCanAnswer is the happy path: every
// expected value is read from the same source by independent code.
//
// It also pins the fact-versus-stat distinction, which is the one thing
// about this method that a reader would most easily get wrong. A stat
// expires at the end of the run, so a kernel version recorded as one
// answers no question next month, and the whole reason to gather these is
// to be able to ask that question.
func TestGather_EmitsEveryFactThisMachineCanAnswer(t *testing.T) {
	server := startGatherServer(t)
	rc := newGatherContext(server)

	result, err := gatherRun(t, server, rc, nil)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if result.Changed {
		t.Error("a run that only read reported a change")
	}
	if len(rc.stats) != 0 {
		t.Errorf("stats = %v, want none: everything gathered here is drift data and belongs in a fact", rc.stats)
	}

	node := gatherCommandOutput(t, "uname", "-n")
	short, _, _ := strings.Cut(node, ".")
	for key, want := range map[string]any{
		"ansible_hostname":             short,
		"ansible_kernel":               gatherCommandOutput(t, "uname", "-r"),
		"ansible_architecture":         gatherCommandOutput(t, "uname", "-m"),
		"ansible_distribution":         gatherOSReleaseValue(t, "NAME"),
		"ansible_distribution_version": gatherOSReleaseValue(t, "VERSION_ID"),
	} {
		if got := rc.facts[key]; got != want {
			t.Errorf("%s fact = %v, want %v read straight off this machine", key, got, want)
		}
	}

	// Memory is checked against /proc/meminfo's own number, converted here
	// rather than trusted from the method, so a conversion that multiplied
	// where it should divide would fail.
	wantMB, err := strconv.ParseInt(strings.Fields(strings.SplitN(gatherReadFile(t, "/proc/meminfo"), "\n", 2)[0])[1], 10, 64)
	if err != nil {
		t.Fatalf("reading MemTotal from /proc/meminfo: %v", err)
	}
	if got := rc.facts["ansible_memtotal_mb"]; got != wantMB/1024 {
		t.Errorf("ansible_memtotal_mb fact = %v, want %d", got, wantMB/1024)
	}

	// Uptime cannot be compared exactly, since it moves between the
	// method's read and this one. What is checked is that a real number
	// arrived: a machine that has been up at all reports more than zero
	// seconds, and a fact that was never determined is absent rather than
	// zero.
	uptime, ok := rc.facts["ansible_uptime_seconds"].(int64)
	if !ok {
		t.Fatalf("ansible_uptime_seconds fact = %#v, want an int64", rc.facts["ansible_uptime_seconds"])
	}
	if uptime <= 0 {
		t.Errorf("ansible_uptime_seconds fact = %d, want the real uptime of a running machine", uptime)
	}

	// The processor count is present exactly when /proc/cpuinfo carries
	// the field it is counted from, which is what makes an absent fact a
	// real answer rather than a bug.
	wantSockets := gatherDistinctPhysicalIDs(t)
	got, present := rc.facts["ansible_processor_count"]
	switch {
	case wantSockets == 0 && present:
		t.Errorf("ansible_processor_count fact = %v, want it absent: this machine's /proc/cpuinfo has no physical id field", got)
	case wantSockets != 0 && got != wantSockets:
		t.Errorf("ansible_processor_count fact = %v, want %d distinct physical ids", got, wantSockets)
	}
}

// gatherDistinctPhysicalIDs counts the distinct physical id values in
// this machine's /proc/cpuinfo, with its own parser.
func gatherDistinctPhysicalIDs(t *testing.T) int {
	t.Helper()

	seen := map[string]struct{}{}
	for _, line := range strings.Split(gatherReadFile(t, "/proc/cpuinfo"), "\n") {
		label, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(label) == "physical id" {
			seen[strings.TrimSpace(value)] = struct{}{}
		}
	}
	return len(seen)
}

// TestGather_OmitsEverythingItCannotDetermine drives the case where the
// device answers nothing at all, by emptying the PATH the remote shell
// searches.
//
// That is a real device, not an injected failure: an account whose PATH
// is broken finds no uname and no cat, and every command exits 127 having
// printed nothing. What must happen then is that the task succeeds having
// emitted NOTHING. The alternative a careless implementation reaches for,
// recording each fact as the empty string, is exactly what makes a later
// condition comparing ansible_distribution give a confidently wrong
// answer.
func TestGather_OmitsEverythingItCannotDetermine(t *testing.T) {
	server := startGatherServer(t)
	rc := newGatherContext(server)

	// The harness runs each command through /bin/sh by absolute path, so
	// the shell still starts and only the commands inside it are lost.
	t.Setenv("PATH", "")

	result, err := gatherRun(t, server, rc, nil)
	if err != nil {
		t.Fatalf("Gather: %v, want a device that could answer nothing to be a successful run with no facts", err)
	}
	if result.Changed {
		t.Error("a run that read nothing reported a change")
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v, want none: a fact that could not be determined is left out, never guessed and never empty", rc.facts)
	}
}

// TestGather_OmitsOnlyTheFactsItCannotDetermine proves the omission is
// per fact rather than all or nothing.
//
// The PATH is narrowed to a directory holding uname alone, so the three
// uname facts arrive and the four read out of files cannot. A method that
// abandoned the whole gather at the first command it could not run would
// fail this, and so would one that emitted empty strings for the rest.
func TestGather_OmitsOnlyTheFactsItCannotDetermine(t *testing.T) {
	server := startGatherServer(t)
	rc := newGatherContext(server)

	// Resolved before PATH is emptied, since afterward there is nothing to
	// resolve it with.
	uname, err := exec.LookPath("uname")
	if err != nil {
		t.Skipf("this machine has no uname: %v", err)
	}
	kernel := gatherCommandOutput(t, "uname", "-r")

	only := t.TempDir()
	if err := os.Symlink(uname, filepath.Join(only, "uname")); err != nil {
		t.Fatalf("linking uname into the narrowed PATH: %v", err)
	}
	t.Setenv("PATH", only)

	if _, err := gatherRun(t, server, rc, nil); err != nil {
		t.Fatalf("Gather: %v", err)
	}

	if got := rc.facts["ansible_kernel"]; got != kernel {
		t.Errorf("ansible_kernel fact = %v, want %q: uname was reachable and its fact should have arrived", got, kernel)
	}
	for _, key := range []string{"ansible_distribution", "ansible_distribution_version", "ansible_memtotal_mb", "ansible_uptime_seconds"} {
		if got, present := rc.facts[key]; present {
			t.Errorf("%s fact = %v, want it absent: cat was not on the PATH, so nothing could read it", key, got)
		}
	}
}

// TestGather_FilterNarrowsToOneFactOfAProbeThatAnswersTwo is the case a
// filter applied only to the command list would get wrong.
//
// One command reads /etc/os-release and answers two facts, so a filter
// naming one of them keeps that command and must still drop the other
// fact on the way out. Asking for ansible_distribution and receiving
// ansible_distribution_version as well is the failure this pins.
func TestGather_FilterNarrowsToOneFactOfAProbeThatAnswersTwo(t *testing.T) {
	server := startGatherServer(t)
	rc := newGatherContext(server)

	if _, err := gatherRun(t, server, rc, map[string]any{
		"filter": []any{"ansible_distribution"},
	}); err != nil {
		t.Fatalf("Gather: %v", err)
	}

	if got := rc.facts["ansible_distribution"]; got != gatherOSReleaseValue(t, "NAME") {
		t.Errorf("ansible_distribution fact = %v, want the name this machine reports", got)
	}
	if got, present := rc.facts["ansible_distribution_version"]; present {
		t.Errorf("ansible_distribution_version fact = %v, want it absent: the filter named its sibling, not it", got)
	}
	if len(rc.facts) != 1 {
		t.Errorf("facts = %v, want exactly the one the filter named", rc.facts)
	}
}

// TestGather_FilterMatchesAsAGlob proves the patterns are shell-style,
// the way ansible.builtin.setup's filter is, rather than exact names.
//
// Both expected values are read off this machine BEFORE the round trip,
// for two reasons that are really one. It asserts the values rather than
// the keys, so a glob that selected the right two facts and filled them
// with the wrong ones cannot pass; and reading them is what gives this
// test the same environmental guard every one of its siblings already
// has, since gatherOSReleaseValue skips when the machine cannot answer.
//
// That guard is not a macOS special case, though macOS is where its
// absence was found. This suite probes THIS machine through a real SSH
// server and a real /bin/sh, and gather.go's os-release probe is one
// `cat /etc/os-release`: a host without that file answers neither fact,
// which is the documented contract rather than a defect. Asking whether
// the glob matched two facts the device never had is a question with no
// meaningful answer, so the test declines to ask it.
//
// TestGather_LinuxAnswersEverythingThisSuiteWouldOtherwiseSkip is what
// stops that skip firing where the evidence is supposed to come from.
func TestGather_FilterMatchesAsAGlob(t *testing.T) {
	want := map[string]any{
		"ansible_distribution":         gatherOSReleaseValue(t, "NAME"),
		"ansible_distribution_version": gatherOSReleaseValue(t, "VERSION_ID"),
	}

	server := startGatherServer(t)
	rc := newGatherContext(server)

	if _, err := gatherRun(t, server, rc, map[string]any{
		"filter": []any{"ansible_distribution*"},
	}); err != nil {
		t.Fatalf("Gather: %v", err)
	}

	for key, value := range want {
		if got := rc.facts[key]; got != value {
			t.Errorf("%s fact = %v, want %v read straight off this machine: the glob should have matched it", key, got, value)
		}
	}
	if len(rc.facts) != 2 {
		t.Errorf("facts = %v, want only the two the glob matches", rc.facts)
	}
}

// TestGather_FilterSkipsTheCommandsItExcludes proves the narrowing
// happens before the round trip rather than after it.
//
// The server accepts ONE session channel and refuses every one after it,
// so a run that still sent all seven commands would fail on the second.
// Reaching the end with the one fact asked for is the only way this can
// pass, and it is the only assertion available that can tell a filter
// applied to the output from a filter applied to the work.
func TestGather_FilterSkipsTheCommandsItExcludes(t *testing.T) {
	server := startGatherServerWithSessionBudget(t, 1)
	rc := newGatherContext(server)
	kernel := gatherCommandOutput(t, "uname", "-r")

	if _, err := gatherRun(t, server, rc, map[string]any{
		"filter": []any{"ansible_kernel"},
	}); err != nil {
		t.Fatalf("Gather: %v, want a narrowed run to have opened only one session", err)
	}
	if got := rc.facts["ansible_kernel"]; got != kernel {
		t.Errorf("ansible_kernel fact = %v, want %q", got, kernel)
	}
}

// TestGather_RefusesAFilterItCannotRead covers the three ways a filter
// arrives as something this cannot match against, and proves each refusal
// happens before any connection.
//
// A nil device would fail in connect, so reaching the expected message
// with one is itself the proof that nothing tried to dial.
func TestGather_RefusesAFilterItCannotRead(t *testing.T) {
	tests := []struct {
		name   string
		filter any
		want   string
	}{
		{name: "a bare string", filter: "ansible_kernel", want: "filter must be a list of strings"},
		{name: "a list of numbers", filter: []any{42}, want: "filter[0] is int, not a string"},
		{name: "a malformed pattern", filter: []any{"ansible_["}, want: "is malformed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := facts.Gather(context.Background(), nil, nil, map[string]any{"filter": tt.filter})
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// TestGather_RefusesAFilterThatMatchesNothing proves a pattern set
// matching none of the known facts is a refusal rather than a silent
// empty gather.
//
// Ansible would return nothing here. This does not, because a pattern
// matching none of eight fixed names is a typo far more often than an
// intention, and the refusal has to list what it could have matched or
// the author has nothing to compare their spelling against.
func TestGather_RefusesAFilterThatMatchesNothing(t *testing.T) {
	_, err := facts.Gather(context.Background(), nil, nil, map[string]any{
		"filter": []any{"ansible_kernal"},
	})
	if err == nil {
		t.Fatal("a filter matching nothing was accepted, so the task would have gathered nothing and reported success")
	}
	if !strings.Contains(err.Error(), "matches none of the facts") {
		t.Errorf("error = %q, want it to say the filter matched nothing", err)
	}
	if !strings.Contains(err.Error(), "ansible_kernel") {
		t.Errorf("error = %q, want it to list the names it could have matched", err)
	}
}

// TestGather_AcceptsAnEmptyFilterAsEverything pins the Ansible behavior
// an empty list has to keep.
//
// ansible.builtin.setup's own default for filter is the empty list, and
// it means every fact. Refusing it here, next to a refusal for a filter
// that matches nothing, would be an easy and wrong symmetry.
func TestGather_AcceptsAnEmptyFilterAsEverything(t *testing.T) {
	server := startGatherServer(t)
	rc := newGatherContext(server)

	if _, err := gatherRun(t, server, rc, map[string]any{"filter": []any{}}); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if got := rc.facts["ansible_kernel"]; got != gatherCommandOutput(t, "uname", "-r") {
		t.Errorf("ansible_kernel fact = %v, want an empty filter to have gathered everything", got)
	}
}

// TestGather_RefusesAnUnreachableDevice covers the connect failure path,
// which is what a device with no SSH transport produces.
func TestGather_RefusesAnUnreachableDevice(t *testing.T) {
	_, err := facts.Gather(context.Background(), newGatherContext(gatherServer{}), newGatherUnreachable(), nil)
	if err == nil {
		t.Fatal("expected a device with no SSH transport to be refused")
	}
	if !strings.Contains(err.Error(), "not reachable over SSH") {
		t.Errorf("error = %q, want it to say the device cannot be reached", err)
	}
}

// TestGather_TransportFailureIsReported covers the branch where the
// connection authenticates and then cannot carry a probe.
//
// A session budget of zero is what that looks like from this side, and it
// is a real protocol-level refusal rather than an injected Go error. The
// point is that a broken connection is never mistaken for a device with
// nothing to say: emitting a silently partial fact set is worse than
// failing, because the facts that are missing are exactly the ones nobody
// would think to check.
func TestGather_TransportFailureIsReported(t *testing.T) {
	server := startGatherServerWithSessionBudget(t, 0)
	rc := newGatherContext(server)

	_, err := gatherRun(t, server, rc, nil)
	if err == nil {
		t.Fatal("a broken connection was reported as a device with no facts")
	}
	if !strings.Contains(err.Error(), "open session") {
		t.Errorf("error = %q, want it to say the session could not be opened", err)
	}
	if !strings.Contains(err.Error(), "uname -n") {
		t.Errorf("error = %q, want it to name the command that failed", err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts = %v, want none recorded for a run that failed", rc.facts)
	}
}

// TestGather_FactRecordFailureIsReported covers the branch where a probe
// answered and recording the answer did not.
//
// Swallowing it would leave a fact set that looks complete and is not,
// which is the failure this method's whole omission rule exists to
// prevent from happening by accident.
func TestGather_FactRecordFailureIsReported(t *testing.T) {
	server := startGatherServer(t)
	rc := newGatherContext(server)
	rc.failOn = "ansible_hostname"

	_, err := gatherRun(t, server, rc, nil)
	if err == nil {
		t.Fatal("a failure to record a fact was swallowed")
	}
	if !errors.Is(err, errGatherEmit) {
		t.Errorf("error = %q, want it to carry what recording returned", err)
	}
	if !strings.Contains(err.Error(), "ansible_hostname") {
		t.Errorf("error = %q, want it to name the fact that could not be recorded", err)
	}
}
