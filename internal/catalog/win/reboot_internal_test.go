// Tests for win.reboot with its WinRM calls and clock replaced: a host
// that answers boot times in turn and a shutdown.exe that answers exit
// statuses in turn, on a clock that moves only when the method waits. The
// real host is the lab's win-check.
package win

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

var (
	boot0 = "2026-10-18T20:37:34.5000000Z"
	boot1 = "2026-10-18T21:02:11.2500000Z"
)

// rebootHost answers win.reboot's calls: each boot-time read takes the
// next of boots (a string is the answer, an error a failed try, and the
// last repeats), each shutdown.exe call the next of exits.
type rebootHost struct {
	boots    []any
	exits    []any
	reads    int
	commands []string
	sleeps   []time.Duration
	now      time.Time
	readCost time.Duration
}

func (h *rebootHost) install(t *testing.T) {
	t.Helper()
	oldRun, oldSleep, oldNow := rebootRun, rebootSleep, rebootNow
	t.Cleanup(func() { rebootRun, rebootSleep, rebootNow = oldRun, oldSleep, oldNow })
	h.now = time.Date(2026, 10, 18, 21, 0, 0, 0, time.UTC)
	rebootNow = func() time.Time { return h.now }
	rebootSleep = func(ctx context.Context, d time.Duration) error {
		h.sleeps = append(h.sleeps, d)
		h.now = h.now.Add(d)
		return ctx.Err()
	}
	rebootRun = func(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, shell winrmexec.Shell, script string, opts winrmexec.Options) (winrmexec.Result, error) {
		if shell == winrmexec.ShellPowerShell {
			if script != bootTimeScript {
				t.Fatalf("an unexpected script: %s", script)
			}
			if opts.Timeout <= 0 {
				t.Error("a boot-time read had no timeout")
			}
			h.now = h.now.Add(h.readCost)
			answer := h.boots[min(h.reads, len(h.boots)-1)]
			h.reads++
			if err, ok := answer.(error); ok {
				return winrmexec.Result{}, err
			}
			return winrmexec.Result{Stdout: answer.(string) + "\r\n"}, nil
		}
		h.commands = append(h.commands, script)
		answer := h.exits[min(len(h.commands)-1, len(h.exits)-1)]
		if err, ok := answer.(error); ok {
			return winrmexec.Result{}, err
		}
		return winrmexec.Result{ExitCode: answer.(int), Stderr: "shutdown said no"}, nil
	}
}

func runReboot(t *testing.T, params map[string]any) (*ctxStub, error) {
	t.Helper()
	rc := &ctxStub{stats: map[string]any{}}
	_, err := Reboot(context.Background(), rc, device(), params)
	return rc, err
}

// TestReboot_RestartsAndWaitsForANewBootTime: the restart is asked for as
// planned maintenance with the delay and message, the old boot time and a
// host that is down are both waited through, and the new boot time ends it.
func TestReboot_RestartsAndWaitsForANewBootTime(t *testing.T) {
	h := &rebootHost{boots: []any{boot0, boot0, errors.New("connection refused"), boot1}, exits: []any{0}}
	h.install(t)
	rc, err := runReboot(t, map[string]any{"post_reboot_delay": 30})
	if err != nil {
		t.Fatal(err)
	}
	want := `C:\Windows\System32\shutdown.exe /r /f /t 2 /d p:4:1 /c "Reboot initiated by The Pleiades"`
	if len(h.commands) != 1 || h.commands[0] != want {
		t.Errorf("commands %q, want %q", h.commands, want)
	}
	if fmt.Sprint(h.sleeps) != "[2s 5s 5s 30s]" {
		t.Errorf("sleeps %v: the delay, two waits between tries, then the post delay", h.sleeps)
	}
	if rc.stats["rebooted"] != true || rc.stats["previous_boot_time"] != "2026-10-18T20:37:34Z" || rc.stats["boot_time"] != "2026-10-18T21:02:11Z" || rc.stats["elapsed"] != 12 {
		t.Errorf("stats %v", rc.stats)
	}
}

