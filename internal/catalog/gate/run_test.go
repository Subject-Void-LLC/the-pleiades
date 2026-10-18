// Tests that gate.run runs a manifest's checks on the device and reports
// each one truthfully: real commands, over a real SSH session, through the
// real timeout command.
package gate_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/gate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestRun_Registered declares what the design note promises.
func TestRun_Registered(t *testing.T) {
	d, ok := collection.Lookup("gate.run")
	if !ok {
		t.Fatal("gate.run is not registered")
	}
	m := d.Manifest
	if m.Status != collection.StatusImplemented || !m.SupportsCheck || m.Reversibility.Reversible || m.Reversibility.ReadOnly {
		t.Errorf("manifest = %+v", m)
	}
	if err := d.CheckCall(nil); err == nil {
		t.Error("check_mode on gate.run is not refused at validation")
	}
}

// TestRun_ReportsEachOutcomeTruthfully runs a pass, a failure, a skip by
// each precondition kind, a skip by declared exit code, a check in a
// subdirectory and an unselected one, and finds each reported as itself,
// with the failure's output in the task's error.
func TestRun_ReportsEachOutcomeTruthfully(t *testing.T) {
	h := newHarness(t)
	if err := os.Mkdir(filepath.Join(h.project, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	// An operating system the device does not run, for the os precondition:
	// FreeBSD, unless the tests are running on FreeBSD.
	otherOS := "freebsd"
	if runtime.GOOS == "freebsd" {
		otherOS = "linux"
	}
	manifest := h.manifest(t, `version: 1
kinds: [{name: browser}]
defaults: {timeout: 30s}
checks:
  - {name: passes, kind: pure, command: ["true"]}
  - {name: fails, kind: pure, shell: "printf 'boom\n'; exit 3"}
  - {name: in-sub, kind: pure, dir: sub, shell: "test \"$(basename \"$PWD\")\" = sub"}
  - {name: no-tool, kind: pure, shell: "true", requires: {commands: [pleiades-no-such-tool]}}
  - {name: no-env, kind: pure, shell: "true", requires: {env: [PLEIADES_GATE_UNSET_VARIABLE]}}
  - {name: no-file, kind: pure, shell: "true", requires: {files: [missing.txt]}}
  - {name: has-file, kind: pure, shell: "true", requires: {files: [gates.yaml]}}
  - {name: other-os, kind: pure, shell: "true", requires: {os: `+otherOS+`}}
  - {name: declared-skip, kind: db, shell: "echo 'no database here'; exit 5", skip_exit: [5]}
  - {name: ui, kind: browser, shell: "true"}
`)
	_, err := gate.Run(context.Background(), h.rc, h.device, params(manifest, map[string]any{"kinds": []any{"pure", "db"}}))
	if err == nil {
		t.Fatal("a manifest with a failing check passed")
	}
	got := resultsByName(t, h.rc)
	for name, want := range map[string]string{
		"passes": "PASS", "fails": "FAIL", "in-sub": "PASS", "has-file": "PASS",
		"no-tool": "SKIP", "no-env": "SKIP", "no-file": "SKIP", "other-os": "SKIP", "declared-skip": "SKIP", "ui": "SKIP",
	} {
		if got[name]["status"] != want {
			t.Errorf("%s = %v, want %s", name, got[name], want)
		}
	}
	for name, reason := range map[string]string{
		"no-tool":       "needs command pleiades-no-such-tool",
		"no-env":        "needs env PLEIADES_GATE_UNSET_VARIABLE",
		"no-file":       "needs file missing.txt",
		"other-os":      "needs os " + otherOS + ", and the device runs",
		"declared-skip": "exited 5, which it declares means skipped: no database here",
		"ui":            "its kind, browser, is not among the kinds",
		"fails":         "exited 3",
	} {
		if r, _ := got[name]["reason"].(string); !strings.Contains(r, reason) {
			t.Errorf("%s's reason = %q, want it to say %q", name, r, reason)
		}
	}
	if code := got["fails"]["exit_code"]; code != 3 {
		t.Errorf("fails exit_code = %v", code)
	}
	if h.rc.stats["summary"] != "3 passed, 1 failed, 6 skipped" {
		t.Errorf("summary = %v", h.rc.stats["summary"])
	}
	// The error tells the story too, since it is what a person reads first.
	for _, want := range []string{"the gate failed (3 passed, 1 failed, 6 skipped): fails", "fails (pure): exited 3", "boom", "skipped no-tool: needs command"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v\nwant it to say %q", err, want)
		}
	}
	if ledger, _ := h.rc.stats["ledger"].(string); !strings.Contains(ledger, "6 check(s) skipped") {
		t.Errorf("ledger = %q", ledger)
	}
}

// TestRun_AllPassingSucceedsAndChangesNothing.
func TestRun_AllPassingSucceedsAndChangesNothing(t *testing.T) {
	h := newHarness(t)
	manifest := h.manifest(t, "version: 1\ndefaults: {timeout: 30s}\nchecks:\n  - {name: a, kind: pure, command: [\"true\"]}\n  - {name: b, kind: pure, shell: \"exit 0\"}\n")
	res, err := gate.Run(context.Background(), h.rc, h.device, params(manifest, nil))
	if err != nil || res.Changed {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if h.rc.stats["passed"] != 2 || h.rc.stats["failed"] != 0 {
		t.Errorf("stats = %v", h.rc.stats)
	}
	if _, ledger := h.rc.stats["ledger"]; ledger {
		t.Error("a run that skipped nothing has a ledger")
	}
}

// TestRun_FailOnSkipFailsASkip.
func TestRun_FailOnSkipFailsASkip(t *testing.T) {
	h := newHarness(t)
	manifest := h.manifest(t, "version: 1\ndefaults: {timeout: 30s}\nchecks:\n  - {name: a, kind: pure, shell: \"true\"}\n  - {name: b, kind: pure, shell: \"true\", requires: {env: [PLEIADES_GATE_UNSET_VARIABLE]}}\n")
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(manifest, nil)); err != nil {
		t.Fatalf("a skip failed the task without fail_on_skip: %v", err)
	}
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(manifest, map[string]any{"fail_on_skip": true})); err == nil || !strings.Contains(err.Error(), "fail_on_skip") {
		t.Fatalf("fail_on_skip = %v", err)
	}
}

