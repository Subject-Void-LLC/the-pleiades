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

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangfacts"
)

// The facts net.restconf.facts records beside the yang.* family.
const (
	factRoot         = "restconf.root"
	factCapabilities = "restconf.capabilities"
	factDatastores   = "restconf.datastores"
)

// factKeys are every fact a read may record, in the order they are recorded.
var factKeys = append([]string{factRoot, factCapabilities, factDatastores}, yangfacts.FactKeys...)

// description is the method's description, word for word its catalog
// entry's.
const description = "Reads the device's RESTCONF root from its /.well-known/host-meta (RFC 8040 section 3.1), never assuming /restconf, then the capabilities ietf-restconf-monitoring lists and the device's YANG library (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it), and records them, changing nothing: the same yang.* facts net.netconf.facts records over NETCONF, so a device reads alike over either protocol, and RESTCONF's own root, capabilities and datastores. The device is reached at its own origin with its own TLS (always verified, a pinned certificate included) and its own stored credential, following no redirect, and a root its host-meta names on another scheme, host or port is refused. A device reached over http:// records its findings as stats only, never as facts, since nothing proves which server answered. Reports no change, and a check runs it for real."

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
	return append([]collection.ReturnField{
		{Name: factRoot, Type: "string", Returned: when, Description: "The RESTCONF root the device's /.well-known/host-meta names, as an escaped path (such as /restconf), or / for a root at the device's origin itself."},
		{Name: factCapabilities, Type: "list of string", Returned: when, Description: "Every capability URI the device's ietf-restconf-monitoring lists (RFC 8040 section 9.1), with its parameters, sorted. Empty, with a warning, when the list could not be read or does not fit in 60 KiB; an entry that is not printable is left out whole."},
		{Name: factDatastores, Type: "list of string", Returned: when, Description: "The datastores the device's YANG library lists, sorted: an NMDA device's running, intended and operational, which RFC 8527 addresses under the root's /ds/; empty for a library that lists none, as RFC 7895's modules-state does."},
	}, yangfacts.Returns(when, yangfacts.RestconfSource)...)
}

// Facts implements the "net.restconf.facts" collection method: one
// discovery, recorded as stats always and as facts only over https.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
	const fqcn = "net.restconf.facts"
	if device == nil {
		return collection.Result{}, errors.New(fqcn + ": the task has no target device")
	}
	client, warnings, err := restconf.ClientFor(device, device.Name(), rc.InjectSecrets())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	// Recorded before the read, so a read that fails still says what the
	// device's record let it do.
	if err := sdk.RecordWarnings(rc, warnings); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	d, err := client.Discover(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: device %q: %w", fqcn, device.Name(), err)
	}
	values, found, err := discoveryValues(d)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	unverified := ""
	if !client.Verified() {
		unverified = "the device was reached over http://, where nothing proves which server answered"
	}
	withheld, err := yangfacts.Record(rc, factKeys, values, unverified)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if withheld != "" {
		found = append(found, withheld)
	}
	if len(found) > 0 {
		// The stat holds one list, so the record's own warnings are kept
		// beside what the read found.
		if err := sdk.RecordWarnings(rc, append(append([]string(nil), warnings...), found...)); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: false}, nil
}

// discoveryValues are the facts d gives and the warnings for what was left
// out of them.
func discoveryValues(d restconf.Discovery) (map[string]any, []string, error) {
	values, schemaWarnings, err := yangfacts.Values(d.Discovery)
	if err != nil {
		return nil, nil, err
	}
	root := d.Root
	if root == "" {
		root = "/"
	}
	values[factRoot] = root
	values[factDatastores] = yangfacts.NonNil(d.Datastores)
	var warnings []string
	capabilities := yangfacts.NonNil(d.Protocol)
	switch {
	case d.CapabilitiesError != "":
		warnings = append(warnings, "the device's RESTCONF capabilities were not read: "+d.CapabilitiesError)
	case !yangfacts.Fits(capabilities):
		warnings = append(warnings, fmt.Sprintf("the device lists %d RESTCONF capabilities, more than %s holds in 60 KiB; left out", len(capabilities), factCapabilities))
		capabilities = []string{}
	}
	values[factCapabilities] = capabilities
	return values, append(warnings, schemaWarnings...), nil
}
