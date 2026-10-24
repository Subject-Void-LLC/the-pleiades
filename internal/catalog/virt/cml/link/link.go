// Package link implements the virt.cml.link.* methods: two nodes'
// interfaces linked in a CML lab, and a link removed. This file holds what
// both read: a link's two ends by node and interface, and the ends a
// removed link's undo names it by.
package link

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The parameter names.
const (
	paramLab        = "lab"
	paramLink       = "link"
	paramA          = "a"
	paramAInterface = "a_interface"
	paramB          = "b"
	paramBInterface = "b_interface"
)

// ends is a link's two ends as a task names them.
type ends struct {
	lab, a, aIface, b, bIface string
}

// named reports whether any end was named.
func (e ends) named() bool { return e.a != "" || e.aIface != "" || e.b != "" || e.bIface != "" }

// readEnds reads the lab and a link's two ends from params; with required,
// all four end parameters must be set, and otherwise all four or none.
func readEnds(params map[string]any, required bool) (ends, error) {
	e := ends{
		lab: sdk.StringParam(params, paramLab),
		a:   sdk.StringParam(params, paramA), aIface: sdk.StringParam(params, paramAInterface),
		b: sdk.StringParam(params, paramB), bIface: sdk.StringParam(params, paramBInterface),
	}
	if e.lab == "" {
		return ends{}, fmt.Errorf("%s is required", paramLab)
	}
	all := e.a != "" && e.aIface != "" && e.b != "" && e.bIface != ""
	if (required || e.named()) && !all {
		return ends{}, fmt.Errorf("a link's ends are %s, %s, %s and %s, all four", paramA, paramAInterface, paramB, paramBInterface)
	}
	return e, nil
}

// resolve logs in for the task and finds the lab, both ends and the lab's
// links.
func resolve(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, e ends) (*cml.Client, cml.Lab, cml.Endpoint, cml.Endpoint, []cml.Link, error) {
	client, err := cml.ForTask(ctx, rc, device)
	if err != nil {
		return nil, cml.Lab{}, cml.Endpoint{}, cml.Endpoint{}, nil, err
	}
	lab, err := client.FindLab(ctx, e.lab)
	if err != nil {
		return nil, cml.Lab{}, cml.Endpoint{}, cml.Endpoint{}, nil, err
	}
	nodes, err := client.Nodes(ctx, lab.ID)
	if err != nil {
		return nil, cml.Lab{}, cml.Endpoint{}, cml.Endpoint{}, nil, err
	}
	a, err := client.FindEndpoint(ctx, lab.ID, nodes, e.a, e.aIface)
	if err != nil {
		return nil, cml.Lab{}, cml.Endpoint{}, cml.Endpoint{}, nil, err
	}
	b, err := client.FindEndpoint(ctx, lab.ID, nodes, e.b, e.bIface)
	if err != nil {
		return nil, cml.Lab{}, cml.Endpoint{}, cml.Endpoint{}, nil, err
	}
	links, err := client.Links(ctx, lab.ID)
	if err != nil {
		return nil, cml.Lab{}, cml.Endpoint{}, cml.Endpoint{}, nil, err
	}
	return client, lab, a, b, links, nil
}

// endsOf names link's two ends as the parameters of virt.cml.link.create
// that would make it again: each node by its id, so a node removed and made
// again under the same label in between is never linked in its place, and
// each interface by its label. It also describes them by label, for people.
func endsOf(ctx context.Context, client *cml.Client, lab string, nodes []cml.Node, link cml.Link) (map[string]any, string, error) {
	end := func(nodeID, ifaceID string) (string, string, error) {
		for _, n := range nodes {
			if n.ID != nodeID {
				continue
			}
			ifaces, err := client.NodeInterfaces(ctx, lab, n.ID)
			if err != nil {
				return "", "", err
			}
			for _, i := range ifaces {
				if i.ID == ifaceID {
					return n.Label, i.Label, nil
				}
			}
		}
		return "", "", fmt.Errorf("link %s names an interface the lab does not hold", link.ID)
	}
	a, aIface, err := end(link.NodeA, link.InterfaceA)
	if err != nil {
		return nil, "", err
	}
	b, bIface, err := end(link.NodeB, link.InterfaceB)
	if err != nil {
		return nil, "", err
	}
	params := map[string]any{paramA: link.NodeA, paramAInterface: aIface, paramB: link.NodeB, paramBInterface: bIface}
	return params, fmt.Sprintf("%s %s and %s %s", a, aIface, b, bIface), nil
}