// TestRun_ATimeoutStopsTheCheckOnTheDevice: a check past its timeout is a
// FAIL saying so, and the process it started is gone.
func TestRun_ATimeoutStopsTheCheckOnTheDevice(t *testing.T) {
	h := newHarness(t)
	marker := fmt.Sprintf("pleiades-gate-timeout-%d", time.Now().UnixNano())
	manifest := h.manifest(t, "version: 1\nchecks:\n  - {name: slow, kind: pure, timeout: 1s, shell: \"sleep 30; echo "+marker+"\"}\n")
	start := time.Now()
	_, err := gate.Run(context.Background(), h.rc, h.device, params(manifest, nil))
	if err == nil || !strings.Contains(err.Error(), "timed out after 1s") {
		t.Fatalf("Run = %v", err)
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("a 1s timeout took %v", time.Since(start))
	}
	if running(t, marker) {
		t.Error("the timed-out check is still running on the device")
	}
}

// TestRun_TheTasksDeadlineStopsARunningCheck: a check running when the
// task's deadline passes is stopped on the device and is a FAIL saying
// so, and a check the run never started is a SKIP saying why.
func TestRun_TheTasksDeadlineStopsARunningCheck(t *testing.T) {
	h := newHarness(t)
	marker := fmt.Sprintf("pleiades-gate-deadline-%d", time.Now().UnixNano())
	manifest := h.manifest(t, "version: 1\ndefaults: {timeout: 5m}\nchecks:\n  - {name: long, kind: pure, shell: \"sleep 60; echo "+marker+"\"}\n  - {name: next, kind: pure, shell: \"true\"}\n")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := gate.Run(ctx, h.rc, h.device, params(manifest, nil))
	if err == nil {
		t.Fatal("a run its deadline cut short passed")
	}
	got := resultsByName(t, h.rc)
	if r, _ := got["long"]["reason"].(string); got["long"]["status"] != "FAIL" || !strings.Contains(r, "deadline passed while it ran") {
		t.Errorf("long = %v", got["long"])
	}
	if r, _ := got["next"]["reason"].(string); got["next"]["status"] != "SKIP" || !strings.Contains(r, "deadline passed before it started") {
		t.Errorf("next = %v", got["next"])
	}
	deadline := time.Now().Add(15 * time.Second)
	for running(t, marker) {
		if time.Now().After(deadline) {
			t.Fatal("the check the deadline stopped is still running on the device")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestRun_KeepsABoundedEscapedTail: a check printing a thousand lines and
// a terminal escape keeps its last lines, escaped, counting the rest.
func TestRun_KeepsABoundedEscapedTail(t *testing.T) {
	h := newHarness(t)
	manifest := h.manifest(t, "version: 1\ndefaults: {timeout: 30s}\nchecks:\n  - {name: chatty, kind: pure, shell: \"i=0; while [ $i -lt 1000 ]; do echo line $i; i=$((i+1)); done; printf 'red \\\\033[31mend\\\\n'; exit 1\"}\n")
	_, err := gate.Run(context.Background(), h.rc, h.device, params(manifest, nil))
	if err == nil {
		t.Fatal("a failing check passed")
	}
	tail, _ := resultsByName(t, h.rc)["chatty"]["tail"].(string)
	lines := strings.Split(tail, "\n")
	if !strings.HasPrefix(lines[0], "... 961 earlier line(s) left out") || len(lines) != 41 {
		t.Errorf("tail has %d lines, first %q", len(lines), lines[0])
	}
	esc := string(rune(92)) + "u001b"
	if !strings.Contains(tail, "red "+esc+"[31mend") || strings.ContainsRune(tail, 0x1b) {
		t.Errorf("the escape was not escaped: %q", lines[len(lines)-1])
	}
	if strings.Count(err.Error(), "line ") > 15 {
		t.Error("the error carries more than its bounded share of the output")
	}
}

// TestRun_ParallelismRunsChecksTogether: two one-second checks take about
// one second with parallelism 2, and about two without it.
func TestRun_ParallelismRunsChecksTogether(t *testing.T) {
	h := newHarness(t)
	serial := h.manifest(t, "version: 1\ndefaults: {timeout: 30s}\nchecks:\n  - {name: a, kind: pure, shell: \"sleep 1\"}\n  - {name: b, kind: pure, shell: \"sleep 1\"}\n")
	start := time.Now()
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(serial, nil)); err != nil {
		t.Fatal(err)
	}
	// A lower bound only: a slow machine makes it longer, never shorter.
	if took := time.Since(start); took < 2*time.Second {
		t.Errorf("two 1s checks took %v by default, want them one after the other", took)
	}

	// Together is proved by a rendezvous, not a clock, which a busy runner
	// would stretch (FAILURE_PATTERNS 211, 422): each check marks that it
	// started and waits for the other's mark, so both pass only when both
	// run at once, however slowly.
	dir := t.TempDir()
	meet := func(mine, theirs string) string {
		return fmt.Sprintf("touch %s/%s; i=0; while [ ! -e %s/%s ] && [ $i -lt 300 ]; do sleep 0.1; i=$((i+1)); done; [ -e %s/%s ]", dir, mine, dir, theirs, dir, theirs)
	}
	together := h.manifest(t, fmt.Sprintf("version: 1\ndefaults: {timeout: 60s}\nchecks:\n  - {name: a, kind: pure, shell: %q}\n  - {name: b, kind: pure, shell: %q}\n", meet("a", "b"), meet("b", "a")))
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(together, map[string]any{"parallelism": 2})); err != nil {
		t.Fatalf("with parallelism 2 the two checks did not meet: %v", err)
	}
	got := resultsByName(t, h.rc)
	if got["a"]["status"] != "PASS" || got["b"]["status"] != "PASS" {
		t.Errorf("a = %v, b = %v; each should have seen the other running", got["a"], got["b"])
	}
}

// TestRun_ATCPPreconditionIsTestedFromTheDevice: a listening endpoint
// lets the check run, a closed one skips it.
func TestRun_ATCPPreconditionIsTestedFromTheDevice(t *testing.T) {
	h := newHarness(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	// An address nothing can answer, not a released port, which another
	// process may take (FAILURE_PATTERNS 123, 177, 183): TEST-NET-1 (RFC
	// 5737) is never routed, so the dial fails, at once where there is no
	// route and within the probe's budget where packets are dropped. Port 0,
	// those entries' other remedy, is refused by the manifest's validation.
	gate.SetProbeTimeout(t, 2*time.Second)
	closedAddr := "192.0.2.1:9"
	manifest := h.manifest(t, fmt.Sprintf("version: 1\ndefaults: {timeout: 30s}\nchecks:\n  - {name: up, kind: network, shell: \"true\", requires: {tcp: [%q]}}\n  - {name: down, kind: network, shell: \"true\", requires: {tcp: [%q]}}\n", ln.Addr().String(), closedAddr))
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(manifest, nil)); err != nil {
		t.Fatal(err)
	}
	got := resultsByName(t, h.rc)
	if got["up"]["status"] != "PASS" {
		t.Errorf("up = %v", got["up"])
	}
	if r, _ := got["down"]["reason"].(string); got["down"]["status"] != "SKIP" || !strings.Contains(r, "needs tcp "+closedAddr) {
		t.Errorf("down = %v", got["down"])
	}
}

// TestRun_RefusesBeforeRunningAnything: a missing or invalid manifest, a
// selection that runs nothing, a bad parameter, a missing directory, and
// an image check on a device with no engine.
func TestRun_RefusesBeforeRunningAnything(t *testing.T) {
	h := newHarness(t)
	marker := filepath.Join(h.project, "ran")
	valid := h.manifest(t, "version: 1\ndefaults: {timeout: 30s}\nchecks:\n  - {name: a, kind: pure, shell: \"touch "+marker+"\"}\n")
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"a missing manifest":    {params(filepath.Join(h.project, "nope.yaml"), nil), "could not read the manifest"},
		"an unknown kind":       {params(valid, map[string]any{"kinds": []any{"gpu"}}), "not a kind"},
		"nothing selected":      {params(valid, map[string]any{"heavy": "only"}), "runs no check"},
		"a parallelism":         {params(valid, map[string]any{"parallelism": 17}), "parallelism 17"},
		"a manifest path":       {params("a\nb", nil), "not a path"},
		"no manifest parameter": {map[string]any{"insecure_skip_host_key_verify": true}, "manifest"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := gate.Run(context.Background(), h.rc, h.device, tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run = %v, want it to say %q", err, tc.want)
			}
		})
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused run ran a check")
	}

	bad := h.manifest(t, "version: 1\nchecks:\n  - {name: a, shell: \"true\", timeout: 1s}\n")
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(bad, nil)); err == nil || !strings.Contains(err.Error(), "no kind") {
		t.Fatalf("a check with no kind = %v", err)
	}
	image := h.manifest(t, "version: 1\nchecks:\n  - {name: a, kind: pure, shell: \"true\", timeout: 1s, image: 'alpine@sha256:"+strings.Repeat("ab", 32)+"'}\n")
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(image, nil)); err == nil || !strings.Contains(err.Error(), "no engine was onboarded") {
		t.Fatalf("an image check without an engine = %v", err)
	}
	nodir := h.manifest(t, "version: 1\nchecks:\n  - {name: a, kind: pure, shell: \"true\", timeout: 1s, dir: absent}\n")
	if _, err := gate.Run(context.Background(), h.rc, h.device, params(nodir, nil)); err == nil || !strings.Contains(err.Error(), "is not a directory on the device") {
		t.Fatalf("a missing directory = %v", err)
	}
}

