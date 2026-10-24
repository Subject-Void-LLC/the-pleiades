// Package lab: virt.cml.lab.info, one lab with its nodes, their interfaces
// and its links. Its registration's Doc is generated from
// internal/forge/catalogdata.
package lab

import (
	"context"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: "virt.cml.lab.info",
		Manifest: collection.Manifest{
			SupportedTransports: []string{
				"https",
			},
			RequiredCapabilities: []capability.Name{
				capability.Name("CMLAPICapable"),
			},
			ExecutionContext: collection.ExecutionContext{
				RequiresElevation: false,
				Site:              collection.SiteController,
				Device:            collection.DeviceRequired,
			},
			// TODO(forge): PlatformTargets narrows this manifest to a
			// specific vendor, model, firmware range, or deployment
			// context (pkg/collection.PlatformTarget). Left nil: thin
			// flag parsing only, per Phase 33's own checklist. Add real
			// entries by hand once this method's platform scope is known.
			PlatformTargets: nil,
			EngineVersion:   ">=0.2.0",
			Status:          collection.StatusImplemented,
			Reversibility: collection.Reversibility{
				ReadOnly: true,
				Notes:    "It only reads the controller.",
			},

			SupportsCheck: true,
			Doc: collection.Doc{
				Summary:     "Reports one CML lab: its nodes, their interfaces and its links.",
				Description: "Reads one lab and changes nothing. A lab that does not exist is not a failure: exists is false and nothing else is reported, so a later task can decide what to do. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check is the same read.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the controller holds the lab."},
					{Name: "id", Type: "string", Returned: "when exists", Description: "The lab's id."},
					{Name: "title", Type: "string", Returned: "when exists", Description: "The lab's title."},
					{Name: "state", Type: "string", Returned: "when exists", Description: "DEFINED_ON_CORE, STARTED or STOPPED."},
					{Name: "nodes", Type: "list", Returned: "when exists", Description: "Each node as id, label, definition, state and interfaces (each as label and connected)."},
					{Name: "links", Type: "list", Returned: "when exists", Description: "Each link as id, a, a_interface, b and b_interface, by node label and interface label."},
				},
				Examples: []collection.Example{
					{Name: "Read a lab", RunbookYAML: "- name: Read the CVD lab\n  virt.cml.lab.info:\n    lab: cvd\n  register: lab\n"},
				},
				SeeAlso: []string{"virt.cml.lab.list", "virt.cml.lab.create"},
			},
		},
		Invoke: Info,
		Check:  Info,
	})
}

// Info implements "virt.cml.lab.info", and is its own check, since it
// only reads. A lab that does not exist is reported as exists false.
func Info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "virt.cml.lab.info"
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	lab, err := client.FindLab(ctx, ref)
	if missing(err) {
		return collection.Result{}, rc.SetStat("exists", false)
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	nodes, err := client.Nodes(ctx, lab.ID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Label < nodes[j].Label })
	type end struct{ node, iface string }
	ends := map[string]end{}
	nodeStats := make([]any, 0, len(nodes))
	for _, n := range nodes {
		ifaces, err := client.NodeInterfaces(ctx, lab.ID, n.ID)
		if err != nil {
			return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
		}
		ifaceStats := make([]any, 0, len(ifaces))
		for _, i := range ifaces {
			ends[i.ID] = end{n.Label, i.Label}
			ifaceStats = append(ifaceStats, map[string]any{"label": i.Label, "connected": i.IsConnected})
		}
		nodeStats = append(nodeStats, map[string]any{"id": n.ID, "label": n.Label, "definition": n.NodeDefinition, "state": n.State, "interfaces": ifaceStats})
	}
	links, err := client.Links(ctx, lab.ID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	linkStats := make([]any, 0, len(links))
	for _, l := range links {
		a, b := ends[l.InterfaceA], ends[l.InterfaceB]
		linkStats = append(linkStats, map[string]any{"id": l.ID, "a": a.node, "a_interface": a.iface, "b": b.node, "b_interface": b.iface})
	}
	stats := labStats(lab)
	stats["exists"], stats["nodes"], stats["links"] = true, nodeStats, linkStats
	for _, k := range []string{"exists", "id", "title", "state", "nodes", "links"} {
		if err := rc.SetStat(k, stats[k]); err != nil {
			return collection.Result{}, err
		}
	}
	return collection.Result{}, nil
}
