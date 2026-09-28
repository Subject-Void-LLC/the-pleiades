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
