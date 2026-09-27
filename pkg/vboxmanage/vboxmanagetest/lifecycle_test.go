// Tests for the model's lifecycle verbs and file scripts: the imported
// machine is held to the one captured from a real import, and each verb
// to what pkg/vboxmanage relies on it for.
package vboxmanagetest

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

const ova = `G:\PleiadesLab\ubuntu-24.04-server-cloudimg-amd64.ova`

// ubuntuAppliance is Ubuntu's cloud image as importing it makes it, slot
// for slot as the captured import (showvminfo-imported.stdout) shows,
// with the bridged adapter the appliance asks for.
func ubuntuAppliance() *VM {
	vm := &VM{MemoryMB: 1024, CPUs: 2, NICs: map[int]vboxmanage.NIC{1: {Kind: "bridged"}}}
	for port := 0; port < 2; port++ {
		for device := 0; device < 2; device++ {
			vm.Slots = append(vm.Slots, vboxmanage.Slot{Controller: "IDE", ControllerType: "PIIX4", Port: port, Device: device, Medium: "none"})
		}
	}
	vm.Slots = append(vm.Slots, vboxmanage.Slot{Controller: "SCSI", ControllerType: "LsiLogic", Medium: `G:\PleiadesLab\ubuntu-2404-base\Snapshots/{ccc97fb6-a275-4e19-84f2-25842ca5bcf4}.vmdk`})
	for port := 1; port < 16; port++ {
		vm.Slots = append(vm.Slots, vboxmanage.Slot{Controller: "SCSI", ControllerType: "LsiLogic", Port: port, Medium: "none"})
	}
	vm.Slots = append(vm.Slots,
		vboxmanage.Slot{Controller: "Floppy", ControllerType: "I82078", Medium: "emptydrive"},
		vboxmanage.Slot{Controller: "Floppy", ControllerType: "I82078", Device: 1, Medium: "none"})
	return vm
}

func TestImportMatchesTheCapturedImport(t *testing.T) {
	ctx := context.Background()
	h := New()
	h.Appliances[ova] = ubuntuAppliance()
	host := h.VBoxHost()
	if err := host.Import(ctx, ova, "ubuntu-2404-base", `G:\PleiadesLab`); err != nil {
		t.Fatal(err)
	}
	got, err := host.Machine(ctx, "ubuntu-2404-base")
	if err != nil {
		t.Fatal(err)
	}
	want := capturedMachine(t, "showvminfo-imported.stdout")
	if !reflect.DeepEqual(got.Slots, want.Slots) {
		t.Errorf("slots:\n%+v\nthe host's:\n%+v", got.Slots, want.Slots)
	}
	if !reflect.DeepEqual(got.NICs, want.NICs) {
		t.Errorf("NICs %+v, the host's %+v", got.NICs, want.NICs)
	}
	if got.MemoryMB != want.MemoryMB || got.CPUs != want.CPUs {
		t.Errorf("%d MB %d CPUs, the host's %d and %d", got.MemoryMB, got.CPUs, want.MemoryMB, want.CPUs)
	}
	if slot, ok := got.FreeIDESlot(); !ok || slot.Controller != "IDE" || slot.Port != 0 || slot.Device != 0 {
		t.Errorf("free IDE slot = %+v, %v", slot, ok)
	}
	for _, line := range strings.Split(strings.TrimSpace(h.VM("ubuntu-2404-base").showvminfo()), "\r\n") {
		if strings.HasPrefix(line, "name=") || strings.HasPrefix(line, "UUID=") {
			continue
		}
		if !strings.Contains(captured(t, "showvminfo-imported.stdout"), line+"\n") && !strings.Contains(captured(t, "showvminfo-imported.stdout"), line+"\r\n") {
			t.Errorf("the model wrote %q, which the host did not", line)
		}
	}
	if err := host.Import(ctx, ova, "ubuntu-2404-base", ""); err == nil {
		t.Error("imported over an existing machine")
	}
	if err := host.Import(ctx, `G:\missing.ova`, "x", ""); err == nil {
		t.Error("imported a file that is not there")
	}
}
