// Tests for win.schtask with its WinRM call replaced by a recording
// stand-in that answers as the script does, and the script itself parsed
// by PowerShell's own parser where one is installed.
package win

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// host is a WinRM device.
type host struct{ *inventorytest.Stub }

func (host) WinRMHost() string { return "win1.example.test" }
func (host) WinRMPort() int    { return 5986 }

func device() inventory.InventoryItem {
	return host{&inventorytest.Stub{StubName: "win1", StubState: inventory.StateActive, Caps: []capability.Name{capability.NameWinRM}}}
}

type ctxStub struct{ stats map[string]any }

func (c *ctxStub) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "pw"}
}
func (c *ctxStub) SetStat(k string, v any) error  { c.stats[k] = v; return nil }
func (c *ctxStub) EmitFact(k string, v any) error { return c.SetStat(k, v) }

// fake answers each op with the task held, recording every call.
type fake struct {
	task  *described
	calls []map[string]any
	// scripts and stdins are what each call sent, as sent.
	scripts, stdins []string
}

func (f *fake) install(t *testing.T) {
	t.Helper()
	old := run
	t.Cleanup(func() { run = old })
	run = func(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, script, stdin string, _ winrmexec.Options) (winrmexec.Result, error) {
		f.scripts = append(f.scripts, script)
		f.stdins = append(f.stdins, stdin)
		var in map[string]any
		if err := json.Unmarshal([]byte(stdin), &in); err != nil {
			t.Fatalf("stdin is not JSON: %v", err)
		}
		f.calls = append(f.calls, in)
		switch in["op"] {
		case "register":
			f.task = &described{Exists: true, XML: "<Task>new</Task>", Enabled: in["enabled"].(bool), Execute: in["execute"].(string),
				Arguments: in["arguments"].(string), Description: in["description"].(string), Actions: 1, Triggers: 1,
				User: in["user"].(string), RunLevel: in["run_level"].(string), LogonType: logonTypeOf(in["logon"].(string))}
			f.task.Trigger.Kind = map[string]string{"daily": "MSFT_TaskDailyTrigger", "at_startup": "MSFT_TaskBootTrigger"}[in["trigger"].(string)]
			f.task.Trigger.Start = "2026-10-15T" + in["at"].(string) + ":00"
			f.task.Trigger.Days = "1"
		case "remove":
			f.task = nil
		case "restore":
			f.task = &described{Exists: true, XML: in["xml"].(string)}
		}
		d := described{}
		if f.task != nil {
			d = *f.task
		}
		out, _ := json.Marshal(d)
		return winrmexec.Result{Stdout: string(out)}, nil
	}
}

