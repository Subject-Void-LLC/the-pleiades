// Package ansiblefacts: tests of AnsibleNames and FilterKeys, what the
// playbook converter expands a setup filter against and into.
package ansiblefacts

import (
	"reflect"
	"slices"
	"testing"
)

func TestAnsibleNamesAreEveryMappedNameSorted(t *testing.T) {
	names := AnsibleNames()
	if len(names) != len(ansibleByNative) || !slices.IsSorted(names) {
		t.Fatalf("AnsibleNames() = %v; want the %d mapped names sorted", names, len(ansibleByNative))
	}
	for _, name := range names {
		if _, ok := NativeKey(name); !ok {
			t.Errorf("%s has no native key", name)
		}
	}
}

func TestFilterKeysKeepWhatToAnsibleReadsOnEveryHost(t *testing.T) {
	for _, tc := range []struct {
		ansible string
		want    []string
	}{
		{"ansible_distribution", []string{"os.distribution", "os.edition"}},
		{"ansible_swaptotal_mb", []string{"hardware.memory_mb", "hardware.swap_mb"}},
		{"ansible_os_family", []string{"os.family"}},
		{"ansible_default_ipv4", nil},
	} {
		if got := FilterKeys(tc.ansible); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FilterKeys(%s) = %v; want %v", tc.ansible, got, tc.want)
		}
	}
	// Measured against ToAnsible: a Windows host filtered to the
	// distribution's keys still gives Ansible's distribution, its caption.
	windows := map[string]any{"os.distribution": "windows", "os.edition": "Microsoft Windows Server 2022 Datacenter",
		"hardware.memory_mb": int64(4096), "os.family": "windows", "os.kernel.name": "windows"}
	kept := map[string]any{}
	for _, key := range FilterKeys("ansible_distribution") {
		kept[key] = windows[key]
	}
	if got := ToAnsible(kept)["ansible_distribution"]; got != windows["os.edition"] {
		t.Errorf("a Windows host filtered to %v gives ansible_distribution %v, want its caption", FilterKeys("ansible_distribution"), got)
	}
	// And a Windows host whose filter keeps swap and a fact naming Windows
	// gives Ansible's 0, which ToAnsible gives only beside memory.
	kept = map[string]any{"os.kernel.name": "windows"}
	for _, key := range FilterKeys("ansible_swaptotal_mb") {
		if v, ok := windows[key]; ok {
			kept[key] = v
		}
	}
	if got := ToAnsible(kept)["ansible_swaptotal_mb"]; got != 0 {
		t.Errorf("a Windows host filtered to %v gives ansible_swaptotal_mb %v, want 0", FilterKeys("ansible_swaptotal_mb"), got)
	}
}
