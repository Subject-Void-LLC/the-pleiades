// Tests for win.cpu.topology against a host that answers with what the lab
// host printed (pkg/wincpu/testdata). The script itself runs on a real host
// in the lab run.
package cpu

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wincpu"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// recorder keeps what the method records.
type recorder struct {
	stats  map[string]any
	failOn string
	// clash makes the credential a certificate bundle and a password
	// together, which WinRM authentication refuses.
	clash bool
}

func (r *recorder) InjectSecrets() map[string]string {
	if r.clash {
		return map[string]string{"pfx_base64": "MA==", "password": "secret"}
	}
	return map[string]string{"username": "gate", "password": "secret"}
}

func (r *recorder) SetStat(k string, v any) error {
	if k == r.failOn {
		return errors.New("refusing " + k)
	}
	r.stats[k] = v
	return nil
}

func (r *recorder) EmitFact(k string, v any) error { return r.SetStat(k, v) }

// windowsHost is a Windows server as the method sees one.
type windowsHost struct{ *inventorytest.Stub }

func (windowsHost) WinRMHost() string        { return "172.18.32.1" }
func (windowsHost) WinRMPort() int           { return 5986 }
func (windowsHost) WorkingDirectory() string { return "" }
func (windowsHost) CmdPath() string          { return `C:\Windows\System32\cmd.exe` }
func (windowsHost) PowerShellPath() string {
	return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
}

var host inventory.InventoryItem = windowsHost{&inventorytest.Stub{StubName: "vengeance", Caps: []capability.Name{capability.NameWindowsShell, capability.NameWinRM}}}

// answer makes the method's host answer with result, recording the
// script and options it was sent.
func answer(t *testing.T, result winrmexec.Result, err error) *[]string {
	t.Helper()
	var sent []string
	old := run
	run = func(_ context.Context, target winrmexec.Target, _ winrmexec.Auth, shell winrmexec.Shell, script string, opts winrmexec.Options) (winrmexec.Result, error) {
		if shell != winrmexec.ShellPowerShell || target.Host != "172.18.32.1" || opts.PowerShellPath == "" {
			t.Errorf("sent to %+v as %v with %+v", target, shell, opts)
		}
		sent = append(sent, script, opts.Timeout.String())
		return result, err
	}
	t.Cleanup(func() { run = old })
	return &sent
}

// call runs the method through its registered descriptor.
func call(t *testing.T, check bool, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup("win.cpu.topology")
	if !ok {
		t.Fatal("win.cpu.topology is not registered")
	}
	if check {
		return d.Check(context.Background(), rc, device, params)
	}
	return d.Invoke(context.Background(), rc, device, params)
}

