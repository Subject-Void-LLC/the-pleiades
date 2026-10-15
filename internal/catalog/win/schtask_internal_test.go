// Tests for win.schtask with its WinRM call replaced by a recording
// stand-in that answers as the script does, and the script itself parsed
// by PowerShell's own parser where one is installed.
package win

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os/exec"
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
}

func (f *fake) install(t *testing.T) {
	t.Helper()
	old := run
	t.Cleanup(func() { run = old })
	run = func(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, stdin string, _ winrmexec.Options) (winrmexec.Result, error) {
		var in map[string]any
		if err := json.Unmarshal([]byte(stdin), &in); err != nil {
			t.Fatalf("stdin is not JSON: %v", err)
		}
		f.calls = append(f.calls, in)
		switch in["op"] {
		case "register":
			f.task = &described{Exists: true, XML: "<Task>new</Task>", Enabled: in["enabled"].(bool), Execute: in["execute"].(string),
				Arguments: in["arguments"].(string), Description: in["description"].(string), Actions: 1, Triggers: 1,
				User: in["user"].(string), RunLevel: in["run_level"].(string)}
			f.task.Trigger.Kind = map[string]string{"daily": "MSFT_TaskDailyTrigger", "at_startup": "MSFT_TaskBootTrigger"}[in["trigger"].(string)]
			f.task.Trigger.Start = "2026-10-15T" + in["at"].(string) + ":00"
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
	var scripts []string
	inner := run
	run = func(ctx context.Context, tg winrmexec.Target, a winrmexec.Auth, stdin string, o winrmexec.Options) (winrmexec.Result, error) {
		scripts = append(scripts, schtaskScript)
		return inner(ctx, tg, a, stdin, o)
	}
	hostile := `x'; Remove-Item -Recurse C:\ #` + "\u2019" + `$(whoami)`
	if _, err := Schtask(context.Background(), &ctxStub{stats: map[string]any{}}, device(), params(map[string]any{"arguments": hostile, "description": hostile})); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	if last["arguments"] != hostile || last["description"] != hostile {
		t.Errorf("stdin carried %q", last["arguments"])
	}
	if strings.Contains(schtaskScript, "whoami") {
		t.Error("a value reached the script")
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
// no error, where a PowerShell is installed (pwsh, or Windows PowerShell
// through WSL). Nothing is run: the script is only parsed.
func TestSchtaskScript_Parses(t *testing.T) {
	shell := ""
	for _, candidate := range []string{"pwsh", "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"} {
		if p, err := exec.LookPath(candidate); err == nil {
			shell = p
			break
		}
	}
	if shell == "" {
		t.Skip("needs powershell: no pwsh or Windows PowerShell to parse the script with")
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
