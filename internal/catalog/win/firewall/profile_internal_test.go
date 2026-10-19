// Tests for win.firewall.profile with its WinRM call replaced by a
// stand-in that keeps the three profiles and answers as the script does:
// each setting as the machine keeps it and as it is in force.
package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// profiles is the stand-in: each profile's settings as kept, and any a
// Group Policy holds in force over them.
type profiles struct {
	kept   map[string]map[string]string
	policy map[string]map[string]string
	calls  []map[string]any
	drop   bool
}

// stock is a profile as Windows Server ships it: on, block inbound, allow
// outbound, inbound left NotConfigured, no logging.
func stock() map[string]string {
	return map[string]string{pfEnabled: "True", pfInbound: "NotConfigured", pfOutbound: "Allow", pfLogAllowed: "False",
		pfLogBlocked: "False", pfLogFile: `%systemroot%\system32\LogFiles\Firewall\pfirewall.log`, pfLogMaxSizeK: "4096"}
}

func (p *profiles) install(t *testing.T) {
	t.Helper()
	old := run
	t.Cleanup(func() { run = old })
	run = func(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, script, stdin string, _ winrmexec.Options) (winrmexec.Result, error) {
		if script != profileScript {
			t.Fatalf("a script other than the constant was sent")
		}
		var in map[string]any
		if err := json.Unmarshal([]byte(stdin), &in); err != nil {
			t.Fatalf("stdin is not JSON: %v", err)
		}
		p.calls = append(p.calls, in)
		name := in["name"].(string)
		if set, ok := in["set"].(map[string]any); ok && in["op"] == "set" && !p.drop {
			for k, v := range set {
				p.kept[name][k] = v.(string)
			}
		}
		active := map[string]string{}
		for k, v := range p.kept[name] {
			active[k] = v
		}
		for k, v := range p.policy[name] {
			active[k] = v
		}
		out, _ := json.Marshal(profileAsIs{Persistent: p.kept[name], Active: active})
		return winrmexec.Result{Stdout: string(out)}, nil
	}
}

func pfRun(t *testing.T, check bool, params map[string]any) (*ctxStub, bool, error) {
	t.Helper()
	rc := &ctxStub{stats: map[string]any{}}
	res, err := firewallProfile(context.Background(), rc, device(), params, check)
	return rc, res.Changed, err
}

// TestProfile_SetsOnlyWhatItIsGivenAndPutsItBack: NotConfigured compares as
// what it does, only the settings that differ are sent, a second run is no
// change, and the undo puts the earlier values back.
func TestProfile_SetsOnlyWhatItIsGivenAndPutsItBack(t *testing.T) {
	p := &profiles{kept: map[string]map[string]string{"domain": stock()}}
	p.install(t)
	call := map[string]any{"name": "Domain", "default_inbound_action": "block", "log_blocked": true, "log_max_size_kb": 16384,
		"log_file": `%SystemRoot%\System32\LogFiles\Firewall\pfirewall.log`}
	rc, changed, err := pfRun(t, true, call)
	if err != nil || !changed || len(p.calls) != 1 {
		t.Fatalf("check: %v %v, %d calls", changed, err, len(p.calls))
	}
	if d := rc.stats["differences"].([]any); len(d) != 2 {
		t.Errorf("differences %v, want log_blocked and log_max_size_kb", d)
	}
	if o := rc.stats["overridden"].([]any); len(o) != 0 {
		t.Errorf("settings not yet changed were called overridden: %v", o)
	}
	rc, changed, err = pfRun(t, false, call)
	if err != nil || !changed {
		t.Fatalf("run: %v %v", changed, err)
	}
	if set := p.calls[len(p.calls)-1]["set"].(map[string]any); !reflect.DeepEqual(set, map[string]any{pfLogBlocked: "True", pfLogMaxSizeK: "16384"}) {
		t.Errorf("sent %v", set)
	}
	inv := rc.stats[sdk.StatInverse].(map[string]any)["params"].(map[string]any)
	if !reflect.DeepEqual(inv, map[string]any{"name": "domain", pfLogBlocked: false, pfLogMaxSizeK: 4096}) {
		t.Errorf("undo %v", inv)
	}
	if _, changed, err := pfRun(t, false, call); err != nil || changed {
		t.Errorf("again: %v %v", changed, err)
	}
	if _, changed, err := pfRun(t, false, inv); err != nil || !changed || p.kept["domain"][pfLogBlocked] != "False" || p.kept["domain"][pfLogMaxSizeK] != "4096" {
		t.Errorf("the undo: %v %v, %v", changed, err, p.kept["domain"])
	}
	if _, changed, err := pfRun(t, false, map[string]any{"name": "domain"}); err != nil || changed {
		t.Errorf("a report: %v %v", changed, err)
	}
}

