// Tests for what Host sends and how it reads each answer, through a
// recording runner fed output captured from VirtualBox 7.2.20 on
// Windows 11 (testdata).
package vboxmanage

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// recorded is a runner that records each call and answers with a
// captured fixture's stdout, stderr and exit code.
type recorded struct {
	calls   [][]string
	fixture string
	t       *testing.T
}

func (r *recorded) Run(_ context.Context, program string, args []string) (Output, error) {
	r.calls = append(r.calls, append([]string{program}, args...))
	if r.fixture == "" {
		return Output{}, nil
	}
	code, err := strconv.Atoi(fixture(r.t, r.fixture+".exit"))
	if err != nil {
		r.t.Fatal(err)
	}
	return Output{Stdout: fixture(r.t, r.fixture+".stdout"), Stderr: fixture(r.t, r.fixture+".stderr"), ExitCode: code}, nil
}

// PowerShell records the script as a call to "powershell".
func (r *recorded) PowerShell(_ context.Context, script, stdin string) (Output, error) {
	r.calls = append(r.calls, []string{"powershell", script, stdin})
	return Output{}, nil
}

const vbox = `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`

// hostWith returns a Host whose runner answers with fixture.
func hostWith(t *testing.T, fixture string) (Host, *recorded) {
	r := &recorded{fixture: fixture, t: t}
	return Host{Runner: r, Path: vbox}, r
}

func TestHost_SendsExactlyTheseArguments(t *testing.T) {
	ctx := context.Background()
	for name, tt := range map[string]struct {
		call func(Host) error
		want string
	}{
		"machine":      {func(h Host) error { _, err := h.Machine(ctx, "vm1"); return err }, "showvminfo vm1 --machinereadable"},
		"list":         {func(h Host) error { _, err := h.List(ctx); return err }, "list vms"},
		"running":      {func(h Host) error { _, err := h.Running(ctx); return err }, "list runningvms"},
		"start":        {func(h Host) error { return h.Start(ctx, "vm1") }, "startvm vm1 --type headless"},
		"poweroff":     {func(h Host) error { return h.PowerOff(ctx, "vm1") }, "controlvm vm1 poweroff"},
		"acpi":         {func(h Host) error { return h.ACPIShutdown(ctx, "vm1") }, "controlvm vm1 acpipowerbutton"},
		"autostart":    {func(h Host) error { return h.SetAutostart(ctx, "vm1", true) }, "modifyvm vm1 --autostart-enabled on --autostart-delay 0"},
		"no autostart": {func(h Host) error { return h.SetAutostart(ctx, "vm1", false) }, "modifyvm vm1 --autostart-enabled off --autostart-delay 0"},
		"restore": {func(h Host) error { return h.RestoreSnapshot(ctx, "vm1", "5732a952-0b85-4e3a-820a-1a568f31166b") },
			"snapshot vm1 restore 5732a952-0b85-4e3a-820a-1a568f31166b"},
		"delete": {func(h Host) error { return h.DeleteSnapshot(ctx, "vm1", "5732a952-0b85-4e3a-820a-1a568f31166b") },
			"snapshot vm1 delete 5732a952-0b85-4e3a-820a-1a568f31166b"},
	} {
		h, r := hostWith(t, "")
		_ = tt.call(h)
		if len(r.calls) != 1 || r.calls[0][0] != vbox || strings.Join(r.calls[0][1:], " ") != tt.want {
			t.Errorf("%s: calls = %q, want %q", name, r.calls, tt.want)
		}
	}
}

func TestHost_ReadsCapturedAnswers(t *testing.T) {
	ctx := context.Background()

	h, _ := hostWith(t, "list-vms")
	refs, err := h.List(ctx)
	if err != nil || len(refs) != 1 || refs[0] != (Ref{Name: "pleiades-probe", UUID: "9883f6c3-b17d-4077-aa52-d9c49a6912a5"}) {
		t.Errorf("List = %+v, %v", refs, err)
	}
	h, _ = hostWith(t, "list-runningvms-none")
	if refs, err := h.Running(ctx); err != nil || len(refs) != 0 {
		t.Errorf("Running with none = %+v, %v", refs, err)
	}

	h, r := hostWith(t, "snapshot-take")
	uuid, err := h.TakeSnapshot(ctx, "pleiades-probe", "fixture-snap", "fixture")
	if err != nil || uuid != "5732a952-0b85-4e3a-820a-1a568f31166b" {
		t.Errorf("TakeSnapshot = %q, %v", uuid, err)
	}
	if got := strings.Join(r.calls[0][1:], " "); got != "snapshot pleiades-probe take fixture-snap --description fixture" {
		t.Errorf("take sent %q", got)
	}
}

func TestHost_ClassifiesCapturedFailures(t *testing.T) {
	ctx := context.Background()

	h, _ := hostWith(t, "showvminfo-missing")
	_, err := h.Machine(ctx, "no-such-vm")
	if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "Could not find a registered machine named 'no-such-vm'") {
		t.Errorf("missing machine: err = %v, want ErrNotFound quoting VBoxManage", err)
	}
	h, _ = hostWith(t, "controlvm-poweroff-not-running")
	err = h.PowerOff(ctx, "pleiades-probe")
	if !errors.Is(err, ErrNotRunning) || errors.Is(err, ErrNotFound) {
		t.Errorf("not running: err = %v, want ErrNotRunning", err)
	}
	// A failure that is neither is reported, and is neither.
	h, _ = hostWith(t, "snapshot-list-none")
	_, err = h.run(ctx, "snapshot", "pleiades-probe", "list", "--machinereadable")
	var vboxErr *Error
	if !errors.As(err, &vboxErr) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotRunning) ||
		!strings.Contains(err.Error(), "does not have any snapshots") {
		t.Errorf("other failure: err = %v", err)
	}
}

func TestHost_RefusesBeforeSending(t *testing.T) {
	ctx := context.Background()
	for name, call := range map[string]func(Host) error{
		"a name with a space":    func(h Host) error { return h.Start(ctx, "my vm") },
		"a name with a quote":    func(h Host) error { _, err := h.Machine(ctx, `vm"`); return err },
		"a leading dash":         func(h Host) error { return h.PowerOff(ctx, "--help") },
		"a snapshot name":        func(h Host) error { _, err := h.TakeSnapshot(ctx, "vm1", "a&b", ""); return err },
		"a description line":     func(h Host) error { _, err := h.TakeSnapshot(ctx, "vm1", "s1", "one\ntwo"); return err },
		"a long description":     func(h Host) error { _, err := h.TakeSnapshot(ctx, "vm1", "s1", strings.Repeat("x", 201)); return err },
		"a UUID that is not one": func(h Host) error { return h.DeleteSnapshot(ctx, "vm1", "fixture-snap") },
		"an empty name":          func(h Host) error { return h.Start(ctx, "") },
		"a name too long":        func(h Host) error { return h.Start(ctx, strings.Repeat("a", 64)) },
	} {
		h, r := hostWith(t, "")
		if err := call(h); err == nil || len(r.calls) != 0 {
			t.Errorf("%s: err = %v, calls = %d; want a refusal with nothing sent", name, err, len(r.calls))
		}
	}
	if err := CheckName("VM", "ubuntu-24.04_lab"); err != nil {
		t.Errorf("an ordinary name was refused: %v", err)
	}
}
