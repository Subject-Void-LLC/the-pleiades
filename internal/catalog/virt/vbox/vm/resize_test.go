// Tests for sizing VMs: virt.vbox.vm.resize, the size a clone takes and
// the host it must fit, and the size info and list report, against the
// model host.
package vm

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

func TestResize_Registered(t *testing.T) {
	d, ok := collection.Lookup("virt.vbox.vm.resize")
	if !ok {
		t.Fatal("virt.vbox.vm.resize is not registered")
	}
	m := d.Manifest
	if m.Status != collection.StatusImplemented || !m.SupportsCheck || d.Check == nil || !m.Reversibility.Reversible {
		t.Errorf("status %v, check %v, reversible %v", m.Status, m.SupportsCheck, m.Reversibility.Reversible)
	}
}

// TestResize_BySize proves a check predicts the change and sends nothing,
// and a run gives the VM the size's CPUs and memory, reads them back, and
// records the numbers it had as the inverse, which puts them back.
func TestResize_BySize(t *testing.T) {
	model := vboxmanagetest.New(lab())
	onModel(t, model)
	params := map[string]any{"name": "ubuntu-lab", "size": "medium"}
	check := newRecorder()
	result, err := call(t, "virt.vbox.vm.resize", true, check, params)
	if err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	if check.stats["size"] != "medium" || check.stats["memory_mb"] != 4096 {
		t.Errorf("check stats %v", check.stats)
	}
	rc := newRecorder()
	if result, err := call(t, "virt.vbox.vm.resize", false, rc, params); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	vm := model.VM("ubuntu-lab")
	if vm.MemoryMB != 4096 || vm.CPUs != 2 {
		t.Fatalf("the VM has %d MB and %d CPUs", vm.MemoryMB, vm.CPUs)
	}
	before, after := rc.diff(t)
	if !reflect.DeepEqual(before, map[string]any{"memory_mb": 1024, "cpus": 2, "size": ""}) ||
		!reflect.DeepEqual(after, map[string]any{"memory_mb": 4096, "cpus": 2, "size": "medium"}) {
		t.Errorf("diff %v -> %v", before, after)
	}
	want := map[string]any{"fqcn": "virt.vbox.vm.resize", "params": map[string]any{"name": "ubuntu-lab", "memory_mb": 1024, "cpus": 2},
		"description": "Give ubuntu-lab back the 2 CPUs and 1024 MB it had before this task."}
	inverse := rc.stats[sdk.StatInverse]
	if !reflect.DeepEqual(inverse, want) {
		t.Fatalf("inverse %v\nwant %v", inverse, want)
	}
	if _, err := call(t, "virt.vbox.vm.resize", false, newRecorder(), want["params"].(map[string]any)); err != nil {
		t.Fatal(err)
	}
	if vm := model.VM("ubuntu-lab"); vm.MemoryMB != 1024 || vm.CPUs != 2 {
		t.Errorf("the inverse left %d MB and %d CPUs", vm.MemoryMB, vm.CPUs)
	}
}

// TestResize_OneNumberKeepsTheOther proves memory_mb alone leaves the
// CPUs as they are, and an aborted VM, which runs nothing, is resized.
func TestResize_OneNumberKeepsTheOther(t *testing.T) {
	vm := lab()
	vm.State = vboxmanage.StateAborted
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.resize", false, rc, map[string]any{"name": "ubuntu-lab", "memory_mb": 2048}); err != nil {
		t.Fatal(err)
	}
	if vm := model.VM("ubuntu-lab"); vm.MemoryMB != 2048 || vm.CPUs != 2 || rc.stats["size"] != "" {
		t.Errorf("%d MB, %d CPUs, stats %v", vm.MemoryMB, vm.CPUs, rc.stats)
	}
}

// TestResize_AlreadyThatSize proves a VM already that shape reports no
// change and sends nothing, even while it runs.
func TestResize_AlreadyThatSize(t *testing.T) {
	vm := lab()
	vm.State, vm.MemoryMB, vm.CPUs = vboxmanage.StateRunning, 2048, 1
	model := vboxmanagetest.New(vm)
	onModel(t, model)
	for _, check := range []bool{true, false} {
		rc := newRecorder()
		result, err := call(t, "virt.vbox.vm.resize", check, rc, map[string]any{"name": "ubuntu-lab", "size": "small"})
		if err != nil || result.Changed || rc.stats["size"] != "small" || rc.stats[sdk.StatInverse] != nil {
			t.Fatalf("check %v: %+v, %v, %v", check, result, err, rc.stats)
		}
	}
	if c := changes(model); len(c) != 0 {
		t.Errorf("calls %v", c)
	}
}

