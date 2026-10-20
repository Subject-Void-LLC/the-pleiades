// Tests for a lab check's box: its settings, and starting it for a run and
// putting it back afterwards, with the pleiades calls replaced by a host
// that answers them (adhocStats).
package oscheck

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLabBox reads the VM settings: none is no box, the VM defaults to the
// device's name, and a VM without a host or a name pleiades would misread
// is refused.
func TestLabBox(t *testing.T) {
	for name, tc := range map[string]struct {
		values         map[string]string
		host, vm, want string
	}{
		"none":          {map[string]string{}, "", "", ""},
		"host alone":    {map[string]string{"H": "vengeance"}, "vengeance", "box-1", ""},
		"host and vm":   {map[string]string{"H": "vengeance", "V": "bsd-vm"}, "vengeance", "bsd-vm", ""},
		"vm alone":      {map[string]string{"V": "bsd-vm"}, "", "", "set H too"},
		"a flag host":   {map[string]string{"H": "-x"}, "", "", "not a device name"},
		"a bad vm name": {map[string]string{"H": "vengeance", "V": "a b"}, "", "", "not a VM name"},
	} {
		host, vm, err := LabBox(Settings{Values: tc.values}, "H", "V", "box-1")
		if (tc.want == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.want) || host != tc.host || vm != tc.vm {
			t.Errorf("%s: %q %q %v, want %q %q %q", name, host, vm, err, tc.host, tc.vm, tc.want)
		}
	}
	if host, vm, err := LabBox(Settings{Values: map[string]string{"H": "x"}}, "", "", "box-1"); host != "" || vm != "" || err != nil {
		t.Errorf("a check with no VM settings: %q %q %v", host, vm, err)
	}
}

// fakeHost answers the box's pleiades calls: the VM's state, each method
// failing when fail names it, and wait.connection answering waits in turn
// (nil once they run out).
type fakeHost struct {
	state string
	fail  map[string]error
	waits []error
	calls []string
}

// install answers through h.
func (h *fakeHost) install(t *testing.T) {
	t.Helper()
	old := adhocStats
	t.Cleanup(func() { adhocStats = old })
	adhocStats = func(_ context.Context, p Pleiades, method string, params ...string) (map[string]any, error) {
		h.calls = append(h.calls, p.Device+" "+method+" "+strings.Join(params, " "))
		if err := h.fail[method]; err != nil {
			return nil, err
		}
		if method == "wait.connection" && len(h.waits) > 0 {
			err := h.waits[0]
			h.waits = h.waits[1:]
			if err != nil {
				return nil, err
			}
		}
		switch method {
		case "virt.vbox.vm.info":
			return map[string]any{"state": h.state}, nil
		case "virt.vbox.vm.start":
			h.state = "running"
		case "virt.vbox.vm.stop":
			h.state = "saved"
		}
		return map[string]any{}, nil
	}
}

var testBox = Box{Host: Pleiades{Device: "vengeance"}, Device: Pleiades{Device: "box-1"}, VM: "box-vm"}

// bootWaitCall is the box's wait after a start, as it reaches pleiades:
// three fresh logins in a row within ten minutes, backing off while it
// refuses.
const bootWaitCall = "box-1 wait.connection timeout=600 backoff=exponential sleep=2 max_sleep=30 steady=3 steady_interval=10 connect_timeout=10"

