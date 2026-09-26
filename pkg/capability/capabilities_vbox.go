// The capability of a host that runs Oracle VirtualBox, which the
// virt.vbox.* Collection targets.
package capability

// NameVirtualBox is VirtualBoxCapable's registered name.
const NameVirtualBox Name = "VirtualBoxCapable"

// VirtualBoxCapable is implemented by a host that runs Oracle VirtualBox
// and can be told to run VBoxManage.
//
// It is a capability rather than a device type: a Windows server with
// VirtualBox installed is still a Windows server, reached and credentialed
// as one, and a device type per combination is the explosion the map's
// capability model exists to avoid. A device type declares it only when
// its record says VirtualBox is there.
type VirtualBoxCapable interface {
	// VBoxManagePath is the absolute path of VBoxManage on the host.
	VBoxManagePath() string

	// VMFolder is the folder new VMs are created in, or "" for
	// VirtualBox's own default machine folder.
	VMFolder() string
}

func init() {
	Register(Descriptor{
		Name:   NameVirtualBox,
		Assert: func(item any) bool { _, ok := item.(VirtualBoxCapable); return ok },
	})
}