func TestResize_Refusals(t *testing.T) {
	inState := func(state string) *vboxmanagetest.VM {
		vm := lab()
		vm.State = state
		return vm
	}
	for why, tt := range map[string]struct {
		vm     *vboxmanagetest.VM
		params map[string]any
		want   string
	}{
		"nothing asked":             {lab(), map[string]any{}, "give size, or memory_mb, cpus or both"},
		"a size and a number":       {lab(), map[string]any{"size": "small", "cpus": 2}, "not both"},
		"an unknown size":           {lab(), map[string]any{"size": "huge"}, "xsmall, small, medium, large, xlarge"},
		"a size that is not a name": {lab(), map[string]any{"size": 4}, "size must be one of"},
		"no CPUs":                   {lab(), map[string]any{"cpus": 0}, "cpus must be positive"},
		"no memory":                 {lab(), map[string]any{"memory_mb": -1}, "memory_mb must be positive"},
		"text memory":               {lab(), map[string]any{"memory_mb": "lots"}, "memory_mb"},
		"text CPUs":                 {lab(), map[string]any{"cpus": "two"}, "cpus"},
		"more CPUs than the host":   {lab(), map[string]any{"cpus": 21}, "it has 20 processors online"},
		"more memory than the host": {lab(), map[string]any{"memory_mb": 65536}, "it has 32388 MB of memory"},
		"a running VM":              {inState(vboxmanage.StateRunning), map[string]any{"size": "small"}, "stop it first with virt.vbox.vm.stop"},
		"a paused VM":               {inState(vboxmanage.StatePaused), map[string]any{"size": "small"}, `"ubuntu-lab" is paused`},
		"a saved VM":                {inState(vboxmanage.StateSaved), map[string]any{"size": "small"}, `"ubuntu-lab" is saved`},
		"a missing VM":              {lab(), map[string]any{"name": "missing", "size": "small"}, `has no VM named "missing"`},
	} {
		model := vboxmanagetest.New(tt.vm)
		onModel(t, model)
		params := map[string]any{"name": "ubuntu-lab"}
		for k, v := range tt.params {
			params[k] = v
		}
		if _, err := call(t, "virt.vbox.vm.resize", false, newRecorder(), params); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
		if c := changes(model); len(c) != 0 {
			t.Errorf("%s: calls %v", why, c)
		}
	}
}

// TestResize_CheckCannotSeeAnEarlierStop proves a check of a running or
// paused VM, or of one not made yet, is unchecked rather than failed,
// since an earlier task may stop or make it, while a saved VM, which a
// stop leaves saved, fails the check as it would the run.
func TestResize_CheckCannotSeeAnEarlierStop(t *testing.T) {
	var cannot *collection.CannotCheckError
	for _, state := range []string{vboxmanage.StateRunning, vboxmanage.StatePaused} {
		vm := lab()
		vm.State = state
		onModel(t, vboxmanagetest.New(vm))
		if _, err := call(t, "virt.vbox.vm.resize", true, newRecorder(), map[string]any{"name": "ubuntu-lab", "size": "small"}); !errors.As(err, &cannot) {
			t.Errorf("a %s VM: %v", state, err)
		}
	}
	if _, err := call(t, "virt.vbox.vm.resize", true, newRecorder(), map[string]any{"name": "missing", "size": "small"}); !errors.As(err, &cannot) {
		t.Errorf("a VM not made yet: %v", err)
	}
	vm := lab()
	vm.State = vboxmanage.StateSaved
	onModel(t, vboxmanagetest.New(vm))
	if _, err := call(t, "virt.vbox.vm.resize", true, newRecorder(), map[string]any{"name": "ubuntu-lab", "size": "small"}); err == nil || errors.As(err, &cannot) {
		t.Errorf("a saved VM: %v", err)
	}
}

