// Tests of facts.gather's Windows path (gather_win.go, gather_transport.go):
// the parsers against what the lab's Windows host printed, and Gather
// against a host model that answers each script, so the route through
// WinRM, the scripts sent and the refusal of a failed one are proven here.
// The scripts themselves run on a real host in the lab gate
// (cmd/pleiades/facts_lab_release_gate_test.go).
package facts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

func TestWindowsParsers_ReadTheScriptsJSON(t *testing.T) {
	min := valuesOf(parseWindowsMin(`{"ansible_hostname":"WIN-CHECK","ansible_nodename":"win-check.lab.example","ansible_system":"Win32NT","ansible_kernel":"10.0.26100.0",` +
		`"ansible_architecture":"64-bit","ansible_distribution":"Microsoft Windows 11 Pro","ansible_distribution_version":"10.0.26100.0"}` + "\r\n"))
	want := map[string]any{
		gatherFactNodename: "win-check.lab.example", gatherFactHostname: "WIN-CHECK", gatherFactSystem: "Win32NT",
		gatherFactKernel: "10.0.26100.0", gatherFactArchitecture: "64-bit", gatherFactDistribution: "Microsoft Windows 11 Pro",
		gatherFactDistributionVersion: "10.0.26100.0", gatherFactDistributionMajor: "10",
	}
	if !reflect.DeepEqual(min, want) {
		t.Errorf("min = %v\nwant %v", min, want)
	}
	// A host naming no computer name takes the node name's first label.
	if got := valuesOf(parseWindowsMin(`{"ansible_hostname":"","ansible_nodename":"web01.example"}`)); got[gatherFactHostname] != "web01" {
		t.Errorf("host name from the node name = %v", got)
	}
	hw := valuesOf(parseWindowsObject(`{"ansible_memtotal_mb":2047,"ansible_swaptotal_mb":0,"ansible_processor_count":1,"ansible_processor_vcpus":2,"ansible_processor_cores":2,"ansible_uptime_seconds":311}`))
	if hw[gatherFactMemTotalMB] != 2047 || hw[gatherFactSwapTotalMB] != 0 || hw[gatherFactUptimeSeconds] != 311 || len(hw) != 6 {
		t.Errorf("hardware = %v", hw)
	}
	// What the host could not answer is absent, never empty or guessed.
	if got := valuesOf(parseWindowsObject(`{"ansible_distribution":"  ","ansible_kernel":null,"ansible_memtotal_mb":-1,"ansible_x":1.5,"ansible_y":[1],"ansible_system":"Win32NT"}`)); !reflect.DeepEqual(got, map[string]any{gatherFactSystem: "Win32NT"}) {
		t.Errorf("partial = %v", got)
	}
	for _, bad := range []string{"", "[]", "not json", `{"a":`, `{"a" 1}`} {
		if got := parseWindowsObject(bad); got != nil {
			t.Errorf("%q read as %v", bad, got)
		}
	}
}

