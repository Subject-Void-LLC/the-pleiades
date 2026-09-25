package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory" // registers every built-in device type
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
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
//
// Conditional lists what a type declares only when one of its own
// properties says so. The two could not be told apart until Phase 77, so
// cisco_router's NetconfCapable (declared when netconf_enabled is true)
// was simply missing from the generated page; the completeness test now
// checks a conditional capability in both directions, absent from a bare
// record and present once its property is set.
var handWrittenDevices = []struct {
	Vendor       string
	TypeKey      string
	Capabilities []string
	Conditional  []conditionalCapability
}{
	{
		Vendor: "cisco", TypeKey: "cisco_router",
		Capabilities: []string{"SSHTransportCapable", "CiscoIOSCapable", "NetworkAddressableCapable"},
		Conditional:  []conditionalCapability{{Name: "NetconfCapable", Property: "netconf_enabled", Value: true}},
	},
	{
		Vendor: "linux", TypeKey: "linux_server",
		Capabilities: []string{
			"SSHTransportCapable", "LinuxCapable", "ShellExecCapable",
			"POSIXFileSystemCapable", "FactGathererCapable", "SystemdCapable",
			"NetworkAddressableCapable",
		},
		Conditional: []conditionalCapability{
			{Name: "FileTransferCapable", Property: "file_transfer_root", Value: "/srv/xfer"},
			{Name: "FirewalldCapable", Property: "firewalld", Value: true},
		},
	},
}

// conditionalCapability is a capability a hand-written type declares
// only when Property holds a value like Value.
type conditionalCapability struct {
	// Name is the capability.
	Name string
	// Property is the inventory property that enables it.
	Property string
	// Value is an example value that enables it, which the completeness
	// test hydrates with.
	Value any
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
		"that type can carry.\n\n")

	rows := make([][]string, 0, len(catalogdata.Devices)+len(handWrittenDevices))
	var conditional bool
	for _, d := range handWrittenDevices {
		names := append([]string(nil), d.Capabilities...)
		for _, c := range d.Conditional {
			names = append(names, c.Name)
		}
		caps, marked := markConditional(d.TypeKey, names)
		conditional = conditional || marked
		rows = append(rows, []string{code(d.TypeKey), code(d.Vendor), caps, "hand-written, predates the Forge"})
	}
	generic, err := genericDeviceRows()
	if err != nil {
		return err
	}
	rows = append(rows, generic...)
	for _, d := range catalogdata.Devices {
		names := make([]string, len(d.Capabilities))
		for i, c := range d.Capabilities {
			names[i] = string(c)
		}
		caps, marked := markConditional(d.TypeKey, names)
		conditional = conditional || marked
		rows = append(rows, []string{code(d.TypeKey), code(d.Vendor), caps, "generated"})
	}

	b.WriteString(table([]string{"Type", "Vendor", "Capabilities", "Origin"}, rows))
	b.WriteString(fmt.Sprintf("\n%d device types registered.\n", len(rows)))
	if conditional {
		b.WriteString("\n" + conditionalNote + "\n")
	}
	b.WriteString("\n" + genericNote + "\n")

	return os.WriteFile(filepath.Join(outDir, "devices.md"), []byte(b.String()), 0o644) // #nosec G306 -- generated docs, not secret material
}

// conditionalNote explains the marker markConditional adds. It is written
// only when some type actually earns it, so the page never carries a
// footnote pointing at nothing.
const conditionalNote = "A capability list marked with an asterisk is what that type CAN carry, not what " +
	"every instance declares: the type decides per device, from that device's own properties. " +
	"`console_device` is the case this exists for, because a local serial line, a console server " +
	"port and a bare Telnet session are alternative ways to reach one device rather than three " +
	"facts about it, so a device configured for one must not claim the others. `cisco_router` " +
	"declares `NetconfCapable` only when `netconf_enabled` is true, and `linux_server` declares " +
	"`FileTransferCapable` only when `file_transfer_root` names a directory and `FirewalldCapable` " +
	"only when `firewalld` is true. A `linux_server` gains `AptCapable` (or `DnfCapable`) and " +
	"`PosixAccountCapable` from its classification: add it with `--classify " +
	"linux_server,debian_family` (or `rhel_family`) and the package and account methods can reach " +
	"it. See each type's package documentation for which property enables which capability."

// markConditional renders a type's capability list, appending an asterisk
// when hydrating that type with a bare Record does not in fact declare
// every capability listed, and reports whether it did.
//
// Deriving this rather than hardcoding a type name is the difference
// between a footnote that stays true and one that rots: a type that
// later becomes unconditional loses its marker with no edit here, and a
// new conditional type gains one without anybody remembering to.
func markConditional(typeKey string, capabilities []string) (string, bool) {
	rendered := quoteList(capabilities)

	constructor, ok := record.LookupType(typeKey)
	if !ok {
		// TestDeviceRowsMatchLiveRegistry owns this failure; rendering
		// the row unmarked keeps one problem from looking like two.
		return rendered, false
	}
	item, err := constructor(record.Record{Name: typeKey, Type: typeKey})
	if err != nil {
		return rendered, false
	}

	declared := make(map[string]bool, len(item.Capabilities()))
	for _, c := range item.Capabilities() {
		declared[string(c)] = true
	}
	for _, name := range capabilities {
		if !declared[name] {
			return rendered + " \\*", true
		}
	}
	return rendered, false
}

// genericExampleProperties is the least each generic type's constructor
// needs to build: an address. TestGenericDeviceRows fails if one is
// missing, since a row would then not render.
var genericExampleProperties = map[string]map[string]inventory.PropertyValue{
	generic.TypeHTTP: {generic.BaseURLProperty: "https://api.example.com"},
	generic.TypeGRPC: {generic.GRPCTargetProperty: "grpc.example.com:443"},
}

// genericDeviceRows renders the generic types from their code: the
// baseline their constructor declares for a device nobody has onboarded,
// then what onboarding may add (generic.Discoverable).
func genericDeviceRows() ([][]string, error) {
	var rows [][]string
	for _, t := range generic.Types() {
		constructor, ok := record.LookupType(t)
		if !ok {
			return nil, fmt.Errorf("generic device type %s is not registered", t)
		}
		item, err := constructor(record.Record{Name: t, Type: t, Properties: genericExampleProperties[t]})
		if err != nil {
			return nil, fmt.Errorf("generic device type %s: %w", t, err)
		}
		baseline := make([]string, 0, len(item.Capabilities()))
		for _, c := range item.Capabilities() {
			baseline = append(baseline, string(c))
		}
		// Capabilities comes from a set; sorted so the page is stable.
		sort.Strings(baseline)
		discovered := []string{}
		for _, c := range generic.Discoverable(t) {
			discovered = append(discovered, string(c))
		}
		caps := quoteList(baseline) + "; discovered: " + quoteList(discovered)
		rows = append(rows, []string{code(t), code("generic"), caps, "generic, discovered at onboarding"})
	}
	return rows, nil
}

// genericNote explains the generic rows.
const genericNote = "A `generic` type's capabilities after \"discovered:\" are granted only by `pleiades onboard`, " +
	"which probes the device over its protocol and records what the device's own answers prove; no inventory " +
	"value, classification or sync plugin can grant one. Such a device starts `discovered`, which runs nothing, " +
	"and becomes `active` when onboarding succeeds."
