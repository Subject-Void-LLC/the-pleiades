// Tests for win.firewall.rule with its WinRM call replaced by a stand-in
// that keeps rules and answers as the script does, in the forms Windows
// writes them: profiles as "Domain, Private" or "Any", protocols in capitals,
// an IPv4 network with a dotted mask.
package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

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

// ctxStub keeps the stats a call sets.
type ctxStub struct{ stats map[string]any }

func (c *ctxStub) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "pw"}
}
func (c *ctxStub) SetStat(k string, v any) error  { c.stats[k] = v; return nil }
func (c *ctxStub) EmitFact(k string, v any) error { return c.SetStat(k, v) }

// failingStats keeps no stat.
type failingStats struct{}

func (failingStats) InjectSecrets() map[string]string {
	return map[string]string{"username": "administrator", "password": "pw"}
}
func (failingStats) SetStat(string, any) error  { return fmt.Errorf("the journal is full") }
func (failingStats) EmitFact(string, any) error { return nil }

// firewall is the stand-in: the rules it keeps, the calls it was sent, and
// whether a change is dropped (a host that answers but does not apply).
type firewall struct {
	rules   map[string]*ruleAsIs
	policy  map[string]bool
	calls   []map[string]any
	scripts []string
	drop    bool
}

func (f *firewall) install(t *testing.T) {
	t.Helper()
	old := run
	t.Cleanup(func() { run = old })
	run = func(_ context.Context, _ winrmexec.Target, _ winrmexec.Auth, script, stdin string, _ winrmexec.Options) (winrmexec.Result, error) {
		f.scripts = append(f.scripts, script)
		var in map[string]any
		if err := json.Unmarshal([]byte(stdin), &in); err != nil {
			t.Fatalf("stdin is not JSON: %v", err)
		}
		f.calls = append(f.calls, in)
		name := in["name"].(string)
		set, _ := in["set"].(map[string]any)
		switch in["op"] {
		case "create":
			r := &ruleAsIs{Exists: true, Enabled: "True", Direction: "Inbound", Action: "Allow", Profiles: "Any", Protocol: "Any",
				LocalPort: []string{"Any"}, RemotePort: []string{"Any"}, LocalAddress: []string{"Any"}, RemoteAddress: []string{"Any"}, Program: "Any", Service: "Any"}
			apply(r, set)
			f.rules[name] = r
		case "set":
			if !f.drop {
				apply(f.rules[name], set)
			}
		case "remove":
			delete(f.rules, name)
		}
		a := ruleAsIs{Policy: f.policy[name]}
		if r := f.rules[name]; r != nil {
			a = *r
		}
		out, _ := json.Marshal(a)
		return winrmexec.Result{Stdout: string(out)}, nil
	}
}

// apply sets a call's settings on r as Windows would keep them.
func apply(r *ruleAsIs, set map[string]any) {
	list := func(v any) []string {
		var out []string
		for _, e := range v.([]any) {
			s := e.(string)
			if p, err := netip.ParsePrefix(s); err == nil && p.Addr().Is4() {
				m := netip.AddrFrom4([4]byte{})
				bits := uint32(0xffffffff) << (32 - p.Bits())
				m = netip.AddrFrom4([4]byte{byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits)})
				s = p.Addr().String() + "/" + m.String()
			}
			out = append(out, s)
		}
		return out
	}
	for k, v := range set {
		switch k {
		case fwDisplayName:
			r.DisplayName = v.(string)
		case fwDescription:
			r.Description = v.(string)
		case fwEnabled:
			r.Enabled = v.(string)
		case fwDirection:
			r.Direction = v.(string)
		case fwAction:
			r.Action = v.(string)
		case fwProfiles:
			p := list(v)
			if len(p) == 3 {
				r.Profiles = "Any"
			} else {
				r.Profiles = strings.Join(p, ", ")
			}
		case fwProtocol:
			r.Protocol = strings.ToUpper(v.(string))
		case fwLocalPort:
			r.LocalPort = list(v)
		case fwRemotePort:
			r.RemotePort = list(v)
		case fwLocalAddress:
			r.LocalAddress = list(v)
		case fwRemoteAddress:
			r.RemoteAddress = list(v)
		case fwProgram:
			r.Program = v.(string)
		case fwService:
			r.Service = v.(string)
		}
	}
}