// TestCheckRun_ValidatesThenAnswersUnchecked: a check reads and validates
// the manifest, runs nothing, and says the task cannot be checked; a bad
// manifest fails the check as it would fail the run.
func TestCheckRun_ValidatesThenAnswersUnchecked(t *testing.T) {
	h := newHarness(t)
	marker := filepath.Join(h.project, "ran")
	manifest := h.manifest(t, "version: 1\ndefaults: {timeout: 30s}\nchecks:\n  - {name: a, kind: pure, shell: \"touch "+marker+"\"}\n  - {name: b, kind: db, shell: \"touch "+marker+"\"}\n")
	_, err := gate.CheckRun(context.Background(), h.rc, h.device, params(manifest, map[string]any{"kinds": []any{"pure"}}))
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, "1 check(s) would run (a)") {
		t.Fatalf("CheckRun = %v, want an unchecked answer naming what would run", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("a check of gate.run ran a check")
	}
	bad := h.manifest(t, "version: 1\nchecks: []\n")
	if _, err := gate.CheckRun(context.Background(), h.rc, h.device, params(bad, nil)); err == nil || errors.As(err, &cannot) {
		t.Fatalf("a bad manifest's check = %v, want a plain failure", err)
	}
}

// running reports whether a process whose command line holds marker is
// still running on this machine, the harness's device.
func running(t *testing.T, marker string) bool {
	t.Helper()
	out, _ := exec.Command("ps", "-eo", "args").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, marker) && !strings.Contains(line, "ps -eo") {
			return true
		}
	}
	return false
}

// TestRun_ACancellationStopsARunningCheck: a run canceled mid-check stops
// it on the device, saying it was canceled rather than out of time.
func TestRun_ACancellationStopsARunningCheck(t *testing.T) {
	h := newHarness(t)
	marker := fmt.Sprintf("pleiades-gate-cancel-%d", time.Now().UnixNano())
	manifest := h.manifest(t, "version: 1\ndefaults: {timeout: 5m}\nchecks:\n  - {name: long, kind: pure, shell: \"sleep 60; echo "+marker+"\"}\n")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(time.Second, cancel)
	if _, err := gate.Run(ctx, h.rc, h.device, params(manifest, nil)); err == nil {
		t.Fatal("a canceled run passed")
	}
	if r, _ := resultsByName(t, h.rc)["long"]["reason"].(string); !strings.Contains(r, "the run was canceled while it ran") {
		t.Errorf("long's reason = %q", r)
	}
	deadline := time.Now().Add(15 * time.Second)
	for running(t, marker) {
		if time.Now().After(deadline) {
			t.Fatal("the canceled check is still running on the device")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
