// Tests for the data-disk helpers a VM's second disk is attached with
// (create.go's AttachDiskAt and HasDisk), against the model host in
// vboxmanagetest.
package vboxmanage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// TestHasDisk finds a registered disk however its path is cased, does not
// find one VirtualBox never registered, and reports a failed list as an
// error rather than as "not registered".
func TestHasDisk(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	host := h.VBoxHost()
	made := `G:\PleiadesLab\bsd-check\bsd-check-data.vdi`
	if err := host.CreateDisk(ctx, made, 40960); err != nil {
		t.Fatal(err)
	}
	if ok, err := host.HasDisk(ctx, strings.ToLower(made)); !ok || err != nil {
		t.Errorf("HasDisk of the made disk, lowercased = %v, %v; want true", ok, err)
	}
	if ok, err := host.HasDisk(ctx, `G:\PleiadesLab\other.vdi`); ok || err != nil {
		t.Errorf("HasDisk of a disk never made = %v, %v; want false", ok, err)
	}
	h.Fail = map[string]vboxmanage.Output{"VBoxManage.exe list hdds": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
	if ok, err := host.HasDisk(ctx, made); err == nil {
		t.Errorf("HasDisk with no list of disks = %v, nil; want an error", ok)
	}
}

// TestAttachDiskAt puts a disk in the free SATA slot the machine reports,
// port and device included, and refuses an unknown controller before running anything.
func TestAttachDiskAt(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	host := h.VBoxHost()
	if err := host.CreateVM(ctx, windowsMachine()); err != nil {
		t.Fatal(err)
	}
	boot, made := `G:\PleiadesLab\ws2025-base\boot.vdi`, `G:\PleiadesLab\ws2025-base\data.vdi`
	for _, path := range []string{boot, made} {
		if err := host.CreateDisk(ctx, path, 1024); err != nil {
			t.Fatal(err)
		}
	}
	if err := host.AttachDisk(ctx, "ws2025-base", boot); err != nil {
		t.Fatal(err)
	}
	m, err := host.Machine(ctx, "ws2025-base")
	if err != nil {
		t.Fatal(err)
	}
	slot, ok := m.FreeSATASlot()
	if !ok || slot.Port == 0 {
		t.Fatalf("FreeSATASlot = %+v, %v; want a free SATA port past the boot disk's", slot, ok)
	}
	if err := host.AttachDiskAt(ctx, "ws2025-base", slot, made); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("storageattach ws2025-base --storagectl %s --port %d --device %d --type hdd --medium %s", slot.Controller, slot.Port, slot.Device, made)
	if !strings.Contains(strings.Join(h.Calls(), "\n"), want) {
		t.Errorf("no %q in the calls %v", want, h.Calls())
	}
	before := len(h.Calls())
	if err := host.AttachDiskAt(ctx, "ws2025-base", vboxmanage.Slot{Controller: "Floppy; rm"}, made); err == nil {
		t.Error("an unknown controller was accepted")
	}
	if len(h.Calls()) != before {
		t.Errorf("a refused attach ran %v", h.Calls()[before:])
	}
}