func TestWindowsPackages_EveryEntryKeepsItsOwnFact(t *testing.T) {
	out := `[{"name":"7-Zip 24.09 (x64)","version":"24.09","arch":"amd64"},` +
		`{"name":"Microsoft Visual C++ 2022 X64 Minimum Runtime","version":"14.40.33810","arch":"amd64"},` +
		`{"name":"Microsoft Visual C++ 2022 X64 Minimum Runtime","version":"14.38.33130","arch":"amd64"},` +
		`{"name":"Microsoft Visual C++ 2022 X64 Minimum Runtime","version":"14.36.32532","arch":"amd64"},` +
		`{"name":"Caf\u00e9 Tool","version":"","arch":"x86"},{"name":"  ","version":"1","arch":"x86"}]`
	got := valuesOf(distinctAll(packageFacts(parseWindowsPackages(out))))
	want := map[string]any{
		"package.7-Zip%2024.09%20(x64)":                                                    map[string]any{"version": "24.09", "arch": "amd64", "source": sourceRegistry},
		"package.Microsoft%20Visual%20C++%202022%20X64%20Minimum%20Runtime":                map[string]any{"version": "14.40.33810", "arch": "amd64", "source": sourceRegistry},
		"package.Microsoft%20Visual%20C++%202022%20X64%20Minimum%20Runtime:amd64":          map[string]any{"version": "14.38.33130", "arch": "amd64", "source": sourceRegistry},
		"package.Microsoft%20Visual%20C++%202022%20X64%20Minimum%20Runtime:amd64@registry": map[string]any{"version": "14.36.32532", "arch": "amd64", "source": sourceRegistry},
		"package.Caf\u00e9%20Tool":                                                         map[string]any{"arch": "x86", "source": sourceRegistry},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("packages = %v\nwant %v", got, want)
	}
	// A fourth entry of the same name, arch and source takes a number.
	four := valuesOf(distinctAll(packageFacts(append(parseWindowsPackages(out), installedPackage{name: "Microsoft Visual C++ 2022 X64 Minimum Runtime", version: "1", arch: "amd64", source: sourceRegistry}))))
	if _, ok := four["package.Microsoft%20Visual%20C++%202022%20X64%20Minimum%20Runtime:amd64@registry#2"]; !ok || len(four) != 6 {
		t.Errorf("a fourth entry was not told apart: %v", four)
	}
	if parseWindowsPackages("") != nil || parseWindowsPackages("{}") != nil {
		t.Error("output that is not an array was read")
	}
}

func TestWindowsServices_ReadTheManagersWords(t *testing.T) {
	out := `[{"name":"WinRM","state":"Running","mode":"Auto","delayed":false},` +
		`{"name":"BITS","state":"Stopped","mode":"Auto","delayed":true},` +
		`{"name":"Spooler","state":"Stopped","mode":"Disabled","delayed":false},` +
		`{"name":"msiserver","state":"Start Pending","mode":"Manual","delayed":false},` +
		`{"name":"","state":"Running","mode":"Auto"},{"name":"odd","state":"","mode":""}]`
	got := valuesOf(parseWindowsServices(out))
	want := map[string]any{
		"service.WinRM":     map[string]any{"state": "running", "enabled": true, "status": "auto", "source": sourceSCM},
		"service.BITS":      map[string]any{"state": "stopped", "enabled": true, "status": "delayed", "source": sourceSCM},
		"service.Spooler":   map[string]any{"state": "stopped", "enabled": false, "status": "disabled", "source": sourceSCM},
		"service.msiserver": map[string]any{"state": "start pending", "enabled": false, "status": "manual", "source": sourceSCM},
		"service.odd":       map[string]any{"state": "unknown", "enabled": false, "status": "", "source": sourceSCM},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("services = %v\nwant %v", got, want)
	}
}

func TestWindowsScripts_FitTheCommandLineAndCarryNoUnicodeEscape(t *testing.T) {
	for _, p := range windowsProbes() {
		if err := winrmexec.CheckScript(winrmexec.ShellPowerShell, p.command, winrmexec.Options{}); err != nil {
			t.Errorf("%s: %v", p.name(), err)
		}
		if strings.Contains(p.command, "\\"+"u") || strings.Contains(p.command, "`") {
			t.Errorf("%s holds a backslash-u or a backtick, which the ASCII escape avoids: %q", p.name(), p.command)
		}
	}
}

// winTestContext is a sdk.RunbookContext keeping facts.
type winTestContext struct{ facts map[string]any }

func (c *winTestContext) InjectSecrets() map[string]string {
	return map[string]string{"username": "gate", "password": "secret"}
}
func (c *winTestContext) SetStat(string, any) error { return nil }
func (c *winTestContext) EmitFact(k string, v any) error {
	c.facts[k] = v
	return nil
}

// winTestHost is a Windows server as facts.gather sees one.
type winTestHost struct{ *inventorytest.Stub }

func (winTestHost) WinRMHost() string      { return "192.0.2.10" }
func (winTestHost) WinRMPort() int         { return 5985 }
func (winTestHost) FactSourceName() string { return "setup" }
func (winTestHost) WorkingDirectory() string {
	return ""
}
func (winTestHost) CmdPath() string { return `C:\Windows\System32\cmd.exe` }
func (winTestHost) PowerShellPath() string {
	return `D:\ps\powershell.exe`
}

