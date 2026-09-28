// Tests for ForDevice: what a device must declare to be reached as a
// VirtualBox host, and that the host it builds is the one described.
package vboxmanage

import (
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// vboxHost is a Windows server whose record says VirtualBox is there.
type vboxHost struct {
	*inventorytest.Stub
}

func (vboxHost) VBoxManagePath() string { return vbox }
func (vboxHost) VMFolder() string       { return `G:\PleiadesLab` }
func (vboxHost) WinRMHost() string      { return "192.0.2.10" }
func (vboxHost) WinRMPort() int         { return 5986 }

// winrmOnly is a Windows server with no VirtualBox accessors.
type winrmOnly struct {
	*inventorytest.Stub
}

func (winrmOnly) WinRMHost() string { return "192.0.2.10" }
func (winrmOnly) WinRMPort() int    { return 5986 }

// vboxOnly has VirtualBox but no way to reach it.
type vboxOnly struct {
	*inventorytest.Stub
}

func (vboxOnly) VBoxManagePath() string { return vbox }
func (vboxOnly) VMFolder() string       { return "" }

func stub(caps ...capability.Name) *inventorytest.Stub {
	return &inventorytest.Stub{StubName: "vengeance", Caps: caps}
}

var password = map[string]string{"username": "pleiades-gate", "password": "not-a-real-one"}

func TestForDevice(t *testing.T) {
	both := []capability.Name{capability.NameVirtualBox, capability.NameWinRM}
	h, err := ForDevice(vboxHost{stub(both...)}, password, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := h.Runner.(WinRMRunner)
	if !ok {
		t.Fatalf("runner is %T", h.Runner)
	}
	if h.Path != vbox || r.Target.Host != "192.0.2.10" || r.Target.Port != 5986 || r.Options.Timeout != time.Minute || r.Auth.Username != "pleiades-gate" {
		t.Errorf("host = %+v, runner = %+v", h, r)
	}
	for name, tt := range map[string]struct {
		device  inventory.InventoryItem
		secrets map[string]string
		want    string
	}{
		"no device":                     {nil, password, "needs a target device"},
		"VirtualBox not declared":       {vboxHost{stub(capability.NameWinRM)}, password, "set virtualbox: true"},
		"no VirtualBox accessors":       {winrmOnly{stub(both...)}, password, "set virtualbox: true"},
		"WinRM not declared":            {vboxHost{stub(capability.NameVirtualBox)}, password, "not reachable over WinRM"},
		"no WinRM accessors":            {vboxOnly{stub(both...)}, password, "not reachable over WinRM"},
		"a credential WinRM cannot use": {vboxHost{stub(both...)}, map[string]string{"pfx_base64": "x", "password": "y"}, "vboxmanage: winrm: credential carries a PKCS#12 bundle"},
	} {
		if _, err := ForDevice(tt.device, tt.secrets, time.Minute); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want one mentioning %q", name, err, tt.want)
		}
	}
}

func TestMachineOff(t *testing.T) {
	for state, off := range map[string]bool{
		StatePoweroff: true, StateSaved: true, StateAborted: true,
		StateRunning: false, StatePaused: false, "starting": false,
	} {
		if got := (Machine{State: state}).Off(); got != off {
			t.Errorf("Off() for %s = %v, want %v", state, got, off)
		}
	}
}

func TestCheckUUID(t *testing.T) {
	if err := CheckUUID("5732a952-0b85-4e3a-820a-1a568f31166b"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", "clean", "{5732a952-0b85-4e3a-820a-1a568f31166b}", "5732a952-0b85-4e3a-820a-1a568f31166b; x"} {
		if err := CheckUUID(bad); err == nil {
			t.Errorf("CheckUUID(%q) accepted it", bad)
		}
	}
}
