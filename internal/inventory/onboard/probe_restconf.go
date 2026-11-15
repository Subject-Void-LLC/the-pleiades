// The RESTCONF probe: the root read from host-meta, then the server's
// capabilities and YANG library.
package onboard

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
)

type restconfProber struct{}

func init() { Register(generic.TypeRestconf, restconfProber{}) }

func (restconfProber) Protocol() string { return "restconf" }

// Probe finds the device's RESTCONF root from its /.well-known/host-meta,
// read with no credential, or, when host-meta answers 404 or a redirect (never
// followed) as IOS XE answers it to anything but a valid login, with a 302 to
// its WebUI's login page (measured on 17.15.1a, 2026-11-15), the
// conventional /restconf, exactly as net.restconf.facts does, then its capabilities and YANG library,
// over the device's own TLS when its origin is https (pkg/devicetls: always
// verified) with its own credential, following no redirect, and records a summary of what it
// discovered: the root, the capabilities, the datastores, and the YANG
// modules' count, digest and completeness. The module list itself is the
// fact store's (net.restconf.facts' yang.modules), not the inventory
// record's. A root that answers, and a read of the capabilities or the
// library under it that succeeds (carrying the credential unless the record's
// http_auth is none), is what grants RestconfCapable: a host-meta naming a
// root is not proof of a RESTCONF server, nor of the credential.
func (restconfProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	client, warnings, err := restconf.ClientFor(device, device.Name(), secrets)
	if err != nil {
		return Probed{}, err
	}
	d, err := client.Discover(ctx)
	if err != nil {
		return Probed{}, err
	}
	if err := d.Learned(); err != nil {
		return Probed{}, err
	}
	probed := restconfProbed(d)
	probed.Warnings = append(warnings, probed.Warnings...)
	if !client.Verified() {
		probed.Warnings = append(probed.Warnings, "the device is reached over http://, so nothing proves which server answered this probe")
	}
	return probed, nil
}

// restconfProbed builds the result from what a client discovered.
func restconfProbed(d restconf.Discovery) Probed {
	// A capability longer than a recorded entry is left out whole and
	// counted, never cut: an entry cut short names something the device
	// does not offer (FAILURE_PATTERNS 1243).
	var capabilities []string
	long := 0
	for _, c := range d.Protocol {
		if len(c) > maxFactText {
			long++
			continue
		}
		capabilities = append(capabilities, c)
	}
	facts := map[string]any{
		"restconf_root":         factText(rootText(d.Root)),
		"restconf_root_source":  factText(d.RootSource),
		"restconf_capabilities": factList(capabilities),
		"restconf_datastores":   factList(d.Datastores),
		// Text, as every other probe's count is: an int read back from the
		// Controller's JSON column is a float64, and the record would never
		// compare equal on a re-onboard (FAILURE_PATTERNS 1250).
		"yang_module_count":   strconv.Itoa(len(d.Modules)),
		"yang_modules_digest": d.ModulesDigest(),
		"yang_completeness":   string(d.Completeness),
	}
	var warnings []string
	if long > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of the device's capabilities are longer than %d bytes and are left out of the record whole", long, maxFactText))
	}
	if d.CapabilitiesError != "" {
		warnings = append(warnings, "the device's RESTCONF capabilities were not read: "+d.CapabilitiesError)
	}
	if d.LibraryError != "" {
		warnings = append(warnings, "the device's YANG library was not read: "+d.LibraryError)
	}
	if d.Dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d malformed or oversized capability or module entries from the device were left out", d.Dropped))
	}
	return Probed{Capabilities: []capability.Name{capability.NameRestconf}, Facts: facts, Warnings: warnings}
}

// rootText is a discovered root as a record shows it: "/" for a root at the
// origin itself.
func rootText(root string) string {
	if root == "" {
		return "/"
	}
	return root
}