// openSSH is Windows Server 2025's own OpenSSH rule, as it ships.
func openSSH() *ruleAsIs {
	return &ruleAsIs{Exists: true, DisplayName: "OpenSSH SSH Server (sshd)", Group: "OpenSSH Server", Enabled: "True", Direction: "Inbound",
		Action: "Allow", Profiles: "Private", Protocol: "TCP", LocalPort: []string{"22"}, RemotePort: []string{"Any"}, LocalAddress: []string{"Any"},
		RemoteAddress: []string{"Any"}, Program: `C:\Windows\System32\OpenSSH\sshd.exe`, Service: "Any"}
}

func fwRun(t *testing.T, check bool, p map[string]any) (*ctxStub, bool, error) {
	t.Helper()
	rc := &ctxStub{stats: map[string]any{}}
	res, err := firewallRule(context.Background(), rc, device(), p, check)
	return rc, res.Changed, err
}

// undoParams is the recorded undo's parameters.
func undoParams(t *testing.T, rc *ctxStub) map[string]any {
	t.Helper()
	inv, ok := rc.stats[sdk.StatInverse].(map[string]any)
	if !ok {
		t.Fatalf("no undo recorded: %v", rc.stats)
	}
	return inv["params"].(map[string]any)
}

// TestFirewallRule_WidensABuiltInRuleAndPutsItBack: the case it was built
// for. Only profiles are sent, the rest of the rule is left as it was, a
// second run is no change, and the undo puts the one setting back.
func TestFirewallRule_WidensABuiltInRuleAndPutsItBack(t *testing.T) {
	f := &firewall{rules: map[string]*ruleAsIs{"OpenSSH-Server-In-TCP": openSSH()}}
	f.install(t)
	p := map[string]any{"name": "OpenSSH-Server-In-TCP", "profiles": []any{"domain", "private"}}
	if rc, changed, err := fwRun(t, true, p); err != nil || !changed || len(f.calls) != 1 {
		t.Fatalf("check: %v %v, %d calls", changed, err, len(f.calls))
	} else if d := rc.stats["differences"].([]any); len(d) != 1 || !strings.Contains(d[0].(string), "profiles") {
		t.Errorf("differences %v", d)
	}
	rc, changed, err := fwRun(t, false, p)
	if err != nil || !changed {
		t.Fatalf("run: %v %v", changed, err)
	}
	if set := f.calls[len(f.calls)-1]["set"].(map[string]any); len(set) != 1 || set["profiles"] == nil {
		t.Errorf("sent %v, want profiles alone", set)
	}
	if r := f.rules["OpenSSH-Server-In-TCP"]; r.Profiles != "Domain, Private" || r.LocalPort[0] != "22" || r.Program != openSSH().Program {
		t.Errorf("the rule is %+v", r)
	}
	undo := undoParams(t, rc)
	if !reflect.DeepEqual(undo, map[string]any{"name": "OpenSSH-Server-In-TCP", "state": "present", "profiles": "private"}) {
		t.Errorf("undo %v", undo)
	}
	if _, changed, err := fwRun(t, false, p); err != nil || changed {
		t.Errorf("again: %v %v", changed, err)
	}
	if _, changed, err := fwRun(t, false, undo); err != nil || !changed || f.rules["OpenSSH-Server-In-TCP"].Profiles != "Private" {
		t.Errorf("the undo: %v %v, %+v", changed, err, f.rules["OpenSSH-Server-In-TCP"])
	}
}

