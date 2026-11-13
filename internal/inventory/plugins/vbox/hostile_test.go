// Hostile input to the vbox sync plugin: every VM name and every extradata
// value is text the VirtualBox host wrote, and none of it becomes a device,
// or a device's address, unless it is what the plugin takes.
package vbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestSync_HostileInputNeverBecomesADevice: a VM whose name VirtualBox's
// own rule refuses (vboxmanage.CheckName) is quarantined as unreadable and
// never read further, or refused by the shared driver when no device may
// take the name (recordname.Check); a VM whose recorded address is not an
// IP address with no zone, or whose recorded login device is not a device
// name, is quarantined. None of them is stored, and the one VM recorded as
// virt.vbox.vm.clone records one is. Before 2026-11-13 any text in
// pleiades/address became the device's host, which its credential is sent
// to, and any text in pleiades/device was stored.
func TestSync_HostileInputNeverBecomesADevice(t *testing.T) {
	good := map[string]string{vboxmanage.ExtraAddress: "192.168.56.30", vboxmanage.ExtraDevice: "win-lab"}
	vms := []*vboxmanagetest.VM{{Name: "win-lab", OSType: "Windows Server 2025 (64-bit)", Extra: good}}
	hostileNames := []string{`a'b`, "a`b", "a$(calc)b", "a;calc", "a|b&c", `a"b`, `a\..\b`, "a/../b", `\\server\share`, `C:\`, "C:\\Windows", "a\x00b", "a\uFFFEb", "..", "-a"}
	for _, name := range hostileNames {
		vms = append(vms, &vboxmanagetest.VM{Name: name, OSType: "Windows Server 2025 (64-bit)", Extra: good})
	}
	hostileExtra := map[string]map[string]string{
		"addr-semicolon": {vboxmanage.ExtraAddress: "192.168.56.31; rm -rf /"},
		"addr-subshell":  {vboxmanage.ExtraAddress: "$(id)"},
		"addr-backtick":  {vboxmanage.ExtraAddress: "`id`"},
		"addr-name":      {vboxmanage.ExtraAddress: "evil.example.com"},
		"addr-zone":      {vboxmanage.ExtraAddress: "fe80::1%eth0;x"},
		"addr-unc":       {vboxmanage.ExtraAddress: `\\server\share`},
		"addr-drive":     {vboxmanage.ExtraAddress: `C:\`},
		"addr-dotdot":    {vboxmanage.ExtraAddress: "../.."},
		"addr-fffe":      {vboxmanage.ExtraAddress: "192.168.56.32\uFFFE"},
		"login-escape":   {vboxmanage.ExtraAddress: "192.168.56.33", vboxmanage.ExtraDevice: "a\x1b[2Jb"},
		"login-dotdot":   {vboxmanage.ExtraAddress: "192.168.56.34", vboxmanage.ExtraDevice: ".."},
		"login-nul":      {vboxmanage.ExtraAddress: "192.168.56.35", vboxmanage.ExtraDevice: "a\x00b"},
	}
	for name, extra := range hostileExtra {
		vms = append(vms, &vboxmanagetest.VM{Name: name, OSType: "Ubuntu (64-bit)", Extra: extra})
	}
	for i, vm := range vms {
		vm.UUID = fmt.Sprintf("00000000-0000-4000-a000-%012d", i+1)
		vm.State = vboxmanage.StatePoweroff
	}
	model := vboxmanagetest.New(vms...)
	onModel(t, model)
	p := New(hostCreds())
	cfg := config()
	cfg.ReadOnly = false
	if err := p.Connect(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inventory.yaml")
	if err := os.WriteFile(path, []byte("hosts: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := inv.NewFileRepository(path, inv.NewItemFactory())
	report, err := p.Sync(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	// The model writes a listed name Go-quoted where VBoxManage writes it as
	// it is, so a hostile name is matched by what is left: every VM but the
	// clone is quarantined (by its address, its login device, or as a name
	// VirtualBox's rule refuses) or refused (a name no device may take).
	quarantinedExtra := 0
	for _, r := range report.Results {
		switch {
		case r.Name == "win-lab":
			if r.Outcome != syncplugin.OutcomeAdded {
				t.Errorf("the clone was %q, want added", r.Outcome)
			}
		case r.Outcome != syncplugin.OutcomeQuarantined && r.Outcome != syncplugin.OutcomeRefused:
			t.Errorf("%q was %q, want quarantined or refused", r.Name, r.Outcome)
		}
		if _, hostile := hostileExtra[r.Name]; hostile && r.Outcome == syncplugin.OutcomeQuarantined {
			quarantinedExtra++
		}
	}
	if len(report.Results) != len(vms) || quarantinedExtra != len(hostileExtra) {
		t.Errorf("%d results for %d VMs, %d of %d hostile extradata quarantined: %+v", len(report.Results), len(vms), quarantinedExtra, len(hostileExtra), report.Results)
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var hosts struct {
		Hosts []struct {
			Name string `yaml:"name"`
		} `yaml:"hosts"`
	}
	if err := yaml.Unmarshal(stored, &hosts); err != nil || len(hosts.Hosts) != 1 || hosts.Hosts[0].Name != "win-lab" {
		t.Errorf("the inventory holds more than the clone (%v):\n%s", err, stored)
	}
	if item, err := repo.GetByName(context.Background(), "win-lab"); err != nil || item.Properties().Raw()[propHost] != "192.168.56.30" {
		t.Errorf("the clone was not stored at its address: %v", err)
	}
}
