// Tests for the properties that make a windows_server VirtualBox-capable.
package windows_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func TestServer_DeclaresVirtualBoxOnlyWhenTheRecordSaysSo(t *testing.T) {
	plain, err := newServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.HasCapability(capability.NameVirtualBox) {
		t.Error("a server whose record says nothing about VirtualBox declared it")
	}
	// The method still answers on a server that did not declare the
	// capability, with where the installer puts VBoxManage.
	if got := plain.(capability.VirtualBoxCapable).VBoxManagePath(); got != `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe` {
		t.Errorf("an undeclared server's VBoxManagePath = %q", got)
	}
	off, err := newServer(map[string]inventory.PropertyValue{"virtualbox": false})
	if err != nil {
		t.Fatal(err)
	}
	if off.HasCapability(capability.NameVirtualBox) {
		t.Error("virtualbox: false declared it")
	}

	on, err := newServer(map[string]inventory.PropertyValue{"virtualbox": true})
	if err != nil {
		t.Fatal(err)
	}
	if !on.HasCapability(capability.NameVirtualBox) {
		t.Fatal("virtualbox: true did not declare VirtualBoxCapable")
	}
	vbox := on.(capability.VirtualBoxCapable)
	if vbox.VBoxManagePath() != `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe` || vbox.VMFolder() != "" {
		t.Errorf("defaults = %q, %q", vbox.VBoxManagePath(), vbox.VMFolder())
	}
	// It is still a Windows server: the capabilities it had are all there.
	for _, name := range []capability.Name{capability.NameWinRM, capability.NameWindowsShell} {
		if !on.HasCapability(name) {
			t.Errorf("declaring VirtualBox lost %s", name)
		}
	}

	custom, err := newServer(map[string]inventory.PropertyValue{
		"virtualbox": true, "vboxmanage_path": `D:\VirtualBox\VBoxManage.exe`, "vm_folder": `G:\PleiadesLab`,
	})
	if err != nil {
		t.Fatal(err)
	}
	vbox = custom.(capability.VirtualBoxCapable)
	if vbox.VBoxManagePath() != `D:\VirtualBox\VBoxManage.exe` || vbox.VMFolder() != `G:\PleiadesLab` {
		t.Errorf("overrides = %q, %q", vbox.VBoxManagePath(), vbox.VMFolder())
	}
}

func TestServer_RefusesVirtualBoxSettingsThatCannotWork(t *testing.T) {
	for name, tt := range map[string]struct {
		props map[string]inventory.PropertyValue
		want  string
	}{
		"not a boolean":             {map[string]inventory.PropertyValue{"virtualbox": "yes"}, "true or false"},
		"a path without the flag":   {map[string]inventory.PropertyValue{"vboxmanage_path": `C:\x.exe`}, "only with virtualbox: true"},
		"a folder without the flag": {map[string]inventory.PropertyValue{"virtualbox": false, "vm_folder": `G:\vms`}, "only with virtualbox: true"},
		"a relative path":           {map[string]inventory.PropertyValue{"virtualbox": true, "vboxmanage_path": `VBoxManage.exe`}, "absolute path"},
		"a quote in the path":       {map[string]inventory.PropertyValue{"virtualbox": true, "vboxmanage_path": `C:\a"b\VBoxManage.exe`}, "quote"},
		"a percent in the folder":   {map[string]inventory.PropertyValue{"virtualbox": true, "vm_folder": `C:\%TEMP%\vms`}, "quote"},
		"a line break":              {map[string]inventory.PropertyValue{"virtualbox": true, "vm_folder": "C:\\vms\nC:\\other"}, "control character"},
	} {
		_, err := newServer(tt.props)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, tt.want)
		}
	}
}

// TestServer_RefusesAMachineFolderThatClimbsOut: vm_folder and
// vboxmanage_path are held, at the record's write, to the rule every host
// path the virt.vbox.* methods send is held to (vboxmanage.CheckPath): no
// segment of only dots and spaces between either separator, no doubled
// separator, no wildcard or apostrophe, nothing a WS-Man message cannot
// carry. Until 2026-11-13 the record took G:\Lab\..\Windows as a machine
// folder, refused only later, when a prune or a clone sent it.
func TestServer_RefusesAMachineFolderThatClimbsOut(t *testing.T) {
	for _, key := range []string{"vm_folder", "vboxmanage_path"} {
		for _, value := range []string{
			`G:\Lab\..\Windows`, `G:\Lab\..`, `G:\Lab/..`, `G:\Lab/../Windows`, `G:\Lab\.\vms`, `G:\Lab\...\vms`,
			`G:\Lab\\vms`, `G:\Lab\*`, `G:\Lab\it's`, "G:\\Lab\\a\uFFFEb", `G:\Lab\`,
		} {
			if _, err := newServer(map[string]inventory.PropertyValue{"virtualbox": true, key: value}); err == nil {
				t.Errorf("%s %q was accepted", key, value)
			}
		}
	}
	// Control: an ordinary folder with a space and a dot in its names.
	if _, err := newServer(map[string]inventory.PropertyValue{"virtualbox": true, "vm_folder": `D:\VirtualBox VMs\lab.v2`}); err != nil {
		t.Errorf("an ordinary folder was refused: %v", err)
	}
}
