// The records the vbox plugin discovers: one a VM, read from showvminfo
// and the extradata virt.vbox.vm.clone writes.
package vbox

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// Property keys this plugin writes onto every discovered device. host and
// ip are the keys the rest of the codebase reads to reach a device; the
// rest carry a vbox_ prefix, so a reader of hosts.yaml can tell which
// fields came from the hypervisor, and so no second source collides with
// them.
const (
	propHost        = "host"
	propIP          = "ip"
	propUUID        = "vbox_uuid"
	propOSType      = "vbox_os_type"
	propFirmware    = "vbox_firmware"
	propLoginDevice = "vbox_login_device"
	// propUnreadable carries why a VM could not be read, which Classify
	// quarantines it with.
	propUnreadable = "vbox_unreadable"
)

// vmIterator reads one VM a step, so a host with many VMs is read as the
// sync goes rather than all before it starts.
type vmIterator struct {
	host vboxmanage.Host
	refs []vboxmanage.Ref
	pos  int
	cur  record.Record
	err  error
}

// Next reads the next VM. A VM VirtualBox cannot read (its folder gone, or
// a read refused) still yields a record, carrying why, so Classify
// quarantines it where a person sees it rather than the sync dropping it.
func (it *vmIterator) Next(ctx context.Context) bool {
	if it.err != nil || it.pos+1 >= len(it.refs) {
		return false
	}
	it.pos++
	ref := it.refs[it.pos]
	if ref.Inaccessible() {
		it.cur = unreadable(ref, "VirtualBox cannot read this VM's settings (its folder deleted or unreadable); virt.vbox.vm.delete names it by this UUID to unregister it")
		return true
	}
	m, err := it.host.Machine(ctx, ref.Name)
	if err != nil {
		it.cur = unreadable(ref, fmt.Sprintf("reading the VM failed: %v", err))
		return true
	}
	extra, err := it.host.ExtraData(ctx, ref.Name)
	if err != nil {
		it.cur = unreadable(ref, fmt.Sprintf("reading the VM's extradata failed: %v", err))
		return true
	}
	it.cur = vmRecord(m, extra)
	return true
}

// Record returns the VM at the cursor.
func (it *vmIterator) Record() record.Record { return it.cur }

// Error is always nil: a VM that cannot be read is a quarantined record,
// not an end to the stream.
func (it *vmIterator) Error() error { return it.err }

// Close drops the list; there is no connection to release.
func (it *vmIterator) Close() error {
	it.refs = nil
	return nil
}

// vmRecord is m as a discovered record. Its ID is the VM's UUID, which a
// rename keeps; the address and login device are what the clone recorded.
// Whether it runs is left out: it changes with every start and stop, and
// a device's record should change when the device does.
func vmRecord(m vboxmanage.Machine, extra map[string]string) record.Record {
	props := map[string]inventory.PropertyValue{propUUID: m.UUID}
	for key, value := range map[string]string{
		propHost:        extra[vboxmanage.ExtraAddress],
		propIP:          extra[vboxmanage.ExtraAddress],
		propOSType:      m.OSType,
		propFirmware:    m.Firmware,
		propLoginDevice: extra[vboxmanage.ExtraDevice],
	} {
		// An empty value is left out, so it is never stored as a real one.
		if value != "" {
			props[key] = value
		}
	}
	return record.Record{ID: inventory.DeviceID("vbox:" + m.UUID), Name: m.Name, Properties: props}
}

// unreadable is a record for a VM that could not be read, saying why.
func unreadable(ref vboxmanage.Ref, why string) record.Record {
	name := ref.Name
	if ref.Inaccessible() {
		name = ref.UUID
	}
	return record.Record{ID: inventory.DeviceID("vbox:" + ref.UUID), Name: name,
		Properties: map[string]inventory.PropertyValue{propUUID: ref.UUID, propUnreadable: why}}
}
