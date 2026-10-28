// Tests for virt.vbox.vm.info, and for what every vm method refuses before
// it reaches a host.
package vm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

func TestRegistered(t *testing.T) {
	for fqcn, reversible := range map[string]bool{"virt.vbox.vm.info": false, "virt.vbox.vm.start": true, "virt.vbox.vm.stop": true, "virt.vbox.vm.resize": true} {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		m := d.Manifest
		if m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || m.Reversibility.Reversible != reversible {
			t.Errorf("%s: status %v, check %v, reversible %v", fqcn, m.Status, m.SupportsCheck, m.Reversibility.Reversible)
		}
		if !reflect.DeepEqual(m.RequiredCapabilities, []capability.Name{capability.NameVirtualBox}) {
			t.Errorf("%s requires %v", fqcn, m.RequiredCapabilities)
		}
	}
}

func TestInfo(t *testing.T) {
	vm := lab()
	vm.Snapshots = []vboxmanagetest.Snapshot{{Name: "clean", UUID: "5732a952-0b85-4e3a-820a-1a568f31166b", Description: "fresh install"}}
	vm.Current = vm.Snapshots[0].UUID
	vm.OSType, vm.Firmware = "Ubuntu (64-bit)", "EFI"
	vm.NICs = map[int]vboxmanage.NIC{2: {Kind: "hostonly", MAC: "0800271A2B3C", HostOnlyAdapter: "VirtualBox Host-Only Ethernet Adapter"}, 1: {Kind: "nat", MAC: "080027AABBCC"}}
	vm.Slots = []vboxmanage.Slot{{Controller: "SATA", ControllerType: "IntelAhci", Port: 0, Device: 0, Medium: `C:\vms\ubuntu-lab\disk.vdi`}}
	vm.ConsoleLog = `C:\vms\ubuntu-lab\console.log`
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	for _, check := range []bool{false, true} {
		rc := newRecorder()
		result, err := call(t, "virt.vbox.vm.info", check, rc, map[string]any{"name": "ubuntu-lab"})
		if err != nil || result.Changed {
			t.Fatalf("check %v: %+v, %v", check, result, err)
		}
		want := map[string]any{
			"exists": true, "uuid": vm.UUID, "state": "poweroff", "memory_mb": 1024, "cpus": 2, "size": "", "autostart_enabled": false,
			"snapshots":             []any{map[string]any{"name": "clean", "uuid": vm.Current, "description": "fresh install"}},
			"current_snapshot_uuid": vm.Current,
			"os_type":               "Ubuntu (64-bit)", "firmware": "EFI",
			"nics": []any{
				map[string]any{"number": 1, "kind": "nat", "mac": "080027AABBCC"},
				map[string]any{"number": 2, "kind": "hostonly", "mac": "0800271A2B3C", "host_only_adapter": "VirtualBox Host-Only Ethernet Adapter"},
			},
			"storage":     []any{map[string]any{"controller": "SATA", "controller_type": "IntelAhci", "port": 0, "device": 0, "medium": `C:\vms\ubuntu-lab\disk.vdi`}},
			"console_log": `C:\vms\ubuntu-lab\console.log`,
		}
		if !reflect.DeepEqual(rc.stats, want) {
			t.Errorf("check %v: stats = %v\nwant %v", check, rc.stats, want)
		}
	}
	if c := changes(model); len(c) != 0 {
		t.Errorf("info changed something: %v", c)
	}
}

// TestInfo_Missing: a VM the host does not have is exists false and
// nothing else, in a run and in a check alike (Phase 131, R4).
func TestInfo_Missing(t *testing.T) {
	onModel(t, vboxmanagetest.New(lab()))
	for _, check := range []bool{false, true} {
		rc := newRecorder()
		if res, err := call(t, "virt.vbox.vm.info", check, rc, map[string]any{"name": "no-such-vm"}); err != nil || res.Changed {
			t.Fatalf("check %v: %+v, %v", check, res, err)
		}
		if !reflect.DeepEqual(rc.stats, map[string]any{"exists": false}) {
			t.Errorf("check %v: stats = %v, want only exists: false", check, rc.stats)
		}
	}
}

