// Tests for virt.vbox.vm.start against a model host that refuses a plain
// start when none of the account's VMs runs, as the real one does.
package vm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

func TestStart_ThroughTheAutostartService(t *testing.T) {
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.start", false, rc, map[string]any{"name": "ubuntu-lab"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if model.VM("ubuntu-lab").State != vboxmanage.StateRunning {
		t.Fatalf("the VM is %s", model.VM("ubuntu-lab").State)
	}
	if rc.stats["state"] != "running" || rc.stats["via_autostart_service"] != true {
		t.Errorf("stats = %v", rc.stats)
	}
	before, after := rc.diff(t)
	if !reflect.DeepEqual(before, map[string]any{"state": "poweroff", "autostart_enabled": false}) ||
		!reflect.DeepEqual(after, map[string]any{"state": "running", "autostart_enabled": true}) {
		t.Errorf("diff = %v -> %v", before, after)
	}
	want := map[string]any{"fqcn": "virt.vbox.vm.stop", "params": map[string]any{"name": "ubuntu-lab"},
		"description": "Shut ubuntu-lab down by its power button, as it was before this task started it."}
	if !reflect.DeepEqual(rc.stats[sdk.StatInverse], want) {
		t.Errorf("inverse = %v", rc.stats[sdk.StatInverse])
	}
	for _, c := range model.Calls() {
		if strings.HasPrefix(c, "VBoxManage.exe startvm") {
			t.Errorf("a plain start was tried, which the host refuses with nothing running: %s", c)
		}
	}
}

func TestStart_PlainWhileAnotherRuns(t *testing.T) {
	model := vboxmanagetest.New(lab(), running())
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.start", false, rc, map[string]any{"name": "ubuntu-lab"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if rc.stats["via_autostart_service"] != false || model.VM("ubuntu-lab").Autostart {
		t.Errorf("a plain start went through the service, or marked the VM: %v", rc.stats)
	}
	if got := changes(model); !reflect.DeepEqual(got, []string{"VBoxManage.exe startvm ubuntu-lab --type headless"}) {
		t.Errorf("changes = %v", got)
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	model := vboxmanagetest.New(running())
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.start", false, rc, map[string]any{"name": "other"})
	if err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok {
		t.Error("an unchanged start recorded an inverse")
	}
	before, after := rc.diff(t)
	if !reflect.DeepEqual(before, after) || rc.stats["via_autostart_service"] != false {
		t.Errorf("diff = %v -> %v, stats %v", before, after, rc.stats)
	}
	if got := changes(model); len(got) != 0 {
		t.Errorf("changes = %v", got)
	}
}

func TestStart_Check(t *testing.T) {
	for name, tt := range map[string]struct {
		vms       []*vboxmanagetest.VM
		via       bool
		autostart bool
	}{
		"nothing running": {[]*vboxmanagetest.VM{lab()}, true, true},
		"another running": {[]*vboxmanagetest.VM{lab(), running()}, false, false},
	} {
		model := vboxmanagetest.New(tt.vms...)
		onModel(t, model)
		rc := newRecorder()
		result, err := call(t, "virt.vbox.vm.start", true, rc, map[string]any{"name": "ubuntu-lab"})
		if err != nil || !result.Changed {
			t.Fatalf("%s: %+v, %v", name, result, err)
		}
		_, after := rc.diff(t)
		if rc.stats["via_autostart_service"] != tt.via || after["autostart_enabled"] != tt.autostart || after["state"] != "running" {
			t.Errorf("%s: stats %v, after %v", name, rc.stats, after)
		}
		if _, ok := rc.stats[sdk.StatInverse]; ok {
			t.Errorf("%s: a check recorded an inverse", name)
		}
		if got := changes(model); len(got) != 0 || model.VM("ubuntu-lab").State != vboxmanage.StatePoweroff {
			t.Errorf("%s: a check changed something: %v", name, got)
		}
	}
}

func TestStart_Refusals(t *testing.T) {
	paused := lab()
	paused.State = vboxmanage.StatePaused
	onModel(t, vboxmanagetest.New(paused))
	if _, err := call(t, "virt.vbox.vm.start", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Errorf("a paused VM: %v", err)
	}
	if _, err := call(t, "virt.vbox.vm.start", false, newRecorder(), map[string]any{"name": "no-such-vm"}); err == nil ||
		!strings.Contains(err.Error(), `host "vengeance" has no VM named "no-such-vm"`) {
		t.Errorf("a missing VM: %v", err)
	}
	model := vboxmanagetest.New(lab())
	model.NoAutostartService = true
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.start", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "1060") {
		t.Errorf("no autostart service: %v", err)
	}
	model = vboxmanagetest.New(lab(), running())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list runningvms": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"}}
	onModel(t, model)
	for _, check := range []bool{false, true} {
		if _, err := call(t, "virt.vbox.vm.start", check, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
			t.Errorf("check %v, the running list failing: %v", check, err)
		}
	}
	model.Fail = nil
	for _, stat := range []string{"state", "via_autostart_service", sdk.StatDiff, sdk.StatInverse} {
		model.VM("ubuntu-lab").State = vboxmanage.StatePoweroff
		rc := newRecorder()
		rc.failOn = stat
		if _, err := call(t, "virt.vbox.vm.start", false, rc, map[string]any{"name": "ubuntu-lab"}); err == nil {
			t.Errorf("recording %s failed and the start did not", stat)
		}
	}
}
