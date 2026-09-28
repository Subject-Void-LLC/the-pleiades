// Tests for the lifecycle verbs and file scripts, against the model host
// in vboxmanagetest, whose own tests hold it to what a real host answered.
package vboxmanage_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

const ova = `G:\PleiadesLab\ubuntu-24.04-server-cloudimg-amd64.ova`

// appliance is a small appliance: an IDE controller with a free slot, a
// disk, and the bridged adapter Ubuntu's cloud image asks for.
func appliance() *vboxmanagetest.VM {
	return &vboxmanagetest.VM{MemoryMB: 1024, CPUs: 2, NICs: map[int]vboxmanage.NIC{1: {Kind: "bridged"}},
		Slots: []vboxmanage.Slot{
			{Controller: "IDE", ControllerType: "PIIX4", Medium: "none"},
			{Controller: "SCSI", ControllerType: "LsiLogic", Medium: `G:\PleiadesLab\base\disk.vmdk`},
		}}
}

func TestCloneConfigureAttachDelete(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	h.Appliances[ova] = appliance()
	host := h.VBoxHost()
	if err := host.Import(ctx, ova, "base", `G:\PleiadesLab`); err != nil {
		t.Fatal(err)
	}
	snap, err := host.TakeSnapshot(ctx, "base", "clean", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.CloneLinked(ctx, "base", snap, "lab", `G:\PleiadesLab`); err != nil {
		t.Fatal(err)
	}
	if err := host.CloneLinked(ctx, "base", "00000000-0000-4000-8000-000000000099", "lab2", ""); err == nil {
		t.Error("cloned a snapshot that is not there")
	}
	hw := vboxmanage.Hardware{MemoryMB: 2048, CPUs: 2, HostOnlyAdapter: "VirtualBox Host-Only Ethernet Adapter", ConsoleLog: `G:\PleiadesLab\lab\console.log`}
	if err := host.Configure(ctx, "lab", hw); err != nil {
		t.Fatal(err)
	}
	m, err := host.Machine(ctx, "lab")
	if err != nil {
		t.Fatal(err)
	}
	if m.MemoryMB != 2048 || m.NICs[1].Kind != "nat" || m.NICs[2].Kind != "hostonly" || m.NICs[2].HostOnlyAdapter != hw.HostOnlyAdapter || m.ConsoleLog != hw.ConsoleLog {
		t.Fatalf("configured %+v", m)
	}
	if m.NICs[1].MAC == "" || m.NICs[1].MAC == m.NICs[2].MAC {
		t.Errorf("MACs %q and %q", m.NICs[1].MAC, m.NICs[2].MAC)
	}

	seed := `G:\PleiadesLab\lab\seed.iso`
	slot, _ := m.FreeIDESlot()
	if err := host.AttachDVD(ctx, "lab", slot, seed); err == nil {
		t.Error("attached an image that is not there")
	}
	if err := host.Upload(ctx, seed, []byte("an ISO")); err != nil {
		t.Fatal(err)
	}
	if err := host.AttachDVD(ctx, "lab", slot, seed); err != nil {
		t.Fatal(err)
	}
	m, _ = host.Machine(ctx, "lab")
	if held := m.SlotsHolding(`G:\PleiadesLab\lab`); len(held) != 2 {
		t.Errorf("slots under the VM's folder: %+v", held)
	}
	if err := host.CloseDVD(ctx, seed, true); err == nil {
		t.Error("closed an image a machine holds")
	}
	if err := host.Unregister(ctx, "base"); err == nil {
		t.Error("deleted a machine a linked clone descends from")
	}
	if err := host.Detach(ctx, "lab", slot); err != nil {
		t.Fatal(err)
	}
	if err := host.CloseDVD(ctx, seed, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Files[seed]; ok {
		t.Error("the closed image was not deleted")
	}
	if err := host.Unregister(ctx, "lab"); err != nil {
		t.Fatal(err)
	}
	if err := host.Unregister(ctx, "base"); err != nil {
		t.Errorf("the base, with its clone gone: %v", err)
	}
	if err := host.Configure(ctx, "gone", hw); !errors.Is(err, vboxmanage.ErrNotFound) {
		t.Errorf("configuring a machine that is gone: %v", err)
	}
}

func TestFileScripts(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	host := h.VBoxHost()
	log := `G:\PleiadesLab\lab\console.log`
	if _, err := host.ReadTail(ctx, log, 10); !errors.Is(err, vboxmanage.ErrNoFile) {
		t.Errorf("reading a file that is not there: %v", err)
	}
	h.Files[log] = []byte("boot\r\n-----BEGIN SSH HOST KEY KEYS-----\r\n")
	tail, err := host.ReadTail(ctx, log, 8)
	if err != nil || string(tail) != "S-----\r\n" {
		t.Errorf("tail = %q, %v", tail, err)
	}
	if err := host.Remove(ctx, log, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Files[log]; ok {
		t.Error("the file was not removed")
	}
	h.Fail = map[string]vboxmanage.Output{"powershell upload": {ExitCode: 21, Stdout: "got=00\r\n"}}
	if err := host.Upload(ctx, log, []byte("x")); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("a damaged upload: %v", err)
	}
	if _, err := h.PowerShell(ctx, "# something else\n", ""); err == nil {
		t.Error("the model answered a script it does not know")
	}
}
