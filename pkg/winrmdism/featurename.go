// Package winrmdism: the one form a Windows optional feature's name may
// take before it reaches dism.exe's command line.
package winrmdism

import (
	"fmt"
	"regexp"
)

// featureName is what an optional feature's name may be: letters, digits,
// ".", "_" and "-", starting with a letter or digit, as DISM lists them
// (Microsoft-Windows-Subsystem-Linux, IIS-WebServerRole, NetFx3,
// SMB1Protocol-Deprecation).
//
// The name travels inside a PowerShell single-quoted literal (QuotePS) to a
// native program, and Windows PowerShell 5.1 does not escape a '"' inside an
// argument it hands a native program, so a name holding one would reach
// dism.exe as more than one argument: /featurename:X followed by whatever
// the rest named (reasoned from PowerShell 5.1's native argument passing,
// not measured on a host; found by the FAILURE_PATTERNS 1174 sweep). A
// space, a slash, a colon or a leading "-" is refused for the same reason:
// each is how DISM's own options are written. CheckCapabilityName holds a
// capability identity to its own form the same way.
var featureName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)

// CheckFeatureName refuses a feature name outside featureName, naming the
// form it takes. Status, Enable and Disable call it before anything is sent,
// and win.feature's methods before they connect.
func CheckFeatureName(name string) error {
	if !featureName.MatchString(name) {
		return fmt.Errorf("feature name %q is not one DISM lists: letters, digits, \".\", \"_\" and \"-\" only, starting with a letter or digit (Get-WindowsOptionalFeature -Online lists them)", name)
	}
	return nil
}
