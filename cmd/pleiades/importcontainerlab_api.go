// pleiades import-containerlab --api: a lab's nodes read from a containerlab
// API server's inspect instead of the topology file containerlab writes on
// deploy, for a lab on a host this machine has no file from.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// inspectMethod is the method the import runs: the same read every runbook
// makes of a lab, with the same login and the same TLS, so the import has no
// path of its own to the server.
const inspectMethod = "virt.clab.lab.info"

// readInspect runs virt.clab.lab.info for lab against the named containerlab
// API server device, with that device's own stored credential, and returns the
// nodes as the topology the import reads. Each node is labelled with the lab
// the server named, and an address is the management address or a published
// SSH port, exactly as the topology file would have given.
func readInspect(ctx context.Context, dir, device, lab string) (clabTopology, error) {
	repo := inv.NewFileRepository(filepath.Join(dir, inv.DefaultInventoryFilename), inv.NewItemFactory())
	item, err := repo.GetByName(ctx, device)
	if err != nil {
		return clabTopology{}, err
	}
	desc, ok := collection.Lookup(inspectMethod)
	if !ok {
		return clabTopology{}, fmt.Errorf("%s is not in this build", inspectMethod)
	}
	if !item.HasCapability(capability.NameClabAPI) {
		return clabTopology{}, fmt.Errorf("device %q is not a containerlab API server onboarding recognised: run pleiades onboard %s first", device, device)
	}
	rc, err := engine.NewCredentialRunbookContext(credential.NewLazyFileStore(dir))(ctx, item)
	if err != nil {
		return clabTopology{}, err
	}
	if _, err := desc.Invoke(ctx, rc, item, map[string]any{"lab": lab}); err != nil {
		return clabTopology{}, err
	}
	facts, ok := rc.(engine.FactCollector)
	if !ok {
		return clabTopology{}, fmt.Errorf("%s's answer could not be read", inspectMethod)
	}
	return inspectTopology(facts.Facts(), device, lab)
}

// inspectTopology reads lab.info's answer for lab on device as a topology.
func inspectTopology(facts map[string]any, device, lab string) (clabTopology, error) {
	if exists, _ := facts["exists"].(bool); !exists {
		return clabTopology{}, fmt.Errorf("the containerlab API server %q has no lab %q deployed", device, lab)
	}
	nodes, _ := facts["nodes"].([]any)
	topo := clabTopology{Name: lab, Nodes: map[string]clabNode{}}
	for _, raw := range nodes {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["node"].(string)
		if name == "" {
			return clabTopology{}, errors.New("the server's inspect names a node with no name")
		}
		n := clabNode{ShortName: name, Labels: map[string]string{"containerlab": lab}}
		n.Kind, _ = m["kind"].(string)
		n.Image, _ = m["image"].(string)
		// The server gives the management address with its mask.
		if cidr, _ := m["ipv4_address"].(string); cidr != "" {
			if p, err := netip.ParsePrefix(cidr); err == nil {
				n.MgmtIPv4 = p.Addr().String()
			} else {
				n.MgmtIPv4 = strings.SplitN(cidr, "/", 2)[0]
			}
		}
		ports, _ := m["ports"].([]any)
		for _, rp := range ports {
			pm, _ := rp.(map[string]any)
			b := struct {
				HostIP   string `json:"host-ip"`
				HostPort int    `json:"host-port"`
				Port     int    `json:"port"`
				Protocol string `json:"protocol"`
			}{}
			b.HostIP, _ = pm["host_ip"].(string)
			b.Protocol, _ = pm["protocol"].(string)
			if v, ok := pm["host_port"].(int); ok {
				b.HostPort = v
			}
			if v, ok := pm["port"].(int); ok {
				b.Port = v
			}
			n.PortBindings = append(n.PortBindings, b)
		}
		topo.Nodes[name] = n
	}
	if len(topo.Nodes) == 0 {
		return clabTopology{}, errors.New("the server's inspect names no nodes")
	}
	return topo, nil
}