func params(extra map[string]any) map[string]any {
	p := map[string]any{"name": `\Pleiades\Cleanup`, "execute": `C:\ops\cleanup.exe`, "arguments": "-q", "trigger": "daily", "at": "02:30"}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// TestSchtask_RegistersOnceAndUndoes: a check reports and changes
// nothing; the run registers and records a removal; a second run is no
// change; a change records a restore of the earlier XML; absent removes.
func TestSchtask_RegistersOnceAndUndoes(t *testing.T) {
	f := &fake{}
	f.install(t)
	rc := &ctxStub{stats: map[string]any{}}
	if res, err := schtask(context.Background(), rc, device(), params(nil), true); err != nil || !res.Changed || len(f.calls) != 1 {
		t.Fatalf("check: %+v %v, %d calls", res, err, len(f.calls))
	}
	rc = &ctxStub{stats: map[string]any{}}
	if res, err := Schtask(context.Background(), rc, device(), params(nil)); err != nil || !res.Changed {
		t.Fatalf("run: %+v %v", res, err)
	}
	if inv, _ := rc.stats[sdk.StatInverse].(map[string]any); inv["params"].(map[string]any)["state"] != "absent" {
		t.Errorf("undo %v", inv)
	}
	if res, err := Schtask(context.Background(), &ctxStub{stats: map[string]any{}}, device(), params(nil)); err != nil || res.Changed {
		t.Errorf("again: %+v %v", res, err)
	}
	rc = &ctxStub{stats: map[string]any{}}
	if res, err := Schtask(context.Background(), rc, device(), params(map[string]any{"at": "03:00"})); err != nil || !res.Changed {
		t.Fatalf("change: %+v %v", res, err)
	}
	if d, _ := rc.stats["differences"].([]any); len(d) != 1 || !strings.Contains(d[0].(string), "02:30") {
		t.Errorf("differences %v", rc.stats["differences"])
	}
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	if p := inv["params"].(map[string]any); p["state"] != "present" || p["xml"] != "<Task>new</Task>" {
		t.Errorf("undo %v", inv)
	}
	if res, err := Schtask(context.Background(), &ctxStub{stats: map[string]any{}}, device(), map[string]any{"name": `\Pleiades\Cleanup`, "state": "absent"}); err != nil || !res.Changed || f.task != nil {
		t.Errorf("absent: %+v %v", res, err)
	}
}

// TestSchtask_ValuesNeverBecomeScript: hostile values reach the device as
// JSON on standard input, exactly as given, and the script sent is always
// the constant.
func TestSchtask_ValuesNeverBecomeScript(t *testing.T) {
	f := &fake{}
	f.install(t)
	hostile := `x'; Remove-Item -Recurse C:\ #` + "\u2019 Jos\u00e9 \U0001F600" + `$(whoami)`
	if _, err := Schtask(context.Background(), &ctxStub{stats: map[string]any{}}, device(), params(map[string]any{"arguments": hostile, "description": hostile})); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	if last["arguments"] != hostile || last["description"] != hostile {
		t.Errorf("stdin carried %q", last["arguments"])
	}
	for i, script := range f.scripts {
		if script != schtaskScript || strings.Contains(script, "whoami") {
			t.Errorf("call %d sent a script other than the constant:\n%s", i, script)
		}
	}
	// Standard input is ASCII whatever the values hold, so the device's
	// console code page cannot change them on the way.
	for i, in := range f.stdins {
		for _, r := range in {
			if r > 0x7f {
				t.Errorf("call %d's standard input holds %q, past ASCII", i, r)
				break
			}
		}
	}
}

// TestSchtask_DifferencesItOnceMissed: a task matching every compared field
// but run as an account of another domain, with a stored-password logon, on
// every third day, or with a once trigger at another instant, differs.
func TestSchtask_DifferencesItOnceMissed(t *testing.T) {
	s, err := parse(params(map[string]any{"run_as": `CORP\svc`}))
	if err != nil {
		t.Fatal(err)
	}
	base := described{Exists: true, Actions: 1, Triggers: 1, Execute: s.execute, Arguments: s.arguments, User: `CORP\svc`, RunLevel: s.runLevel, Enabled: s.enabled, LogonType: "S4U"}
	base.Trigger.Kind, base.Trigger.Start, base.Trigger.Days = "MSFT_TaskDailyTrigger", "2026-10-15T02:30:00", "1"
	if d := base.differences(s); len(d) != 0 {
		t.Fatalf("the matching task differs: %v", d)
	}
	for want, change := range map[string]func(*described){
		"runs as":      func(d *described) { d.User = `PC1\svc` },
		"logs on as":   func(d *described) { d.LogonType = "Password" },
		"every 3 days": func(d *described) { d.Trigger.Days = "3" },
	} {
		d := base
		change(&d)
		if diffs := d.differences(s); len(diffs) != 1 || !strings.Contains(diffs[0], want) {
			t.Errorf("%s: %v", want, diffs)
		}
	}
	once, err := parse(params(map[string]any{"run_as": `CORP\svc`, "trigger": "once", "at": "2026-11-01T10:00:00Z"}))
	if err != nil {
		t.Fatal(err)
	}
	at := base
	at.Trigger.Kind, at.Trigger.Start, at.Trigger.StartUTC = "MSFT_TaskTimeTrigger", "2026-11-01T02:00:00", "2026-11-01T10:00:00.0000000+00:00"
	if d := at.differences(once); len(d) != 0 {
		t.Errorf("a once trigger at the same instant in the device's zone differs: %v", d)
	}
	at.Trigger.StartUTC = "2026-11-01T02:00:00.0000000+00:00"
	if d := at.differences(once); len(d) != 1 || !strings.Contains(d[0], "starts at") {
		t.Errorf("a once trigger at another instant: %v", d)
	}
}

// TestSchtask_Refuses: names, times and values outside their forms are
// refused before WinRM is reached.
func TestSchtask_Refuses(t *testing.T) {
	f := &fake{}
	f.install(t)
	for name, p := range map[string]map[string]any{
		"a name with a quote":      params(map[string]any{"name": `\Pleiades\a'b`}),
		"an empty folder":          params(map[string]any{"name": `\\Cleanup`}),
		"no trigger":               params(map[string]any{"trigger": nil}),
		"a daily trigger, no time": params(map[string]any{"at": "2:30"}),
		"a once, not a time":       params(map[string]any{"trigger": "once", "at": "tomorrow"}),
		"at on a startup trigger":  params(map[string]any{"trigger": "at_startup", "at": "02:30"}),
		"a line break":             params(map[string]any{"arguments": "a\nb"}),
		"another state":            params(map[string]any{"state": "enabled"}),
		"a run level":              params(map[string]any{"run_level": "admin"}),
		"an account":               params(map[string]any{"run_as": "a;b"}),
		"no program":               {"name": `\P\x`, "trigger": "at_startup"},
	} {
		if _, err := Schtask(context.Background(), &ctxStub{stats: map[string]any{}}, device(), p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("WinRM was reached %d times", len(f.calls))
	}
}

// TestSchtaskScript_Parses: PowerShell's own parser reads the script with
// no error. Nothing is run: the script is only parsed. It parses on a
// Windows machine with that machine's own Windows PowerShell, which is
// where the script runs (make win-check runs this package on the lab's
// win-check), or with a pwsh installed on this machine. It never starts a
// Windows program through WSL: that is the workstation the Linux side runs
// on, which a test has no business reaching (FAILURE_PATTERNS 508).
func TestSchtaskScript_Parses(t *testing.T) {
	shell := powerShellHere()
	if shell == "" {
		t.Skip("needs powershell: none native to this machine (Windows PowerShell on Windows, or a pwsh that is not a Windows host's program reached through WSL)")
	}
	parse := `$s = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('` + base64.StdEncoding.EncodeToString([]byte(schtaskScript)) + `'))
$e = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($s, [ref]$null, [ref]$e)
"errors=" + $e.Count; $e | ForEach-Object { $_.Message }`
	units := utf16.Encode([]rune(parse))
	raw := make([]byte, 2*len(units))
	for i, u := range units {
		raw[2*i], raw[2*i+1] = byte(u), byte(u>>8)
	}
	out, err := exec.Command(shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "errors=0") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// powerShellHere is a PowerShell native to the machine the test runs on:
// Windows PowerShell on Windows, else a pwsh on the PATH that is not a
// Windows program. WSL appends the Windows host's PATH to its own, so a
// lookup can resolve to the host's programs, which run on the workstation
// outside this machine; one that ends in .exe once its links are followed
// is refused.
func powerShellHere() string {
	if runtime.GOOS == "windows" {
		if _, err := os.Stat(winrmexec.DefaultPowerShellPath); err == nil {
			return winrmexec.DefaultPowerShellPath
		}
		return ""
	}
	p, err := exec.LookPath("pwsh")
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(p); err != nil || strings.HasSuffix(strings.ToLower(real), ".exe") {
		return ""
	}
	return p
}

// TestSchtaskScript_FitsTheCommandLine: the script, as powershell.exe
// -EncodedCommand carries it, fits the 8191 characters the WinRM service's
// cmd.exe accepts. The other tests replace the WinRM call, so none of them
// built the line, and the script came to 9541 characters: every call was
// refused before it reached a real host.
func TestSchtaskScript_FitsTheCommandLine(t *testing.T) {
	if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, schtaskScript, winrmexec.Options{}); err != nil {
		t.Fatal(err)
	}
}
