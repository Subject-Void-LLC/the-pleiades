// Package restconf implements "net.restconf.facts", the RESTCONF half of what
// a device's YANG discovery records (owner decision 2026-11-14). It finds the
// device's root from its host-meta, reads its capabilities and its YANG
// library through pkg/restconf, and records the same yang.* facts every
// NETCONF session records, beside RESTCONF's own root, capabilities and
// datastores, changing nothing, so a device read on a schedule shows a change
// in what it offers as drift. The read is pkg/restconf's, shared with
// onboarding, so the two cannot disagree about how a device is reached.
package restconf

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/tlsfacts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangfacts"
)

// The facts net.restconf.facts records beside the yang.* family.
const (
	factRoot         = "restconf.root"
	factRootSource   = "restconf.root_source"
	factCapabilities = "restconf.capabilities"
	factDatastores   = "restconf.datastores"
	factOwnModules   = "restconf.modules"
)

// factKeys are every fact a read may record, in the order they are recorded:
// RESTCONF's own, the yang.* family, and the certificate the device
// presented (tls.certificate.*).
var factKeys = append(append([]string{factRoot, factRootSource, factCapabilities, factDatastores, factOwnModules}, yangfacts.FactKeys...), tlsfacts.FactKeys...)

// description is the method's description, word for word its catalog
// entry's.
const description = "Finds the device's RESTCONF root from its /.well-known/host-meta (RFC 8040 section 3.1), read with no credential; a host-meta answered 404 or a redirect, which is never followed (IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page, which answers 404, measured on 17.15.1a, 2026-11-15), leaves RFC 8040's conventional /restconf, which the first authenticated read under it confirms (and restconf.root_source says which way the root was learned), while a 404 there too refuses the device. Then it reads the capabilities ietf-restconf-monitoring lists and the device's YANG library (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it), and records them, changing nothing: the same yang.* facts net.netconf.facts records over NETCONF, and RESTCONF's own root, capabilities and datastores. The yang.* facts are protocol-neutral: each protocol's library also lists its own modules (measured on a Catalyst 8000V running IOS XE 17.15.1a, NETCONF's ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions, RESTCONF's tailf-rest-query), which yang.* leaves out and restconf.modules (or netconf.modules over NETCONF) records, so one device read over either protocol gives the same yang.modules, yang.module_count and yang.modules_digest. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a root its host-meta names on another scheme, host or port is refused. A read whose capabilities and library both failed fails the task rather than recording a device with nothing, as onboarding refuses it; a family whose own read failed is left out, with a warning. A device reached over http:// records its findings as stats only, never as facts, since nothing proves which server answered. Reports no change, and a check runs it for real."

func init() {
	doc := factsDoc()
	collection.MustRegister(collection.Descriptor{
		Name: "net.restconf.facts",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{capability.TransportRestconf},
			RequiredCapabilities: []capability.Name{capability.NameRestconf},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			EngineVersion:  ">=0.4.0",
			Status:         collection.StatusImplemented,
			NoBecomeReason: "it speaks RESTCONF over HTTPS to the device's own API, which no sudo prefix reaches",
			SupportsCheck:  true,
			NoExistsReason: collection.NoExistsDevice,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A read-only fact gatherer changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc:   doc,
			Facts: collection.DeclareFacts(doc.Returns, factKeys, yangfacts.FactContentID),
		},
		Invoke: Facts,
		Check:  Facts,
	})
}

// factsDoc is net.restconf.facts' documentation.
func factsDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Records what a device's RESTCONF server says about it: its root, capabilities, datastores and YANG modules.",
		Description: description,
		Returns:     factsReturns(),
		Examples: []collection.Example{
			{Name: "Read what a device offers over RESTCONF", RunbookYAML: "- name: Discover the device's YANG modules over RESTCONF\n  net.restconf.facts:\n  register: discovered\n"},
		},
		SeeAlso: []string{"net.netconf.facts"},
	}
}

// factsReturns documents the facts, one return each.
func factsReturns() []collection.ReturnField {
	const when = "on every read, as a stat; as a fact only when the device was reached over https"
	const whenLibrary = "when the YANG library was read, as a stat; as a fact only when the device was reached over https"
	return append([]collection.ReturnField{
		{Name: factRoot, Type: "string", Returned: when, Description: "The RESTCONF root, as an escaped path (such as /restconf), or / for a root at the device's origin itself: the one the device's /.well-known/host-meta names, or the conventional /restconf when host-meta answered 404 or a redirect and a read under it answered (restconf.root_source says which)."},
		{Name: factRootSource, Type: "string", Returned: when, Description: "How restconf.root was learned: host-meta (its restconf link) or default (host-meta answered 404 or a redirect, as IOS XE answers it to anything but a valid login with a 302 to its WebUI's login page (404 there), and a read under the conventional /restconf answered)."},
		{Name: factCapabilities, Type: "list of string", Returned: "when the capability list was read and fits, as a stat; as a fact only when the device was reached over https", Description: "Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Left out, with a warning, when the list could not be read or does not fit in 60 KiB, so an unread list never reads as an empty one; an entry that is not printable is left out whole."},
		{Name: factDatastores, Type: "list of string", Returned: whenLibrary, Description: "The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does."},
		{Name: factOwnModules, Type: "list of string", Returned: whenLibrary, Description: yangfacts.OwnModulesDescription("RESTCONF")},
	}, append(yangfacts.Returns(whenLibrary, yangfacts.RestconfSource), tlsfacts.Returns()...)...)
}

