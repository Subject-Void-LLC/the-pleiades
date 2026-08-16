package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
)

// handWrittenDevices names the two device types that predate the Forge
// (cisco.Router, linux.Server) and so are absent from catalogdata.Devices
// (see that file's own doc comment). Their capability lists are recorded
// here by hand rather than reflected over at runtime, which makes this the
// one table behind a generated reference page that a human wrote.
//
// completeness_test.go is what stops it from drifting.
// TestDeviceRowsMatchLiveRegistry fails if the live device registry holds a
// type no row here would list, or the reverse.
// TestHandWrittenDeviceCapabilitiesMatchTheirTypes hydrates each of these
// two types through its real constructor and fails if the capabilities it
// hands back are not the ones recorded below. Both tests were written after
// an audit found an earlier version of this comment claiming a completeness
// gate that had never been built, which is why they are named here: a
// comment that points at a specific test can be checked.
var handWrittenDevices = []struct {
	Vendor       string
	TypeKey      string
	Capabilities []string
}{
	{Vendor: "cisco", TypeKey: "cisco_router", Capabilities: []string{"SSHTransportCapable", "CiscoIOSCapable"}},
	{Vendor: "linux", TypeKey: "linux_server", Capabilities: []string{"SSHTransportCapable", "LinuxCapable", "ShellExecCapable"}},
}

// generateDevices emits outDir/devices.md: every registered device type,
// generated ones from catalogdata.Devices and the two hand-written ones
// that predate the Forge, listed together rather than the generated ones
// only, so this page answers "what device types exist" completely.
func generateDevices(outDir string) error {
	var b strings.Builder
	b.WriteString(frontMatter("beta"))
	b.WriteString("# Device types\n\n")
	b.WriteString("Every registered inventory device type, its vendor package, and the capabilities " +
		"every hydrated instance carries as its baseline.\n\n")

	rows := make([][]string, 0, len(catalogdata.Devices)+len(handWrittenDevices))
	for _, d := range handWrittenDevices {
		rows = append(rows, []string{code(d.TypeKey), code(d.Vendor), quoteList(d.Capabilities), "hand-written, predates the Forge"})
	}
	for _, d := range catalogdata.Devices {
		caps := make([]string, len(d.Capabilities))
		for i, c := range d.Capabilities {
			caps[i] = string(c)
		}
		rows = append(rows, []string{code(d.TypeKey), code(d.Vendor), quoteList(caps), "generated"})
	}

	b.WriteString(table([]string{"Type", "Vendor", "Capabilities", "Origin"}, rows))
	b.WriteString(fmt.Sprintf("\n%d device types registered.\n", len(rows)))

	return os.WriteFile(filepath.Join(outDir, "devices.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}