// TestReboot_ReplacesAScheduledRestart: a restart already scheduled is
// cancelled and this one asked for again.
func TestReboot_ReplacesAScheduledRestart(t *testing.T) {
	h := &rebootHost{boots: []any{boot0, boot1}, exits: []any{errShutdownScheduled, 0, 0}}
	h.install(t)
	if _, err := runReboot(t, map[string]any{"msg": "", "pre_reboot_delay": 0}); err != nil {
		t.Fatal(err)
	}
	restart := `C:\Windows\System32\shutdown.exe /r /f /t 0 /d p:4:1`
	if fmt.Sprint(h.commands) != fmt.Sprint([]string{restart, `C:\Windows\System32\shutdown.exe /a`, restart}) {
		t.Errorf("commands %q", h.commands)
	}
}

// TestReboot_AConnectionLostAfterSendingStillWaits: a host going down
// under the request looks like a failed call; the restart may have been
// sent, so it is waited for. A request that never started fails at once.
func TestReboot_AConnectionLostAfterSendingStillWaits(t *testing.T) {
	h := &rebootHost{boots: []any{boot0, boot1}, exits: []any{errors.New("connection reset")}}
	h.install(t)
	if _, err := runReboot(t, nil); err != nil {
		t.Errorf("a dropped connection after sending: %v", err)
	}
	h = &rebootHost{boots: []any{boot0, boot1}, exits: []any{&winrmexec.NotStartedError{Err: errors.New("no shell")}}}
	h.install(t)
	if _, err := runReboot(t, nil); err == nil || !strings.Contains(err.Error(), "no shell") || h.reads != 1 {
		t.Errorf("a request that never started: %v, reads %d", err, h.reads)
	}
}

// TestReboot_GivesUpAtItsTimeout, naming why: a host that kept its boot
// time, or the last try's failure.
func TestReboot_GivesUpAtItsTimeout(t *testing.T) {
	h := &rebootHost{boots: []any{boot0}, exits: []any{0}, readCost: time.Second}
	h.install(t)
	_, err := runReboot(t, map[string]any{"reboot_timeout": 60})
	if err == nil || !strings.Contains(err.Error(), "did not come back restarted within 1m0s (it kept answering with the boot time it had)") {
		t.Errorf("%v", err)
	}
	if !h.now.Before(time.Date(2026, 10, 18, 21, 1, 7, 0, time.UTC)) {
		t.Errorf("it waited until %s, past its minute", h.now)
	}
	h = &rebootHost{boots: []any{boot0, errors.New("i/o timeout")}, exits: []any{0}}
	h.install(t)
	if _, err := runReboot(t, map[string]any{"reboot_timeout": 30}); err == nil || !strings.Contains(err.Error(), "the last try answered: i/o timeout") {
		t.Errorf("%v", err)
	}
}

// TestReboot_RefusedRestartIsAnError: shutdown.exe refusing is the
// failure, with its words, and nothing is waited for.
func TestReboot_RefusedRestartIsAnError(t *testing.T) {
	h := &rebootHost{boots: []any{boot0}, exits: []any{5}}
	h.install(t)
	if _, err := runReboot(t, nil); err == nil || !strings.Contains(err.Error(), `shutdown.exe exited 5: "shutdown said no`) || len(h.sleeps) != 0 {
		t.Errorf("%v, sleeps %v", err, h.sleeps)
	}
	h = &rebootHost{boots: []any{boot0}, exits: []any{errShutdownScheduled, errors.New("abort failed")}}
	h.install(t)
	if _, err := runReboot(t, nil); err == nil || !strings.Contains(err.Error(), "cancelling the restart already scheduled") {
		t.Errorf("a failed cancel: %v", err)
	}
	h = &rebootHost{boots: []any{boot0}, exits: []any{errShutdownScheduled, 0, errShutdownScheduled}}
	h.install(t)
	if _, err := runReboot(t, nil); err == nil || !strings.Contains(err.Error(), "exited 1190") {
		t.Errorf("a restart scheduled again: %v", err)
	}
}

// TestReboot_CheckSendsNothing: a check reads the boot time, reports the
// change a run makes, and asks for no restart.
func TestReboot_CheckSendsNothing(t *testing.T) {
	h := &rebootHost{boots: []any{boot0}, exits: []any{0}}
	h.install(t)
	rc := &ctxStub{stats: map[string]any{}}
	res, err := reboot(context.Background(), rc, device(), nil, true)
	if err != nil || !res.Changed || len(h.commands) != 0 || rc.stats["rebooted"] != false || rc.stats["previous_boot_time"] == nil {
		t.Errorf("%+v %v, commands %v, stats %v", res, err, h.commands, rc.stats)
	}
}

