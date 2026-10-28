// virt.fusion.vm.info's whole-VM answer against the model Fusion host,
// whose configuration is a real vmcli ConfigParams capture
// (pkg/vmrun/testdata/configparams.txt) with the adapter and disk keys a
// VM made in Fusion's own window carries.
package vm

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// capturedConfig is pkg/vmrun/testdata/configparams.txt, then an adapter,
// a removed adapter, two disks and a guestinfo value standing for a secret
// a vmx can hand its guest.
const capturedConfig = `'config.version': 8
'ethernet0.virtualDev': e1000e
firmware: efi
guestOS: arm-ubuntu-64
'hpet0.present': TRUE
'memory.maxsize': 880
memsize: 768
'mks.enable3d': TRUE
nvram: 's2.nvram'
'pciBridge0.present': TRUE
'svga.present': TRUE
'virtualHW.version': 22
'vm.createDate': 1793057069000000
'vmci0.present': TRUE
displayName: 'win11 arm'
numvcpus: 2
'ethernet0.present': TRUE
'ethernet0.connectionType': nat
'ethernet0.addressType': generated
'ethernet0.generatedAddress': 00:0c:29:aa:bb:cc
'ethernet1.present': FALSE
'ethernet1.connectionType': bridged
'nvme0:0.fileName': 'win11-arm.vmdk'
'sata0:1.fileName': '/Users/me/Downloads/win11.iso'
'sata0:1.deviceType': cdrom-image
'guestinfo.userdata': SECRET-SENTINEL
`

// TestInfo_DescribesTheWholeVM holds R5: one info call returns what the
// vmx says of the VM, its snapshots and Tools' state, and never a key's raw
// value outside the allowlist; and R3: each is a declared return.
func TestInfo_DescribesTheWholeVM(t *testing.T) {
	m := model{exists: true, state: stateOn, tools: true, ip: "10.0.0.155", config: capturedConfig, snapshots: []string{"base", "after updates"}}
	onModel(t, &m)
	rc := newRecorder()
	if _, err := call(t, "virt.fusion.vm.info", false, rc, map[string]any{paramVMX: vmx}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		statDisplayName: "win11 arm", statGuestOS: "arm-ubuntu-64", statMemoryMB: 768, statCPUs: 2, statFirmware: "efi", statHardwareVersion: 22,
		statNICs:      []any{map[string]any{"number": 0, "connection_type": "nat", "virtual_dev": "e1000e", "address_type": "generated", "mac": "00:0c:29:aa:bb:cc"}},
		statDisks:     []any{map[string]any{"slot": "nvme0:0", "file": "win11-arm.vmdk"}, map[string]any{"slot": "sata0:1", "file": "/Users/me/Downloads/win11.iso", "device_type": "cdrom-image"}},
		statSnapshots: []string{"base", "after updates"}, statToolsState: "running",
	}
	for k, v := range want {
		if !reflect.DeepEqual(rc.stats[k], v) {
			t.Errorf("%s = %#v, want %#v", k, rc.stats[k], v)
		}
	}
	for k, v := range rc.stats {
		if strings.Contains(strings.ToUpper(strings.Join(flatten(v), " ")), "SECRET-SENTINEL") {
			t.Errorf("%s holds a vmx value outside the allowlist", k)
		}
	}
	if len(m.changes()) != 0 {
		t.Errorf("a read changed %v", m.changes())
	}

	declared := map[string]bool{}
	desc, _ := collection.Lookup("virt.fusion.vm.info")
	for _, r := range desc.Manifest.Doc.Returns {
		declared[r.Name] = true
	}
	for k := range rc.stats {
		if !declared[k] {
			t.Errorf("info records %s, which its Returns does not declare", k)
		}
	}
}

// TestInfo_AFailedConfigurationReadIsAWarning: the power state answered
// whether the VM exists, so a configuration vmcli will not print is named
// under warnings and the snapshots are still returned.
func TestInfo_AFailedConfigurationReadIsAWarning(t *testing.T) {
	m := model{exists: true, state: stateOff, snapshots: []string{"base"}}
	onModel(t, &m)
	rc := newRecorder()
	if _, err := call(t, "virt.fusion.vm.info", false, rc, map[string]any{paramVMX: vmx}); err != nil {
		t.Fatal(err)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "configuration was not read") {
		t.Errorf("warnings = %#v", rc.stats[sdk.StatWarnings])
	}
	if _, ok := rc.stats[statNICs]; ok {
		t.Error("nics reported from a configuration that was not read")
	}
	if !reflect.DeepEqual(rc.stats[statSnapshots], []string{"base"}) || rc.stats[statToolsState] != nil {
		t.Errorf("snapshots %v, tools_state %v (an off VM has none)", rc.stats[statSnapshots], rc.stats[statToolsState])
	}
}

// flatten is every string inside v, for the leak check.
func flatten(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []string:
		return x
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, flatten(e)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, flatten(e)...)
		}
		return out
	}
	return nil
}