// TestFirewallRule_MakesRemovesAndMakesAgain: a new rule gets Windows'
// defaults and its name as its display name, and undoes to its removal; a
// removal undoes to a rule with every setting it had; a network written as a
// prefix matches Windows' dotted mask.
func TestFirewallRule_MakesRemovesAndMakesAgain(t *testing.T) {
	f := &firewall{rules: map[string]*ruleAsIs{}}
	f.install(t)
	p := map[string]any{"name": "Pleiades-App-In", "protocol": "tcp", "local_port": []any{8443, "9000-9010"}, "remote_address": []any{"10.20.0.0/16", "LocalSubnet"}, "profiles": "domain"}
	rc, changed, err := fwRun(t, false, p)
	if err != nil || !changed {
		t.Fatalf("make: %v %v", changed, err)
	}
	if r := f.rules["Pleiades-App-In"]; r.DisplayName != "Pleiades-App-In" || r.RemoteAddress[0] != "10.20.0.0/255.255.0.0" {
		t.Errorf("made %+v", r)
	}
	if u := undoParams(t, rc); u["state"] != "absent" {
		t.Errorf("undo %v", u)
	}
	if _, changed, err := fwRun(t, false, p); err != nil || changed {
		t.Errorf("again, with Windows' dotted mask: %v %v", changed, err)
	}
	rc, changed, err = fwRun(t, false, map[string]any{"name": "Pleiades-App-In", "state": "absent"})
	if err != nil || !changed || f.rules["Pleiades-App-In"] != nil {
		t.Fatalf("remove: %v %v", changed, err)
	}
	if _, changed, err := fwRun(t, false, undoParams(t, rc)); err != nil || !changed {
		t.Fatalf("the removal's undo: %v %v", changed, err)
	}
	if _, changed, err := fwRun(t, false, p); err != nil || changed {
		t.Errorf("the rule made again differs from the one removed: %v %v %+v", changed, err, f.rules["Pleiades-App-In"])
	}
	if _, changed, err := fwRun(t, false, map[string]any{"name": "Pleiades-App-In", "state": "query"}); err != nil || changed {
		t.Errorf("query: %v %v", changed, err)
	}
	if _, changed, err := fwRun(t, false, map[string]any{"name": "Gone", "state": "absent"}); err != nil || changed {
		t.Errorf("removing what is not there: %v %v", changed, err)
	}
}

// TestFirewallRule_RefusesWhatItCannotKeep: a Group Policy rule, and a host
// that answers without applying the change, fail the task.
func TestFirewallRule_RefusesWhatItCannotKeep(t *testing.T) {
	f := &firewall{rules: map[string]*ruleAsIs{"OpenSSH-Server-In-TCP": openSSH()}, policy: map[string]bool{"Corp-Block": true}}
	f.install(t)
	if _, _, err := fwRun(t, false, map[string]any{"name": "Corp-Block", "enabled": false}); err == nil || !strings.Contains(err.Error(), "Group Policy") {
		t.Errorf("a policy rule: %v", err)
	}
	f.drop = true
	if _, _, err := fwRun(t, false, map[string]any{"name": "OpenSSH-Server-In-TCP", "enabled": false}); err == nil || !strings.Contains(err.Error(), "still differs") {
		t.Errorf("a change that did not apply: %v", err)
	}
}