// TestProfile_GroupPolicyAndAHostThatDoesNotApply: a setting Group Policy
// holds at another value fails the task, after the change when one was
// made, and with nothing sent when none was; a host that answers without
// applying the change fails it too.
func TestProfile_GroupPolicyAndAHostThatDoesNotApply(t *testing.T) {
	p := &profiles{kept: map[string]map[string]string{"public": stock()}, policy: map[string]map[string]string{"public": {pfEnabled: "False", pfLogBlocked: "False"}}}
	p.install(t)
	if _, _, err := pfRun(t, false, map[string]any{"name": "public", "enabled": true}); err == nil || !strings.Contains(err.Error(), "Group Policy sets enabled") {
		t.Errorf("a setting policy holds: %v", err)
	}
	if len(p.calls) != 1 {
		t.Errorf("a refused call sent %d calls", len(p.calls))
	}
	rc, _, err := pfRun(t, false, map[string]any{"name": "public", "log_blocked": true})
	if err == nil || !strings.Contains(err.Error(), "the change was made") || rc.stats[sdk.StatInverse] == nil {
		t.Errorf("a change policy overrides: %v, undo %v", err, rc.stats[sdk.StatInverse])
	}
	p.policy, p.drop = nil, true
	if _, _, err := pfRun(t, false, map[string]any{"name": "public", "log_allowed": true}); err == nil || !strings.Contains(err.Error(), "still differs") {
		t.Errorf("a change that did not apply: %v", err)
	}
}

// TestProfile_Refuses: values outside their forms are refused before WinRM
// is reached.
func TestProfile_Refuses(t *testing.T) {
	p := &profiles{kept: map[string]map[string]string{}}
	p.install(t)
	for name, params := range map[string]map[string]any{
		"no name":         {},
		"another profile": {"name": "work"},
		"an action":       {"name": "domain", "default_inbound_action": "drop"},
		"not a bool":      {"name": "domain", "enabled": "perhaps"},
		"a size":          {"name": "domain", "log_max_size_kb": 40000},
		"not a size":      {"name": "domain", "log_max_size_kb": "big"},
		"a quote":         {"name": "domain", "log_file": `C:\a"b.log`},
		"an empty file":   {"name": "domain", "log_file": ""},
		"not a string":    {"name": "domain", "log_file": 5},
	} {
		if _, _, err := pfRun(t, false, params); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(p.calls) != 0 {
		t.Errorf("WinRM was reached %d times", len(p.calls))
	}
}

// TestProfile_HostFailures: a script that fails, an answer that is not a
// profile, a device not reached over WinRM and a stat that cannot be kept
// each fail the task.
func TestProfile_HostFailures(t *testing.T) {
	old := run
	t.Cleanup(func() { run = old })
	for want, res := range map[string]winrmexec.Result{"query exited 1": {ExitCode: 1, Stderr: "Access is denied."}, "not the profile's": {Stdout: "{}"}} {
		run = func(context.Context, winrmexec.Target, winrmexec.Auth, string, string, winrmexec.Options) (winrmexec.Result, error) {
			return res, nil
		}
		if _, _, err := pfRun(t, false, map[string]any{"name": "domain"}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	p := &profiles{kept: map[string]map[string]string{"domain": stock()}}
	p.install(t)
	if _, err := Profile(context.Background(), &ctxStub{stats: map[string]any{}}, device().(host).Stub, map[string]any{"name": "domain"}); err == nil {
		t.Error("a device not reached over WinRM passed")
	}
	if _, err := Profile(context.Background(), failingStats{}, device(), map[string]any{"name": "domain"}); err == nil {
		t.Error("a stat that could not be kept passed")
	}
}

// TestProfileScript_FitsTheCommandLine: the script fits the WinRM
// service's command line.
func TestProfileScript_FitsTheCommandLine(t *testing.T) {
	if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, profileScript, winrmexec.Options{}); err != nil {
		t.Fatal(err)
	}
}

// TestScripts_NameNoFunctionAfterAnAlias: a function the scripts define is
// not named after a PowerShell alias, which would run in its place. The
// profile script's R ran Invoke-History on the lab's domain controller.
func TestScripts_NameNoFunctionAfterAnAlias(t *testing.T) {
	// The built-in aliases of one or two letters, the names a helper is
	// likely to be given.
	aliases := map[string]bool{"h": true, "r": true, "ac": true, "cd": true, "cp": true, "fc": true, "fl": true, "ft": true,
		"fw": true, "gc": true, "gi": true, "gl": true, "gm": true, "gp": true, "gu": true, "gv": true, "ii": true, "ls": true,
		"md": true, "mi": true, "mp": true, "mv": true, "ni": true, "nv": true, "ps": true, "rd": true, "ri": true, "rm": true,
		"rp": true, "rv": true, "si": true, "sl": true, "sp": true, "sv": true, "%": true, "?": true}
	for name, script := range map[string]string{"rule": firewallScript, "profile": profileScript} {
		for _, line := range strings.Split(script, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 || !strings.EqualFold(fields[0], "function") {
				continue
			}
			fn := strings.ToLower(strings.SplitN(fields[1], "(", 2)[0])
			if aliases[fn] || !strings.Contains(fn, "-") {
				t.Errorf("the %s script defines %q, which is not a verb-noun name and may be an alias", name, fn)
			}
		}
	}
}
