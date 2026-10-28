// virt.vbox.vm.info's hardware half: the values showvminfo reports only
// sometimes are returned when it reports them and left out when it does
// not, and every key info records is one its Returns declares.
package vm

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// TestHardware_TheOptionalValues: a running guest's additions version, the
// state change time and the settings file come back when showvminfo says
// them, as the keys real captures carry (pkg/vboxmanage/testdata).
func TestHardware_TheOptionalValues(t *testing.T) {
	m := vboxmanage.Machine{OSType: "Ubuntu (64-bit)", Firmware: "BIOS", ConfigFile: `C:\vms\a\a.vbox`,
		Values: vboxmanage.Values{{Key: "VMStateChangeTime", Value: "2026-09-27T15:05:18.634000000"}, {Key: "GuestAdditionsVersion", Value: "6.0.0 r127566"}}}
	got := hardware(m)
	for k, want := range map[string]string{"config_file": `C:\vms\a\a.vbox`, "state_changed_at": "2026-09-27T15:05:18.634000000", "guest_additions_version": "6.0.0 r127566"} {
		if got[k] != want {
			t.Errorf("%s = %v, want %q", k, got[k], want)
		}
	}
	bare := hardware(vboxmanage.Machine{})
	for _, k := range []string{"config_file", "console_log", "state_changed_at", "guest_additions_version"} {
		if _, ok := bare[k]; ok {
			t.Errorf("%s reported when showvminfo said nothing of it", k)
		}
	}

	declared := map[string]bool{}
	desc, _ := collection.Lookup("virt.vbox.vm.info")
	for _, r := range desc.Manifest.Doc.Returns {
		declared[r.Name] = true
	}
	for _, k := range infoKeys {
		if !declared[k] {
			t.Errorf("info records %s, which its Returns does not declare", k)
		}
	}
}
