// Tests for virt.vbox.vm.stop against the model host.
package vm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// startedByTheService is a VM virt.vbox.vm.start started through the
// autostart service, so running and still marked.
func startedByTheService() *vboxmanagetest.VM {
	vm := lab()
	vm.State = vboxmanage.StateRunning
	vm.Autostart = true
	return vm
}

func TestStop_PowerButton(t *testing.T) {
	vm := startedByTheService()
	vm.PowerButtonReads = 3
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.stop", false, rc, map[string]any{"name": "ubuntu-lab"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if vm.State != vboxmanage.StatePoweroff || vm.Autostart {
		t.Errorf("the VM is %s with autostart %v", vm.State, vm.Autostart)
	}
	before, after := rc.diff(t)
	if !reflect.DeepEqual(before, map[string]any{"state": "running", "autostart_enabled": true}) ||
		!reflect.DeepEqual(after, map[string]any{"state": "poweroff", "autostart_enabled": false}) {
		t.Errorf("diff = %v -> %v", before, after)
	}
	want := map[string]any{"fqcn": "virt.vbox.vm.start", "params": map[string]any{"name": "ubuntu-lab"},
		"description": "Start ubuntu-lab again, as it was running before this task stopped it."}
	if !reflect.DeepEqual(rc.stats[sdk.StatInverse], want) {
		t.Errorf("inverse = %v", rc.stats[sdk.StatInverse])
	}
	if got := changes(model); !reflect.DeepEqual(got, []string{
		"VBoxManage.exe controlvm ubuntu-lab acpipowerbutton",
		"VBoxManage.exe modifyvm ubuntu-lab --autostart-enabled off --autostart-delay 0",
	}) {
		t.Errorf("changes = %v", got)
	}
}

func TestStop_PowerOff(t *testing.T) {
	vm := startedByTheService()
	vm.PowerButtonReads = -1
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.stop", false, rc, map[string]any{"name": "ubuntu-lab", "mode": "poweroff"}); err != nil {
		t.Fatal(err)
	}
	if vm.State != vboxmanage.StatePoweroff || rc.stats["state"] != "poweroff" {
		t.Errorf("the VM is %s, stats %v", vm.State, rc.stats)
	}
	paused := lab()
	paused.State = vboxmanage.StatePaused
	onModel(t, vboxmanagetest.New(paused))
	if _, err := call(t, "virt.vbox.vm.stop", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "mode": "poweroff"}); err != nil || paused.State != vboxmanage.StatePoweroff {
		t.Errorf("a paused VM's power cut: %v, %s", err, paused.State)
	}
}

func TestStop_AGuestThatIgnoresTheButton(t *testing.T) {
	vm := startedByTheService()
	vm.PowerButtonReads = -1
	onModel(t, vboxmanagetest.New(vm))
	_, err := call(t, "virt.vbox.vm.stop", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "timeout": 1})
	if err == nil || !strings.Contains(err.Error(), "mode: poweroff") {
		t.Fatalf("err = %v, want the way out named", err)
	}
	if vm.State != vboxmanage.StateRunning {
		t.Errorf("the power was cut after all: %s", vm.State)
	}
}

func TestStop_AlreadyOff(t *testing.T) {
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.stop", false, rc, map[string]any{"name": "ubuntu-lab"})
	if err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok || len(changes(model)) != 0 {
		t.Errorf("an unchanged stop acted or recorded an inverse: %v, %v", rc.stats, changes(model))
	}
}

// TestStop_ClearsAMarkLeftOnAStoppedVM covers a VM that stopped on its own
// while marked: the stop changes only the mark, and records no inverse,
// since starting it again is not what undoing that means.
func TestStop_ClearsAMarkLeftOnAStoppedVM(t *testing.T) {
	vm := lab()
	vm.Autostart = true
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.stop", false, rc, map[string]any{"name": "ubuntu-lab"})
	if err != nil || !result.Changed || vm.Autostart {
		t.Fatalf("%+v, %v, autostart %v", result, err, vm.Autostart)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok {
		t.Error("clearing only the mark recorded an inverse")
	}
}

func TestStop_Check(t *testing.T) {
	vm := startedByTheService()
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	result, err := call(t, "virt.vbox.vm.stop", true, rc, map[string]any{"name": "ubuntu-lab", "mode": "poweroff"})
	if err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if _, after := rc.diff(t); !reflect.DeepEqual(after, map[string]any{"state": "poweroff", "autostart_enabled": false}) {
		t.Errorf("predicted %v", after)
	}
	if _, ok := rc.stats[sdk.StatInverse]; ok || len(changes(model)) != 0 || vm.State != vboxmanage.StateRunning {
		t.Errorf("a check acted: %v, %s", changes(model), vm.State)
	}
}

func TestStop_Refusals(t *testing.T) {
	paused := lab()
	paused.State = vboxmanage.StatePaused
	onModel(t, vboxmanagetest.New(paused))
	if _, err := call(t, "virt.vbox.vm.stop", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "use mode: poweroff") {
		t.Errorf("a paused VM and the power button: %v", err)
	}
	if _, err := call(t, "virt.vbox.vm.stop", false, newRecorder(), map[string]any{"name": "no-such-vm"}); err == nil || !strings.Contains(err.Error(), "no VM named") {
		t.Errorf("a missing VM: %v", err)
	}
	for key, out := range map[string]vboxmanage.Output{
		"VBoxManage.exe controlvm ubuntu-lab poweroff":        {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"},
		"VBoxManage.exe controlvm ubuntu-lab acpipowerbutton": {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"},
		"VBoxManage.exe modifyvm":                             {ExitCode: 1, Stderr: "VBoxManage.exe: error: Code E_ACCESSDENIED\r\n"},
	} {
		model := vboxmanagetest.New(startedByTheService())
		model.Fail = map[string]vboxmanage.Output{key: out}
		onModel(t, model)
		mode := "acpi"
		if strings.HasSuffix(key, "poweroff") {
			mode = "poweroff"
		}
		if _, err := call(t, "virt.vbox.vm.stop", false, newRecorder(), map[string]any{"name": "ubuntu-lab", "mode": mode}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
			t.Errorf("%s failing: %v", key, err)
		}
	}
	for _, stat := range []string{"state", sdk.StatDiff, sdk.StatInverse} {
		onModel(t, vboxmanagetest.New(startedByTheService()))
		rc := newRecorder()
		rc.failOn = stat
		if _, err := call(t, "virt.vbox.vm.stop", false, rc, map[string]any{"name": "ubuntu-lab", "mode": "poweroff"}); err == nil {
			t.Errorf("recording %s failed and the stop did not", stat)
		}
	}
}
