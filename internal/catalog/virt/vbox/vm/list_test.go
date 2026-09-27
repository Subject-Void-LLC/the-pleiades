// Tests for virt.vbox.vm.list against the model host.
package vm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

func TestList(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err != nil {
		t.Fatal(err)
	}
	for _, check := range []bool{false, true} {
		rc := newRecorder()
		if result, err := call(t, "virt.vbox.vm.list", check, rc, nil); err != nil || result.Changed {
			t.Fatalf("check %v: %+v, %v", check, result, err)
		}
		vms := rc.stats["vms"].([]any)
		if len(vms) != 2 {
			t.Fatalf("vms %v", vms)
		}
		lab := vms[1].(map[string]any)
		want := map[string]any{"name": "ubuntu-lab", "uuid": model.VM("ubuntu-lab").UUID, "state": "poweroff", "memory_mb": 2048, "cpus": 2,
			"autostart_enabled": false, "address": "192.168.56.10", "device": "ubuntu-lab"}
		if !reflect.DeepEqual(lab, want) {
			t.Errorf("check %v: %v\nwant %v", check, lab, want)
		}
		if _, ok := vms[0].(map[string]any)["address"]; ok {
			t.Error("the imported base, which Pleiades gave no address, has one")
		}
	}
}

func TestList_Failures(t *testing.T) {
	odd := base()
	odd.Name = "a name with spaces"
	model := vboxmanagetest.New(odd)
	onModel(t, model)
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.list", false, rc, nil); err != nil {
		t.Fatal(err)
	}
	if got := rc.stats["vms"].([]any)[0].(map[string]any); len(got) != 2 || got["name"] != "a name with spaces" {
		t.Errorf("a VM whose name this package will not send: %v", got)
	}
	for key, want := range map[string]string{
		"VBoxManage.exe list":         "refused",
		"VBoxManage.exe showvminfo":   "refused",
		"VBoxManage.exe getextradata": "refused",
	} {
		model := vboxmanagetest.New(base())
		model.Fail = map[string]vboxmanage.Output{key: failure}
		onModel(t, model)
		if _, err := call(t, "virt.vbox.vm.list", false, newRecorder(), nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s failing: %v", key, err)
		}
	}
	onModel(t, vboxmanagetest.New(base()))
	failing := newRecorder()
	failing.failOn = "vms"
	if _, err := call(t, "virt.vbox.vm.list", false, failing, nil); err == nil {
		t.Error("recording the list failed and the method did not")
	}
}

// TestList_TheRealHostNeedsTheCapability runs the real host builder,
// which refuses a device that does not declare VirtualBox.
func TestList_TheRealHostNeedsTheCapability(t *testing.T) {
	if _, err := call(t, "virt.vbox.vm.list", false, newRecorder(), nil); err == nil || !strings.Contains(err.Error(), "virtualbox: true") {
		t.Errorf("err = %v", err)
	}
}