// TestFirewallRule_Refuses: values outside their forms are refused before
// WinRM is reached, and so are ports beside a protocol that has none.
func TestFirewallRule_Refuses(t *testing.T) {
	f := &firewall{rules: map[string]*ruleAsIs{"OpenSSH-Server-In-TCP": openSSH(), "Ping": {Exists: true, Protocol: "ICMPv4", Profiles: "Any"}}}
	f.install(t)
	for name, p := range map[string]map[string]any{
		"a quote in the name":   {"name": "a'b"},
		"another state":         {"name": "x", "state": "on"},
		"a direction":           {"name": "x", "direction": "both"},
		"a profile":             {"name": "x", "profiles": []any{"work"}},
		"any beside a profile":  {"name": "x", "profiles": []any{"any", "domain"}},
		"a protocol number":     {"name": "x", "protocol": 300},
		"a port":                {"name": "x", "protocol": "tcp", "local_port": []any{70000}},
		"a backwards range":     {"name": "x", "protocol": "tcp", "local_port": []any{"9-1"}},
		"any beside a port":     {"name": "x", "protocol": "tcp", "local_port": []any{"any", 22}},
		"a remote keyword port": {"name": "x", "protocol": "tcp", "remote_port": []any{"RPC"}},
		"an address":            {"name": "x", "remote_address": []any{"10.0.0.0/33"}},
		"a gapped mask":         {"name": "x", "remote_address": []any{"10.0.0.0/255.0.255.0"}},
		"a mixed range":         {"name": "x", "remote_address": []any{"10.0.0.1-::1"}},
		"a quote in a program":  {"name": "x", "program": `C:\a"b.exe`},
		"a service":             {"name": "x", "service": "a b"},
		"an empty display name": {"name": "x", "display_name": ""},
		"a line break":          {"name": "x", "description": "a\nb"},
		"not a list":            {"name": "x", "profiles": map[string]any{"a": 1}},
		"an empty list":         {"name": "x", "profiles": []any{}},
		"a list of lists":       {"name": "x", "profiles": []any{[]any{"domain"}}},
		"not a bool":            {"name": "x", "enabled": "yes please"},
		"ports on a new any":    {"name": "x", "local_port": []any{22}},
		"ports on an ICMP rule": {"name": "Ping", "local_port": []any{22}},
		"ports with ICMP given": {"name": "OpenSSH-Server-In-TCP", "protocol": "icmpv4", "local_port": []any{22}},
	} {
		if _, _, err := fwRun(t, false, p); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, c := range f.calls {
		if c["op"] != "query" {
			t.Errorf("a refused call changed something: %v", c)
		}
	}
}

// TestFirewallRule_ValuesNeverBecomeScript: hostile values reach the
// device as JSON on standard input, and the script is always the constant.
func TestFirewallRule_ValuesNeverBecomeScript(t *testing.T) {
	f := &firewall{rules: map[string]*ruleAsIs{}}
	f.install(t)
	hostile := `x'; Remove-Item -Recurse C:\ #$(whoami) ` + "Jos\u00e9"
	if _, _, err := fwRun(t, false, map[string]any{"name": "Pleiades-Test", "description": hostile, "display_name": hostile}); err != nil {
		t.Fatal(err)
	}
	if got := f.calls[len(f.calls)-1]["set"].(map[string]any)["description"]; got != hostile {
		t.Errorf("stdin carried %q", got)
	}
	for i, s := range f.scripts {
		if s != firewallScript {
			t.Errorf("call %d sent a script other than the constant", i)
		}
	}
}

// TestFirewallRule_HostFailures: a script that exits non-zero, an answer
// that is not a rule, and a WinRM failure each fail the task, named.
func TestFirewallRule_HostFailures(t *testing.T) {
	old := run
	t.Cleanup(func() { run = old })
	for want, res := range map[string]winrmexec.Result{
		"query exited 1":             {ExitCode: 1, Stderr: "Access is denied."},
		"not the rule's description": {Stdout: "<html>"},
	} {
		run = func(context.Context, winrmexec.Target, winrmexec.Auth, string, string, winrmexec.Options) (winrmexec.Result, error) {
			return res, nil
		}
		if _, _, err := fwRun(t, false, map[string]any{"name": "x"}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	run = func(context.Context, winrmexec.Target, winrmexec.Auth, string, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{}, fmt.Errorf("401 - invalid content type")
	}
	if _, _, err := fwRun(t, false, map[string]any{"name": "x"}); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("a WinRM failure: %v", err)
	}
}

// TestFirewallScript_FitsTheCommandLine: the script fits the 8191
// characters the WinRM service's cmd.exe accepts, as -EncodedCommand.
func TestFirewallScript_FitsTheCommandLine(t *testing.T) {
	if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, firewallScript, winrmexec.Options{}); err != nil {
		t.Fatal(err)
	}
}

// TestFirewallRule_Forms: the forms a call's and a rule's settings meet in.
// A host address written as a network is the address; IPv6 networks and
// ranges keep their form; * is any; a value Windows answers that this
// method does not write still compares, as written, and differs from what
// is asked.
func TestFirewallRule_Forms(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.5/32": "10.0.0.5", "10.0.0.5/255.255.255.255": "10.0.0.5", "2001:db8::/32": "2001:db8::/32",
		"10.0.0.1-10.0.0.9": "10.0.0.1-10.0.0.9", "*": "any", "Intranet": "intranet", "fe80::1": "fe80::1",
	} {
		if got, err := canonicalEntry(fwRemoteAddress, in); err != nil || got != want {
			t.Errorf("%s = %q, %v; want %q", in, got, err, want)
		}
	}
	if got, err := canonicalEntry(fwLocalPort, "RPCEPMap"); err != nil || got != "rpcepmap" {
		t.Errorf("a port keyword = %q, %v", got, err)
	}
	odd := ruleAsIs{Exists: true, Profiles: "Domain, Public", Protocol: "6", LocalPort: []string{"PlayToDiscoveryX"}, Enabled: "False"}
	c := odd.canonical()
	if !reflect.DeepEqual(c[fwLocalPort], []string{"playtodiscoveryx"}) || c[fwProtocol] != "tcp" || c[fwEnabled] != false {
		t.Errorf("canonical %v", c)
	}
	if p := odd.asParams()[fwProfiles]; p != "domain,public" {
		t.Errorf("profiles as a parameter: %v", p)
	}
	if r, err := parseRule(map[string]any{"name": "x", "local_port": "22", "protocol": "tcp"}); err != nil {
		t.Fatal(err)
	} else if fields, _ := ruleDifferences(odd, r); !reflect.DeepEqual(fields, []string{fwLocalPort}) {
		t.Errorf("differences %v", fields)
	}
}