// TestLookups_AMissingVMIsExistsFalseInBothModes: addresses, the log and
// the host keys of a VM the host does not have are exists false and
// nothing else, in a run and in a check alike, as info answers it (Phase
// 131, R4); host_keys waits for nothing.
func TestLookups_AMissingVMIsExistsFalseInBothModes(t *testing.T) {
	onModel(t, vboxmanagetest.New(lab()))
	for _, fqcn := range []string{"virt.vbox.vm.addresses", "virt.vbox.vm.log", "virt.vbox.vm.host_keys"} {
		for _, check := range []bool{false, true} {
			rc := newRecorder()
			if res, err := call(t, fqcn, check, rc, map[string]any{"name": "no-such-vm", "timeout": 1}); err != nil || res.Changed {
				t.Fatalf("%s, check %v: %+v, %v", fqcn, check, res, err)
			}
			if !reflect.DeepEqual(rc.stats, map[string]any{"exists": false}) {
				t.Errorf("%s, check %v: stats = %v, want only exists: false", fqcn, check, rc.stats)
			}
		}
	}
}

func TestInfo_Failures(t *testing.T) {
	model := vboxmanagetest.New(lab())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"}}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.info", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("a failed read: %v", err)
	}
	model.Fail = nil
	rc := newRecorder()
	rc.failOn = "state"
	if _, err := call(t, "virt.vbox.vm.info", false, rc, map[string]any{"name": "ubuntu-lab"}); err == nil {
		t.Error("a stat that could not be recorded was not a failure")
	}
}

// TestRefusedBeforeTheHost covers what each method refuses from its
// parameters alone: nothing reaches the host.
func TestRefusedBeforeTheHost(t *testing.T) {
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	for _, fqcn := range []string{"virt.vbox.vm.info", "virt.vbox.vm.start", "virt.vbox.vm.stop"} {
		for name, params := range map[string]map[string]any{
			"no name":        {},
			"a quoted name":  {"name": `lab" & calc`},
			"a name with /":  {"name": "../lab"},
			"a long name":    {"name": strings.Repeat("a", 64)},
			"a leading dash": {"name": "-lab"},
		} {
			if _, err := call(t, fqcn, false, newRecorder(), params); err == nil || !strings.Contains(err.Error(), fqcn) {
				t.Errorf("%s, %s: err = %v, want a refusal naming the method", fqcn, name, err)
			}
		}
	}
	for name, params := range map[string]map[string]any{
		"an unknown mode": {"name": "ubuntu-lab", "mode": "savestate"},
		"a zero timeout":  {"name": "ubuntu-lab", "timeout": 0},
		"a text timeout":  {"name": "ubuntu-lab", "timeout": "soon"},
		"a negative wait": {"name": "ubuntu-lab", "timeout": -5},
	} {
		if _, err := call(t, "virt.vbox.vm.stop", false, newRecorder(), params); err == nil {
			t.Errorf("stop, %s: accepted", name)
		}
	}
	if calls := model.Calls(); len(calls) != 0 {
		t.Errorf("refused calls reached the host: %v", calls)
	}
}

// TestTheRealHostNeedsTheCapability runs the real hostFunc, which builds
// a host only from a device that declares VirtualBox.
func TestTheRealHostNeedsTheCapability(t *testing.T) {
	plain := &inventorytest.Stub{StubName: "plain", Caps: []capability.Name{capability.NameWinRM}}
	d, _ := collection.Lookup("virt.vbox.vm.info")
	_, err := d.Invoke(t.Context(), newRecorder(), plain, map[string]any{"name": "ubuntu-lab"})
	if err == nil || !strings.Contains(err.Error(), "virtualbox: true") {
		t.Errorf("err = %v, want the missing capability named", err)
	}
}
