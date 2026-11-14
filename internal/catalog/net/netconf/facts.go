// Package netconf: "net.netconf.facts", the read-only half of what every
// NETCONF session discovers (owner decision 2026-11-14). It opens a session
// as net.netconf.config does and records the same discovery facts, changing
// nothing, so a device read on a schedule shows a change in what it offers
// as drift without anything being edited.
package netconf

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	doc := factsDoc()
	collection.MustRegister(collection.Descriptor{
		Name: "net.netconf.facts",
		Manifest: collection.Manifest{
			SupportedTransports:  []string{"netconf"},
			RequiredCapabilities: []capability.Name{capability.NameNetconf, capability.NameSSHTransport},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteTarget,
				Device:            collection.DeviceRequired,
			},
			EngineVersion:  ">=0.4.0",
			Status:         collection.StatusImplemented,
			NoBecomeReason: "it speaks NETCONF over an SSH subsystem, which no sudo prefix reaches, so it would run as the login",
			SupportsCheck:  true,
			NoExistsReason: collection.NoExistsDevice,
			Reversibility: collection.Reversibility{
				Reversible: false,
				Notes:      "A read-only fact gatherer changes nothing on the device, so there is nothing to undo.",
				ReadOnly:   true,
			},
			Doc:   doc,
			Facts: collection.DeclareFacts(doc.Returns, discoveryFactKeys, factContentID),
		},
		Invoke: Facts,
		Check:  Facts,
	})
}

// factsDoc is net.netconf.facts' documentation; its returns are the
// discovery facts every session records.
func factsDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Records what a NETCONF session discovers about a device: its capabilities, datastores and YANG modules.",
		Description: "Opens an RFC 6241 NETCONF session over the SSH \"netconf\" subsystem, exactly as net.netconf.config does, and records what the session discovered, changing nothing: every capability the hello advertised that names no YANG module, the datastores the session could use, and the YANG modules the device implements, read from its YANG library (RFC 8525, or RFC 7895's modules-state) when it advertises one, since a YANG 1.1 device need not list its modules in the hello. The same facts every net.netconf.config session records, so a device read on a schedule shows a change in what it offers (IOS XE drops :writable-running when its candidate datastore is turned on) as drift. A session whose host key was not verified records its findings as stats only, never as facts. Reports no change, and a check runs it for real.",
		Params: []collection.Param{
			{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it; nothing the session discovers is then recorded as a fact."},
		},
		Returns: discoveryReturns(),
		Examples: []collection.Example{
			{Name: "Read what a device offers over NETCONF", RunbookYAML: "- name: Discover the device's YANG modules and datastores\n  net.netconf.facts:\n  register: discovered\n"},
		},
		SeeAlso: []string{"net.netconf.config", "net.ios.facts"},
	}
}

// Facts implements the "net.netconf.facts" collection method.
func Facts(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "net.netconf.facts"
	session, err := openSession(ctx, rc, device, params, fqcn, "")
	if err != nil {
		return collection.Result{}, err
	}
	defer func() { _ = session.Close() }()
	warnings, err := recordDiscovery(rc, params, session.Discovery())
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	for _, w := range session.TakeWarnings() {
		warnings = append(warnings, "the device warned: "+w.Error())
	}
	if len(warnings) > 0 {
		if err := sdk.RecordWarnings(rc, warnings); err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
	}
	return collection.Result{Changed: false}, nil
}