func TestResize_Failures(t *testing.T) {
	params := map[string]any{"name": "ubuntu-lab", "size": "small"}
	for why, fail := range map[string]map[string]vboxmanage.Output{
		"hostinfo fails":        {"VBoxManage.exe list hostinfo": {ExitCode: 1, Stderr: "VBoxManage.exe: error: E_ACCESSDENIED\r\n"}},
		"hostinfo is garbled":   {"VBoxManage.exe list hostinfo": {Stdout: "Host Information:\r\n"}},
		"modifyvm fails":        {"VBoxManage.exe modifyvm": {ExitCode: 1, Stderr: "VBoxManage.exe: error: E_ACCESSDENIED\r\n"}},
		"modifyvm changes none": {"VBoxManage.exe modifyvm": {}},
	} {
		model := vboxmanagetest.New(lab())
		model.Fail = fail
		onModel(t, model)
		if _, err := call(t, "virt.vbox.vm.resize", false, newRecorder(), params); err == nil {
			t.Errorf("%s: no error", why)
		}
	}
	model := vboxmanagetest.New(lab())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe showvminfo": {ExitCode: 1, Stderr: "VBoxManage.exe: error: E_ACCESSDENIED\r\n"}}
	model.Skip = map[string]int{"VBoxManage.exe showvminfo": 1}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.resize", false, newRecorder(), params); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("reading it back: %v", err)
	}
	for _, stat := range []string{"size", sdk.StatDiff, sdk.StatInverse} {
		onModel(t, vboxmanagetest.New(lab()))
		rc := newRecorder()
		rc.failOn = stat
		if _, err := call(t, "virt.vbox.vm.resize", false, rc, params); err == nil {
			t.Errorf("recording %s failed and the resize did not", stat)
		}
	}
	if err := (vboxmanagetest.New().VBoxHost()).Resize(t.Context(), "ubuntu-lab", 2, 1); err == nil {
		t.Error("2 MB was sent as a machine")
	}
	if err := (vboxmanagetest.New().VBoxHost()).Resize(t.Context(), "bad name", 1024, 1); err == nil {
		t.Error("a bad name was sent")
	}
}

// TestClone_BySize proves a clone takes a size's CPUs and memory, xsmall's
// when it is asked for none, and reports the size it made.
func TestClone_BySize(t *testing.T) {
	for size, want := range map[string][2]int{"small": {2048, 1}, "medium": {4096, 2}, "": {1024, 1}} {
		model := vboxmanagetest.New(base())
		onModel(t, model)
		params := cloneParams()
		delete(params, "memory_mb")
		delete(params, "cpus")
		if size != "" {
			params["size"] = size
		}
		for _, check := range []bool{true, false} {
			rc := seeded()
			if _, err := call(t, "virt.vbox.vm.clone", check, rc, params); err != nil {
				t.Fatalf("%q check %v: %v", size, check, err)
			}
			wantSize := size
			if size == "" {
				wantSize = "xsmall"
			}
			if rc.stats["size"] != wantSize {
				t.Errorf("%q check %v: size %v", size, check, rc.stats["size"])
			}
		}
		vm := model.VM("ubuntu-lab")
		if vm.MemoryMB != want[0] || vm.CPUs != want[1] {
			t.Errorf("%q: %d MB and %d CPUs", size, vm.MemoryMB, vm.CPUs)
		}
	}
}

// TestClone_TooBigForTheHost proves a size the host cannot run is refused
// before anything is cloned, in a check as in a run.
func TestClone_TooBigForTheHost(t *testing.T) {
	model := vboxmanagetest.New(base())
	model.CPUs, model.MemoryMB, model.AvailableMB = 4, 8192, 4096
	onModel(t, model)
	for params, want := range map[string]string{"xlarge": "it has 4 processors online", "large": "it has 8192 MB of memory"} {
		p := cloneParams()
		delete(p, "memory_mb")
		delete(p, "cpus")
		p["size"] = params
		if params == "large" {
			delete(p, "size")
			p["memory_mb"], p["cpus"] = 16384, 2
		}
		for _, check := range []bool{true, false} {
			if _, err := call(t, "virt.vbox.vm.clone", check, seeded(), p); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s check %v: %v, want one mentioning %q", params, check, err, want)
			}
		}
	}
	if c := changes(model); len(c) != 0 {
		t.Errorf("calls %v", c)
	}
	p := cloneParams()
	p["size"] = "small"
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), p); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("a size beside memory_mb and cpus: %v", err)
	}
}