// Facts implements the "net.restconf.facts" collection method: one
// discovery, recorded as stats always and as facts only over https.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	const fqcn = "net.restconf.facts"
	if device == nil {
		return collection.Result{}, errors.New(fqcn + ": the task has no target device")
	}
	// The read's own verified handshakes say which certificate the device
	// presented (tls.certificate.*).
	ctx, seen := devicetls.WithPresented(ctx)
	client, warnings, err := restconf.ClientForContext(ctx, device, device.Name(), rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	// Recorded before the read, so a read that fails still says what the
	// device's record let it do.
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	d, err := client.Discover(ctx)
	if err == nil {
		// A read that learned nothing past the root is refused, as
		// onboarding refuses it: recorded, it would read as a device that
		// lost every capability and module.
		err = d.Learned()
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	found, err := recordDiscovery(rc, client.Verified(), d)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	certificate, err := recordCertificate(rc, device, seen)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	found = append(found, certificate...)
	found = append(found, client.SessionWarnings(device.Name())...)
	if len(found) > 0 {
		// The stat holds one list, so the record's own warnings are kept
		// beside what the read found.
		if err := sdk.RecordWarnings(rc, append(append([]string(nil), warnings...), found...)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// recordCertificate records the certificate device presented on a read's
// verified handshakes (seen) as the tls.certificate.* facts, returning the
// warnings it calls for. net.restconf.facts and net.restconf.config both
// record through it.
func recordCertificate(rc sdk.RunbookContext, device inventory.InventoryItem, seen *devicetls.Presented) ([]string, error) {
	return tlsfacts.Record(rc, device.Name(), devicetls.For(device), seen, time.Now())
}

// recordDiscovery records what d learned as stats, and as facts only when
// verified (the device was reached over https), returning the warnings the
// task should carry. net.restconf.facts and net.restconf.config both record
// through it, as every NETCONF session does.
func recordDiscovery(rc sdk.RunbookContext, verified bool, d restconf.Discovery) ([]string, error) {
	values, found, err := discoveryValues(d)
	if err != nil {
		return nil, err
	}
	if err := yangfacts.Record(rc, factKeys, values, verified); err != nil {
		return nil, err
	}
	if !verified {
		found = append(found, "the device was reached over http://, where nothing proves which server answered, so nothing this read found is recorded as a fact")
	}
	return found, nil
}

// discoveryValues are the facts d gives and the warnings for what was left
// out of them. A family whose read failed records nothing, stat or fact, so
// an unread library never reads as a device that implements no module: the
// yang.* facts and restconf.datastores come from the library, and
// restconf.capabilities from the capability list. A capability list past its
// bound is left out the same way, never recorded empty.
func discoveryValues(d restconf.Discovery) (map[string]any, []string, error) {
	values := map[string]any{}
	var warnings []string
	if d.LibraryError != "" {
		warnings = append(warnings, "the device's YANG library was not read ("+d.LibraryError+"); no yang.* or "+factDatastores+" fact is recorded from this read")
	} else {
		schema, schemaWarnings, err := yangfacts.Values(d.Discovery, yangfacts.RestconfSource)
		if err != nil {
			return nil, nil, err
		}
		values = schema
		values[factDatastores] = yangfacts.NonNil(d.Datastores)
		warnings = append(warnings, schemaWarnings...)
		own, fits, err := yangfacts.OwnModules(d.Discovery)
		if err != nil {
			return nil, nil, err
		}
		if fits {
			values[factOwnModules] = own
		} else {
			warnings = append(warnings, "the modules RESTCONF's server lists for itself do not fit in 60 KiB; "+factOwnModules+" is left out")
		}
	}
	root := d.Root
	if root == "" {
		root = "/"
	}
	values[factRoot] = root
	values[factRootSource] = d.RootSource
	capabilities := yangfacts.NonNil(d.Protocol)
	switch {
	case d.CapabilitiesError != "":
		warnings = append([]string{"the device's RESTCONF capabilities were not read: " + d.CapabilitiesError}, warnings...)
	case !yangfacts.Fits(capabilities):
		warnings = append([]string{fmt.Sprintf("the device lists %d RESTCONF capabilities, more than %s holds in 60 KiB; left out", len(capabilities), factCapabilities)}, warnings...)
	default:
		values[factCapabilities] = capabilities
	}
	if d.Dropped > 0 && d.LibraryError != "" {
		// yangfacts.Values names the refused entries when the library was
		// read; a refused capability is named here otherwise.
		warnings = append(warnings, fmt.Sprintf("%d malformed or oversized capability entries from the device were left out", d.Dropped))
	}
	return values, warnings, nil
}