// answerWindows makes the host answer each script by its subset, recording
// the subsets asked.
func answerWindows(t *testing.T, answers map[string]winrmexec.Result) *[]string {
	t.Helper()
	var asked []string
	old := gatherWinRM
	gatherWinRM = func(_ context.Context, target winrmexec.Target, auth winrmexec.Auth, shell winrmexec.Shell, script string, opts winrmexec.Options) (winrmexec.Result, error) {
		if shell != winrmexec.ShellPowerShell || target.Host != "192.0.2.10" || opts.PowerShellPath != `D:\ps\powershell.exe` || auth.Username != "gate" {
			t.Errorf("sent to %+v as %v with %+v", target, shell, opts)
		}
		for _, p := range windowsProbes() {
			if p.command == script {
				asked = append(asked, p.subset)
				return answers[p.subset], nil
			}
		}
		return winrmexec.Result{}, errors.New("a script facts.gather does not send")
	}
	t.Cleanup(func() { gatherWinRM = old })
	return &asked
}

func newWinTestHost() inventory.InventoryItem {
	return winTestHost{&inventorytest.Stub{StubName: "win-check", StubState: inventory.StateActive,
		Caps: []capability.Name{capability.NameWinRM, capability.NameWindowsShell, capability.NameFactGatherer}}}
}

func TestGather_AWindowsHostIsReadOverWinRM(t *testing.T) {
	asked := answerWindows(t, map[string]winrmexec.Result{
		gatherSubsetMin:      {Stdout: `{"ansible_nodename":"win-check","ansible_system":"Win32NT"}`},
		gatherSubsetHardware: {Stdout: `{"ansible_memtotal_mb":2047}`},
		gatherSubsetPackages: {Stdout: `[{"name":"7-Zip","version":"24.09","arch":"amd64"}]`},
		gatherSubsetServices: {Stdout: `[{"name":"WinRM","state":"Running","mode":"Auto","delayed":false}]`},
	})
	rc := &winTestContext{facts: map[string]any{}}
	if _, err := Gather(context.Background(), rc, newWinTestHost(), map[string]any{gatherParamSubset: []any{"all"}, gatherParamFilter: []any{"ansible_*", "package.*", "service.WinRM"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*asked, []string{gatherSubsetMin, gatherSubsetHardware, gatherSubsetPackages, gatherSubsetServices}) {
		t.Errorf("asked %v", *asked)
	}
	if rc.facts[gatherFactHostname] != "win-check" || rc.facts[gatherFactMemTotalMB] != 2047 || rc.facts["package.7-Zip"] == nil || rc.facts["service.WinRM"] == nil {
		t.Errorf("facts %v", rc.facts)
	}

	// The default reads only min and hardware, and a filter narrows the
	// scripts sent.
	*asked = nil
	rc = &winTestContext{facts: map[string]any{}}
	if _, err := Gather(context.Background(), rc, newWinTestHost(), map[string]any{gatherParamFilter: []any{"ansible_system"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*asked, []string{gatherSubsetMin}) || !reflect.DeepEqual(rc.facts, map[string]any{gatherFactSystem: "Win32NT"}) {
		t.Errorf("asked %v, facts %v", *asked, rc.facts)
	}
}

func TestGather_AFailedWindowsScriptFailsTheTask(t *testing.T) {
	answerWindows(t, map[string]winrmexec.Result{
		gatherSubsetMin: {ExitCode: 1, Stderr: "Get-CimInstance : Access denied"},
	})
	_, err := Gather(context.Background(), &winTestContext{facts: map[string]any{}}, newWinTestHost(), nil)
	if err == nil || !strings.Contains(err.Error(), "the Windows min script") || !strings.Contains(err.Error(), "Access denied") {
		t.Errorf("err = %v", err)
	}
	old := gatherWinRM
	gatherWinRM = func(context.Context, winrmexec.Target, winrmexec.Auth, winrmexec.Shell, string, winrmexec.Options) (winrmexec.Result, error) {
		return winrmexec.Result{}, errors.New("connection refused")
	}
	defer func() { gatherWinRM = old }()
	if _, err := Gather(context.Background(), &winTestContext{facts: map[string]any{}}, newWinTestHost(), nil); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("err = %v", err)
	}
}