// TestFirewallRule_DeviceAndRecording: the exported entry runs a real call;
// a device not reached over WinRM, and a stat that cannot be kept, fail it.
func TestFirewallRule_DeviceAndRecording(t *testing.T) {
	f := &firewall{rules: map[string]*ruleAsIs{"OpenSSH-Server-In-TCP": openSSH()}}
	f.install(t)
	if res, err := Rule(context.Background(), &ctxStub{stats: map[string]any{}}, device(), map[string]any{"name": "OpenSSH-Server-In-TCP", "state": "query"}); err != nil || res.Changed {
		t.Errorf("query: %+v %v", res, err)
	}
	stub := device().(host).Stub
	if _, err := Rule(context.Background(), &ctxStub{stats: map[string]any{}}, stub, map[string]any{"name": "x"}); err == nil || !strings.Contains(err.Error(), "WinRM") {
		t.Errorf("a device not reached over WinRM: %v", err)
	}
	if _, err := Rule(context.Background(), failingStats{}, device(), map[string]any{"name": "OpenSSH-Server-In-TCP", "state": "query"}); err == nil {
		t.Error("a stat that could not be kept passed")
	}
}

// TestFirewallRule_UndoIsJournalable: every value an undo carries is a
// single string, number or boolean, the only kinds the run journal keeps,
// and only the display name and description are withheld from it.
func TestFirewallRule_UndoIsJournalable(t *testing.T) {
	f := &firewall{rules: map[string]*ruleAsIs{"OpenSSH-Server-In-TCP": openSSH()}}
	f.install(t)
	rc, _, err := fwRun(t, false, map[string]any{"name": "OpenSSH-Server-In-TCP", "state": "absent"})
	if err != nil {
		t.Fatal(err)
	}
	undo := undoParams(t, rc)
	for k, v := range undo {
		switch v.(type) {
		case string, bool, int, int64, float64:
		default:
			t.Errorf("%s is %T, which the journal cannot keep", k, v)
		}
	}
	if undo["local_port"] != "22" || undo["profiles"] != "private" {
		t.Errorf("undo %v", undo)
	}
	for _, k := range recorded {
		if k == fwDisplayName || k == fwDescription {
			t.Errorf("%s is recorded, and it is text the device holds", k)
		}
	}
	if _, changed, err := fwRun(t, false, undo); err != nil || !changed {
		t.Errorf("the comma-separated undo did not make the rule again: %v %v", changed, err)
	}
	if r := f.rules["OpenSSH-Server-In-TCP"]; r == nil || r.Profiles != "Private" || r.LocalPort[0] != "22" {
		t.Errorf("made again as %+v", r)
	}
}
