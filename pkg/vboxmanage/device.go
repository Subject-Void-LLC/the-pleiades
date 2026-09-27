// Reaching the VirtualBox host an inventory device describes.
package vboxmanage

import (
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// ForDevice returns the Host device describes, reached with secrets (the
// run's credential for it) and each VBoxManage call bounded by timeout.
//
// The device must be VirtualBox-capable, which says where VBoxManage is,
// and reachable over WinRM, which is how a Windows host is reached; its own
// pinned authority and server name, if it names any, verify the host. A
// host reached over SSH (Linux, macOS, Solaris) is the second case this
// will take, with no change to any caller.
func ForDevice(device inventory.InventoryItem, secrets map[string]string, timeout time.Duration) (Host, error) {
	if device == nil {
		return Host{}, fmt.Errorf("vboxmanage: needs a target device")
	}
	vbox, ok := device.(capability.VirtualBoxCapable)
	if !ok || !device.HasCapability(capability.NameVirtualBox) {
		return Host{}, fmt.Errorf("vboxmanage: device %q does not declare %s; set virtualbox: true on it",
			device.Name(), capability.NameVirtualBox)
	}
	winrm, ok := device.(capability.WinRMCapable)
	if !ok || !device.HasCapability(capability.NameWinRM) {
		return Host{}, fmt.Errorf("vboxmanage: device %q is not reachable over WinRM, the one way to a VirtualBox host so far", device.Name())
	}
	auth, err := winrmexec.AuthFromSecrets(secrets)
	if err != nil {
		return Host{}, fmt.Errorf("vboxmanage: %w", err)
	}
	opts := winrmexec.Options{Timeout: timeout}
	if shell, ok := device.(capability.WindowsShellCapable); ok {
		opts.PowerShellPath = shell.PowerShellPath()
	}
	runner := WinRMRunner{
		Target:  winrmexec.Target{Host: winrm.WinRMHost(), Port: winrm.WinRMPort()},
		Auth:    auth,
		Options: winrmexec.WithDeviceTLS(opts, devicetls.For(device)),
	}
	return Host{Runner: runner, Path: vbox.VBoxManagePath()}, nil
}