func captured(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../../../pkg/wincpu/testdata/cpusets-core-ultra-7-265kf.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTopology_Registered(t *testing.T) {
	d, ok := collection.Lookup("win.cpu.topology")
	if !ok {
		t.Fatal("not registered")
	}
	if m := d.Manifest; m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || m.Reversibility.Reversible {
		t.Errorf("status %v, check %v, reversible %v", m.Status, m.SupportsCheck, m.Reversibility.Reversible)
	}
}

// TestTopology proves a run and a check read the same and report the lab
// host's P-cores and E-cores, sending the script with the default timeout.
func TestTopology(t *testing.T) {
	for _, check := range []bool{false, true} {
		sent := answer(t, winrmexec.Result{Stdout: captured(t)}, nil)
		rc := &recorder{stats: map[string]any{}}
		result, err := call(t, check, rc, host, nil)
		if err != nil || result.Changed {
			t.Fatalf("check %v: %+v, %v", check, result, err)
		}
		if (*sent)[0] != wincpu.Script || (*sent)[1] != "2m0s" {
			t.Errorf("sent %q", (*sent)[1])
		}
		for key, want := range map[string]any{
			"processor_count": 20, "core_count": 20, "hybrid": true, "performance_affinity_mask": "0xC03C3",
			"performance_processors": []int{0, 1, 6, 7, 8, 9, 18, 19},
			"efficiency_processors":  []int{2, 3, 4, 5, 10, 11, 12, 13, 14, 15, 16, 17},
		} {
			if !reflect.DeepEqual(rc.stats[key], want) {
				t.Errorf("check %v: %s = %v, want %v", check, key, rc.stats[key], want)
			}
		}
		first := rc.stats["processors"].([]any)[0]
		want := map[string]any{"index": 0, "group": 0, "core": 0, "numa_node": 0, "last_level_cache": 0, "efficiency_class": 1, "parked": false}
		if !reflect.DeepEqual(first, want) {
			t.Errorf("processor 0 = %v", first)
		}
	}
}

// TestTopology_NoMaskAcrossGroups proves a host with two processor groups
// reports no mask rather than one naming only part of them.
func TestTopology_NoMaskAcrossGroups(t *testing.T) {
	answer(t, winrmexec.Result{Stdout: "cpuset lp=0 group=0 core=0 llc=0 numa=0 class=0 flags=0\ncpuset lp=0 group=1 core=0 llc=1 numa=1 class=0 flags=0\n"}, nil)
	rc := &recorder{stats: map[string]any{}}
	if _, err := call(t, false, rc, host, map[string]any{"timeout": 30}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.stats["performance_affinity_mask"]; ok || rc.stats["hybrid"] != false {
		t.Errorf("stats %v", rc.stats)
	}
}

func TestTopology_Failures(t *testing.T) {
	notWindows := &inventorytest.Stub{StubName: "linux", Caps: []capability.Name{capability.NameWinRM}}
	for why, tt := range map[string]struct {
		device inventory.InventoryItem
		params map[string]any
		result winrmexec.Result
		err    error
		want   string
	}{
		"no device":          {nil, nil, winrmexec.Result{}, nil, "needs a target device"},
		"not Windows":        {notWindows, nil, winrmexec.Result{}, nil, `"linux" does not implement`},
		"a zero timeout":     {host, map[string]any{"timeout": 0}, winrmexec.Result{}, nil, "positive"},
		"a text timeout":     {host, map[string]any{"timeout": "soon"}, winrmexec.Result{}, nil, "timeout"},
		"WinRM fails":        {host, nil, winrmexec.Result{}, errors.New("connection refused"), "connection refused"},
		"constrained":        {host, nil, winrmexec.Result{ExitCode: 20, Stdout: "language=ConstrainedLanguage\r\n"}, nil, "runs in ConstrainedLanguage, which refuses the Add-Type"},
		"constrained, blank": {host, nil, winrmexec.Result{ExitCode: 20}, nil, "a restricted language mode"},
		"the script fails":   {host, nil, winrmexec.Result{ExitCode: 1, Stderr: "Add-Type : Cannot add type."}, nil, "exited 1: Add-Type : Cannot add type."},
		"garbled output":     {host, nil, winrmexec.Result{Stdout: "cpuset lp=x"}, nil, "wincpu:"},
	} {
		answer(t, tt.result, tt.err)
		if _, err := call(t, false, &recorder{stats: map[string]any{}}, tt.device, tt.params); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
	sent := answer(t, winrmexec.Result{Stdout: captured(t)}, nil)
	if _, err := call(t, false, &recorder{stats: map[string]any{}, clash: true}, host, nil); err == nil || len(*sent) != 0 {
		t.Errorf("a credential WinRM refuses: %v, sent %d", err, len(*sent))
	}
	for _, stat := range []string{"processors", "performance_affinity_mask"} {
		answer(t, winrmexec.Result{Stdout: captured(t)}, nil)
		if _, err := call(t, false, &recorder{stats: map[string]any{}, failOn: stat}, host, nil); err == nil {
			t.Errorf("recording %s failed and the method did not", stat)
		}
	}
}
