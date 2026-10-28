// Package lab: virt.cml.lab.info, one lab with its nodes, their interfaces,
// the link each interface is on, and its links, beside how many nodes run
// across every lab the login can see. Its registration's Doc is generated
// from internal/forge/catalogdata.
package lab

import (
	"context"
	"fmt"
	"maps"
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
				Summary:     "Reports one CML lab: its nodes, their interfaces and links, and how many nodes the controller runs.",
				Description: "Reads one lab and changes nothing: each node's label, definition and state, each of its interfaces with the link it is on (the link's state, and the node and interface at its other end), every link, and how many nodes run on the controller, so a plan can see how full a shared controller is before it stops or starts a node. A lab that does not exist is not a failure: exists is false and only the controller's count is reported, so a later task can decide what to do. The count is of what this login can see (every lab GET /labs?show_all=true lists it), which may be fewer nodes than the controller runs, and it is not the controller's limit, which CML does not show an ordinary user. The task's target is a cml_controller device (its api_base, TLS settings and its own credential, stored with add-credential --password-stdin); the method runs on this machine and talks to the controller's REST API. A check is the same read, a missing lab included.",
				Params: []collection.Param{
					{Name: "lab", Type: "string", Required: true, Description: "The lab, by id or by title. A title two labs share is refused, naming both ids; an undo always names the id."},
				},
				Returns: []collection.ReturnField{
					{Name: "exists", Type: "bool", Returned: "always", Description: "Whether the controller holds the lab."},
					{Name: "id", Type: "string", Returned: "when exists", Description: "The lab's id."},
					{Name: "title", Type: "string", Returned: "when exists", Description: "The lab's title."},
					{Name: "state", Type: "string", Returned: "when exists", Description: "DEFINED_ON_CORE, STARTED or STOPPED."},
					{Name: "nodes", Type: "list", Returned: "when exists", Description: "Each node as id, label, definition, state (DEFINED_ON_CORE, STOPPED, QUEUED, STARTED or BOOTED) and interfaces. Each interface is label and connected and, when it is on a link, link (the link's id), link_state, peer and peer_interface (the node and interface at the link's other end, by label)."},
					{Name: "links", Type: "list", Returned: "when exists", Description: "Each link as id, a, a_interface, b, b_interface (by node label and interface label) and state: STARTED carries traffic, STOPPED is cut as if unplugged, DEFINED_ON_CORE has never run."},
					{Name: "controller_nodes_running", Type: "int", Returned: "always", Description: "How many nodes are queued, started or booted across every lab this login can see, this one's included. It may be fewer than the controller runs, and it is not the controller's limit."},
					{Name: "controller_labs", Type: "int", Returned: "always", Description: "How many labs controller_nodes_running counts."},
				},
				Examples: []collection.Example{
					{Name: "Read a lab", RunbookYAML: "- name: Read the CVD lab\n  virt.cml.lab.info:\n    lab: cvd\n  register: lab\n"},
				},
				SeeAlso: []string{"virt.cml.lab.list", "virt.cml.lab.create"},
			},
		},
		Invoke: Info,
		Check:  CheckInfo,
	})
}

// Info implements "virt.cml.lab.info". A lab that does not exist is
// reported as exists false.
func Info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runInfo(ctx, rc, device, params)
}

// CheckInfo is "virt.cml.lab.info"'s check: the same read, a missing lab
// included, which is exists false as in a run. It reports what the
// controller holds now; a lab an earlier task in the same check would make
// still reads as missing, as every read in a check does, since a check makes
// nothing.
func CheckInfo(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	return runInfo(ctx, rc, device, params)
}

// Stat keys lab.info reports beside the lab's own.
const (
	statControllerLabs    = "controller_labs"
	statControllerRunning = "controller_nodes_running"
)

// end is one end of a link: a node's label and its interface's label.
type end struct{ node, iface string }

// runInfo reads the controller's running-node count, then the lab, its
// state, its nodes with their interfaces, and its links.
func runInfo(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	const fqcn = "virt.cml.lab.info"
	ref, err := sdk.RequiredStringParam(params, paramLab)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	// The count is read first and reported whether or not the lab exists,
	// so a plan that is about to make a lab sees how full the controller is.
	capacity, err := client.RunningNodes(ctx)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	stats := map[string]any{statControllerLabs: capacity.Labs, statControllerRunning: capacity.Running}
	lab, err := client.FindLab(ctx, ref)
	if missing(err) {
		stats["exists"] = false
		return collection.Result{}, setStats(rc, stats, "exists", statControllerLabs, statControllerRunning)
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	nodes, links, err := readTopology(ctx, client, lab.ID)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	maps.Copy(stats, labStats(lab))
	stats["exists"], stats["nodes"], stats["links"] = true, nodes, links
	return collection.Result{}, setStats(rc, stats, "exists", "id", "title", "state", "nodes", "links", statControllerLabs, statControllerRunning)
}

// setStats records stats' keys, in order.
func setStats(rc sdk.RunbookContext, stats map[string]any, keys ...string) error {
	for _, k := range keys {
		if err := rc.SetStat(k, stats[k]); err != nil {
			return err
		}
	}
	return nil
}

// readTopology reads lab's nodes, each with its interfaces and the link each
// interface is on (its id and state, and the node and interface at its other
// end), and the lab's links, as lab.info reports them.
func readTopology(ctx context.Context, client *cml.Client, lab string) (nodeStats, linkStats []any, err error) {
	nodes, err := client.Nodes(ctx, lab)
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Label < nodes[j].Label })
	// Every interface is read first, so a link's far end can be named by
	// its node's label whichever node comes first.
	ends := map[string]end{}
	ifacesOf := make([][]cml.Interface, len(nodes))
	for k, n := range nodes {
		if ifacesOf[k], err = client.NodeInterfaces(ctx, lab, n.ID); err != nil {
			return nil, nil, err
		}
		for _, i := range ifacesOf[k] {
			ends[i.ID] = end{n.Label, i.Label}
		}
	}
	links, err := client.Links(ctx, lab)
	if err != nil {
		return nil, nil, err
	}
	// onLink maps each interface id to the link it is on.
	onLink := map[string]cml.Link{}
	linkStats = make([]any, 0, len(links))
	for _, l := range links {
		onLink[l.InterfaceA], onLink[l.InterfaceB] = l, l
		a, b := ends[l.InterfaceA], ends[l.InterfaceB]
		linkStats = append(linkStats, map[string]any{"id": l.ID, "a": a.node, "a_interface": a.iface, "b": b.node, "b_interface": b.iface, "state": l.State})
	}
	nodeStats = make([]any, 0, len(nodes))
	for k, n := range nodes {
		ifaceStats := make([]any, 0, len(ifacesOf[k]))
		for _, i := range ifacesOf[k] {
			s := map[string]any{"label": i.Label, "connected": i.IsConnected}
			if l, ok := onLink[i.ID]; ok {
				// The peer is the end of the link that is not this interface.
				peer := ends[l.InterfaceB]
				if l.InterfaceB == i.ID {
					peer = ends[l.InterfaceA]
				}
				s["link"], s["link_state"], s["peer"], s["peer_interface"] = l.ID, l.State, peer.node, peer.iface
			}
			ifaceStats = append(ifaceStats, s)
		}
		nodeStats = append(nodeStats, map[string]any{"id": n.ID, "label": n.Label, "definition": n.NodeDefinition, "state": n.State, "interfaces": ifaceStats})
	}
	return nodeStats, linkStats, nil
}