// TestClone_FoundAnotherSize proves a clone that finds a VM under the
// name changes nothing, and warns when it was asked for other CPUs or
// memory than the VM has, but not when it was asked for none.
func TestClone_FoundAnotherSize(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), cloneParams()); err != nil {
		t.Fatal(err)
	}
	params := cloneParams()
	delete(params, "memory_mb")
	delete(params, "cpus")
	params["size"] = "large"
	rc := seeded()
	if result, err := call(t, "virt.vbox.vm.clone", false, rc, params); err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	want := `"ubuntu-lab" already exists with 2 CPUs and 2048 MB, not the large (4 CPUs, 8192 MB) asked for`
	if len(warnings) != 1 || !strings.Contains(warnings[0], want) || rc.stats["size"] != "" {
		t.Errorf("warnings %q, stats %v", warnings, rc.stats)
	}
	delete(params, "size")
	rc = seeded()
	if _, err := call(t, "virt.vbox.vm.clone", false, rc, params); err != nil || rc.stats[sdk.StatWarnings] != nil {
		t.Errorf("asked for no size: %v, %v", err, rc.stats)
	}
	if vm := model.VM("ubuntu-lab"); vm.MemoryMB != 2048 || vm.CPUs != 2 {
		t.Errorf("the found VM was changed: %d MB, %d CPUs", vm.MemoryMB, vm.CPUs)
	}
	for _, stat := range []string{"size", sdk.StatWarnings} {
		rc := seeded()
		rc.failOn = stat
		p := cloneParams()
		p["cpus"] = 4
		if _, err := call(t, "virt.vbox.vm.clone", false, rc, p); err == nil {
			t.Errorf("recording %s failed and the clone did not", stat)
		}
	}
}

// TestStart_NoRoomOnTheHost proves a VM the host's free memory cannot
// hold, less what it keeps for itself, is refused in a check as in a run,
// and one that fits is started.
func TestStart_NoRoomOnTheHost(t *testing.T) {
	model := vboxmanagetest.New(lab())
	model.AvailableMB = 2047
	onModel(t, model)
	for _, check := range []bool{true, false} {
		_, err := call(t, "virt.vbox.vm.start", check, newRecorder(), map[string]any{"name": "ubuntu-lab"})
		if err == nil || !strings.Contains(err.Error(), `"ubuntu-lab" needs 1024 MB and the host has 2047 MB free, of which it keeps 1024 MB for itself`) {
			t.Errorf("check %v: %v", check, err)
		}
	}
	if c := changes(model); len(c) != 0 {
		t.Errorf("calls %v", c)
	}
	model.AvailableMB = 2048
	if _, err := call(t, "virt.vbox.vm.start", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err != nil {
		t.Errorf("a VM that fits: %v", err)
	}
	model = vboxmanagetest.New(lab())
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list hostinfo": {ExitCode: 1, Stderr: "VBoxManage.exe: error: E_ACCESSDENIED\r\n"}}
	onModel(t, model)
	if _, err := call(t, "virt.vbox.vm.start", false, newRecorder(), map[string]any{"name": "ubuntu-lab"}); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("hostinfo failing: %v", err)
	}
}

// TestSizeReported proves info and list name a VM's size when its CPUs and
// memory are one, and list leaves it out when they are not.
func TestSizeReported(t *testing.T) {
	sized := lab()
	sized.MemoryMB, sized.CPUs = 8192, 4
	other := running()
	onModel(t, vboxmanagetest.New(sized, other))
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.info", false, rc, map[string]any{"name": "ubuntu-lab"}); err != nil || rc.stats["size"] != "large" {
		t.Fatalf("info: %v, %v", err, rc.stats)
	}
	rc = newRecorder()
	if _, err := call(t, "virt.vbox.vm.list", false, rc, nil); err != nil {
		t.Fatal(err)
	}
	vms := rc.stats["vms"].([]any)
	if vms[0].(map[string]any)["size"] != "large" {
		t.Errorf("list: %v", vms[0])
	}
	if _, ok := vms[1].(map[string]any)["size"]; ok {
		t.Errorf("list gave a size to a VM with none: %v", vms[1])
	}
}

// TestClone_Paravirt proves paravirt_provider reaches the new VM's
// settings, and that a value VirtualBox does not name is refused before
// anything is made.
func TestClone_Paravirt(t *testing.T) {
	model := vboxmanagetest.New(base())
	onModel(t, model)
	params := cloneParams()
	params["paravirt_provider"] = "none"
	if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), params); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(changes(model), "\n"), "--paravirt-provider none") {
		t.Errorf("calls %v", changes(model))
	}
	for _, bad := range []any{"xen", 4, ""} {
		model := vboxmanagetest.New(base())
		onModel(t, model)
		params["paravirt_provider"] = bad
		if _, err := call(t, "virt.vbox.vm.clone", false, seeded(), params); err == nil || !strings.Contains(err.Error(), "is not one of default, legacy") || len(changes(model)) != 0 {
			t.Errorf("%v: %v, calls %v", bad, err, changes(model))
		}
	}
}