// TestReboot_Refusals: each mistake in the task, or in what the host
// answers before the restart, fails naming the method, before any restart.
func TestReboot_Refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		boots  []any
		want   string
	}{
		"zero timeout":      {map[string]any{"reboot_timeout": 0}, []any{boot0}, "reboot_timeout must be at least 1"},
		"long delay":        {map[string]any{"pre_reboot_delay": 601}, []any{boot0}, "pre_reboot_delay must be from 0 to 600"},
		"negative post":     {map[string]any{"post_reboot_delay": -1}, []any{boot0}, "post_reboot_delay must be at least 0"},
		"zero connect":      {map[string]any{"connect_timeout": 0}, []any{boot0}, "connect_timeout must be at least 1"},
		"text timeout":      {map[string]any{"reboot_timeout": "60"}, []any{boot0}, "not a whole number"},
		"long message":      {map[string]any{"msg": strings.Repeat("m", 513)}, []any{boot0}, "at most 512 characters"},
		"two-line message":  {map[string]any{"msg": "a\nb"}, []any{boot0}, "on one line"},
		"numeric message":   {map[string]any{"msg": 5}, []any{boot0}, "not text"},
		"control character": {map[string]any{"msg": "a\x01b"}, []any{boot0}, "U+0001"},
		"unreadable boot":   {nil, []any{errors.New("access denied")}, "access denied"},
		"not a time":        {nil, []any{"yesterday"}, `the boot time "yesterday" is not an RFC 3339 time`},
	} {
		h := &rebootHost{boots: tc.boots, exits: []any{0}}
		h.install(t)
		_, err := runReboot(t, tc.params)
		if err == nil || !strings.HasPrefix(err.Error(), "win.reboot: ") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
		if len(h.commands) != 0 {
			t.Errorf("%s: a restart was asked for", name)
		}
	}
	h := &rebootHost{boots: []any{boot0}, exits: []any{0}}
	h.install(t)
	if _, err := Reboot(context.Background(), &ctxStub{stats: map[string]any{}}, &inventorytest.Stub{StubName: "bare"}, nil); err == nil || len(h.commands) != 0 {
		t.Errorf("a device with no WinRM: %v", err)
	}
}

// TestReboot_AFailedBootReadExitIsAnError: a boot-time script that exits
// non-zero is a failure with its words.
func TestReboot_AFailedBootReadExitIsAnError(t *testing.T) {
	old := rebootRun
	t.Cleanup(func() { rebootRun = old })
	rebootRun = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{ExitCode: 1, Stderr: "Get-CimInstance : Access denied"}, nil
	}
	if _, err := runReboot(t, nil); err == nil || !strings.Contains(err.Error(), `exited 1: "Get-CimInstance : Access denied"`) {
		t.Errorf("%v", err)
	}
}

// TestReboot_StatAndContextFailures: a run whose answer cannot be recorded
// fails, and so does one whose context ends while it waits.
func TestReboot_StatAndContextFailures(t *testing.T) {
	h := &rebootHost{boots: []any{boot0, boot1}, exits: []any{0}}
	h.install(t)
	if _, err := Reboot(context.Background(), failingStats{}, device(), nil); err == nil {
		t.Error("a run that could not record its answer succeeded")
	}
	h = &rebootHost{boots: []any{boot0}, exits: []any{0}}
	h.install(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Reboot(ctx, &ctxStub{stats: map[string]any{}}, device(), nil); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled run: %v", err)
	}
}

// failingStats refuses every stat.
type failingStats struct{}

func (failingStats) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "pw"}
}
func (failingStats) SetStat(string, any) error  { return errors.New("no room") }
func (failingStats) EmitFact(string, any) error { return errors.New("no room") }

// TestReboot_Registration: implemented, checkable, elevated, and not
// reversible, saying why.
func TestReboot_Registration(t *testing.T) {
	d, ok := collection.Lookup(fqcnReboot)
	if !ok {
		t.Fatal("win.reboot is not registered")
	}
	m := d.Manifest
	if m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || !m.ExecutionContext.RequiresElevation ||
		m.Reversibility.Reversible || m.Reversibility.Notes == "" {
		t.Errorf("%+v", m)
	}
}

// TestBootTimeScript_FitsTheCommandLine.
func TestBootTimeScript_FitsTheCommandLine(t *testing.T) {
	if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, bootTimeScript, winrmexec.Options{}); err != nil {
		t.Fatal(err)
	}
}