// TestRunWithBox_StartsAnOffBoxAndPutsItBack: a box that is not running is
// started and waited for before the flow, and saved after it, whatever the
// tests did; one already running is left as it was.
func TestRunWithBox_StartsAnOffBoxAndPutsItBack(t *testing.T) {
	inRepo(t)
	for _, passed := range []bool{true, false} {
		h := &fakeHost{state: "saved"}
		h.install(t)
		leg := &fakeLeg{passed: passed}
		var out, errOut bytes.Buffer
		code := runWithBox(context.Background(), options(t, "a/b"), flow, leg, testBox, &out, &errOut)
		want := []string{
			"vengeance virt.vbox.vm.info name=box-vm",
			"vengeance virt.vbox.vm.start name=box-vm",
			bootWaitCall,
			"vengeance virt.vbox.vm.stop name=box-vm mode=save",
		}
		if strings.Join(h.calls, "\n") != strings.Join(want, "\n") || h.state != "saved" {
			t.Errorf("passed %v: calls %q, state %s", passed, h.calls, h.state)
		}
		if (code == 0) != passed || !strings.Contains(out.String(), "starting the VM box-vm on vengeance (it was saved)") ||
			!strings.Contains(out.String(), "put the VM box-vm back on vengeance") || strings.Join(leg.steps, " ") != "reachable ready ship test" {
			t.Errorf("passed %v: code %d, steps %v\n%s%s", passed, code, leg.steps, out.String(), errOut.String())
		}
	}

	h := &fakeHost{state: "running"}
	h.install(t)
	if code := runWithBox(context.Background(), options(t, "a/b"), flow, &fakeLeg{passed: true}, testBox, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 ||
		len(h.calls) != 1 || h.state != "running" {
		t.Errorf("a running box: code %d, calls %q", code, h.calls)
	}
}

// TestBoxUp_WaitsAsItsMachineNeeds: a box naming its own wait (a Windows
// box, which misses connections for a minute or two after it resumes) is
// waited for with it, six fresh logins twenty seconds apart, rather than the
// default streak.
func TestBoxUp_WaitsAsItsMachineNeeds(t *testing.T) {
	h := &fakeHost{state: "saved"}
	h.install(t)
	box := testBox
	box.Wait = WindowsWait
	if started, err := box.Up(context.Background(), "t", &bytes.Buffer{}); !started || err != nil {
		t.Fatalf("Up: %v %v", started, err)
	}
	want := "box-1 wait.connection timeout=600 backoff=exponential sleep=2 max_sleep=30 steady=6 steady_interval=20 connect_timeout=10"
	if got := h.calls[2:]; len(got) != 1 || got[0] != want {
		t.Errorf("calls after the start: %q", got)
	}
}

// TestBoxUp_WaitsForASteadyStreak: a started box is waited for once, by
// wait.connection asking for three fresh logins in a row, and a box that
// never gives them ends the wait with wait.connection's own reason.
func TestBoxUp_WaitsForASteadyStreak(t *testing.T) {
	h := &fakeHost{state: "saved"}
	h.install(t)
	if started, err := testBox.Up(context.Background(), "t", &bytes.Buffer{}); !started || err != nil {
		t.Fatalf("a box that steadies: %v %v", started, err)
	}
	if got := h.calls[2:]; len(got) != 1 || got[0] != bootWaitCall {
		t.Errorf("calls after the start: %q", got)
	}

	flapping := errors.New("timed out after 10m0s: it answered 2 time(s) in a row at most, of the 3 asked for")
	h = &fakeHost{state: "saved", waits: []error{flapping}}
	h.install(t)
	if _, err := testBox.Up(context.Background(), "t", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "of the 3 asked for") ||
		!strings.Contains(err.Error(), "did not answer logins steadily within 10m0s") {
		t.Errorf("a box that never steadies: %v", err)
	}
}

// TestRunWithBox_AnInterruptedRunStillPutsItBack: the box is saved after
// an interrupt too, with a context of its own.
func TestRunWithBox_AnInterruptedRunStillPutsItBack(t *testing.T) {
	inRepo(t)
	h := &fakeHost{state: "poweroff"}
	h.install(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	leg := &fakeLeg{testErr: errors.New("interrupted; the tests on box-1 were stopped")}
	code := runWithBox(ctx, options(t, "a/b"), flow, leg, testBox, &bytes.Buffer{}, &bytes.Buffer{})
	if code != 2 || h.state != "saved" {
		t.Errorf("code %d, state %s, calls %q", code, h.state, h.calls)
	}
}

// TestRunWithBox_WhatGoesWrong: a box that cannot be read or started runs
// nothing and is left alone; one that started and never answered is put
// back; and a box that cannot be put back fails a run that passed.
func TestRunWithBox_WhatGoesWrong(t *testing.T) {
	inRepo(t)
	for method, tc := range map[string]struct {
		code    int
		putBack bool
		want    string
	}{
		"virt.vbox.vm.info":  {2, false, "reading the VM box-vm on vengeance"},
		"virt.vbox.vm.start": {2, false, "starting the VM box-vm on vengeance"},
		"wait.connection":    {2, true, "did not answer logins steadily within 10m0s"},
		"virt.vbox.vm.stop":  {2, true, "putting the VM box-vm back on vengeance"},
	} {
		h := &fakeHost{state: "saved", fail: map[string]error{method: errors.New("refused")}}
		h.install(t)
		leg := &fakeLeg{passed: true}
		var errOut bytes.Buffer
		code := runWithBox(context.Background(), options(t, "a/b"), flow, leg, testBox, &bytes.Buffer{}, &errOut)
		triedStop := strings.Contains(strings.Join(h.calls, "\n"), "virt.vbox.vm.stop")
		if code != tc.code || triedStop != tc.putBack || !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%s failing: code %d, calls %q\n%s", method, code, h.calls, errOut.String())
		}
		if method != "virt.vbox.vm.stop" && len(leg.steps) != 0 {
			t.Errorf("%s failing: the flow ran %v", method, leg.steps)
		}
	}
}

// TestRunWithBox_PrepareHandsTheBoxToTest: a prepare phase that started the
// box leaves it running and says so in its ready file; the test phase given
// that file puts it back when its tests end.
func TestRunWithBox_PrepareHandsTheBoxToTest(t *testing.T) {
	inRepo(t)
	ready := filepath.Join(t.TempDir(), "ready")
	h := &fakeHost{state: "saved"}
	h.install(t)
	if code := runWithBox(context.Background(), options(t, "-phase", "prepare", "-ready-file", ready, "a/b"), flow, &fakeLeg{passed: true}, testBox, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("prepare: %d", code)
	}
	if h.state != "running" || !startedByPrepare(ready) {
		data, _ := os.ReadFile(ready)
		t.Fatalf("after prepare the box is %s and the ready file says %q", h.state, data)
	}
	h.calls = nil
	if code := runWithBox(context.Background(), options(t, "-phase", "test", "-ready-file", ready, "a/b"), flow, &fakeLeg{passed: true}, testBox, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("test: %d", code)
	}
	if h.state != "saved" || len(h.calls) != 2 {
		t.Errorf("after test the box is %s, calls %q", h.state, h.calls)
	}
	if startedByPrepare("") || startedByPrepare(filepath.Join(t.TempDir(), "absent")) {
		t.Error("no ready file read as one saying the box was started")
	}
}

// TestAdhocTaskStats reads a task's stats, or its failure.
func TestAdhocTaskStats(t *testing.T) {
	stats, err := adhocTaskStats("virt.vbox.vm.info", []byte(`{"tasks":[{"status":"ok","stats":{"state":"saved"}}],"outcome":{"exit_code":0}}`))
	if err != nil || stats["state"] != "saved" {
		t.Errorf("%v, %v", stats, err)
	}
	if _, err := adhocTaskStats("virt.vbox.vm.start", []byte(`{"tasks":[{"status":"failed","error":"no VM named x"}],"outcome":{"exit_code":1}}`)); err == nil || !strings.Contains(err.Error(), "no VM named x") {
		t.Errorf("a failed task: %v", err)
	}
	if _, err := adhocTaskStats("x", []byte("not json")); err == nil {
		t.Error("no report was read as one")
	}
}
