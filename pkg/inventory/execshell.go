// Which devices' SSH commands sh reads.
package inventory

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

// ShellFact is the discovery fact onboarding's SSH probe records about a
// login's shell, and NotPOSIX its value when the login is not a POSIX shell's.
const (
	ShellFact = "shell"
	NotPOSIX  = "not posix"
)

// ExecIsPOSIX reports whether a command sent on item's SSH exec channel can be
// handed to sh (pkg/remoteexec/shline): true for every device but one known not
// to be a POSIX system, which is a network device's CLI (NetworkCLICapable)
// and a device onboarding found no POSIX shell on (a Windows OpenSSH server).
// Such a device's commands go to its login shell as written.
func ExecIsPOSIX(item InventoryItem) bool {
	if item == nil {
		return true
	}
	if item.HasCapability(capability.NameNetworkCLI) {
		return false
	}
	if d, ok, err := DiscoveryFrom(item.Properties()); err == nil && ok && d.Facts[ShellFact] == NotPOSIX {
		return false
	}
	return true
}
