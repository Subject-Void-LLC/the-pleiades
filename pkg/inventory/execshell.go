// Which devices' SSH commands sh reads.
package inventory

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

// ShellFact is the discovery fact onboarding's SSH probe records about a
// device's shell, and NotPOSIX its value when the device has no POSIX sh to
// hand commands to (a Windows OpenSSH server's cmd.exe): the probe script,
// sent for sh, never answered.
//
// legacyNotPOSIX is the value an onboarding before the shell-agnostic
// change recorded, which meant something else: that the LOGIN shell was not
// POSIX, as a fish or tcsh login's is, on a device that has sh all the same.
// It is read as nothing recorded, so such a device's commands go to sh. Read
// as NotPOSIX, they went raw to fish, where QuoteArg is no boundary: fish
// reads a backslash inside single quotes, so a quoted value could end its
// quote and run as the login. A device that truly has no sh fails loudly
// until it is onboarded again, which records NotPOSIX.
const (
	ShellFact      = "shell"
	NotPOSIX       = "no posix sh"
	legacyNotPOSIX = "not posix"
)

// ExecIsPOSIX reports whether a command sent on item's SSH exec channel can be
// handed to sh (pkg/remoteexec/shline): true for every device but one known not
// to be a POSIX system, which is a network device's CLI (NetworkCLICapable)
// and a device onboarding found no POSIX shell on (a Windows OpenSSH server).
// Such a device's commands go to its login shell as written. The value an
// older onboarding recorded (legacyNotPOSIX) never counts.
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
