// container.network.info: read a network as the engine reports it.
package network

import (
	"context"
	"fmt"
	"maps"
	"slices"

	moby "github.com/moby/moby/api/types/network"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const fqcnInfo = "container.network.info"

// Returns only info makes.
const (
	statScope      = "scope"
	statLabels     = "labels"
	statOptions    = "options"
	statContainers = "containers"
)

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnInfo,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Notes:    "Reading is not changing: the method reads one network and changes nothing.",
			ReadOnly: true,
		}, infoDoc()),
		Invoke: info,
		// Reading is the same in a check: Check is Invoke.
		Check: info,
	})
}

// info is container.network.info's body: the network named, or exists
// false when there is none.
func info(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	name, err := sdk.RequiredStringParam(params, paramName)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnInfo, err)
	}
	if err := containerengine.ValidName("network", name); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnInfo, err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnInfo, containerengine.OpNetworkInspect)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	live, exists, err := inspectNetwork(ctx, s, name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnInfo, err)
	}
	if err := recordStats(rc, map[string]any{statName: name, statExists: exists}); err != nil {
		return collection.Result{}, err
	}
	if !exists {
		return collection.Result{}, nil
	}
	return collection.Result{}, recordStats(rc, describe(live))
}

// describe is what info reports of a live network.
func describe(live moby.Inspect) map[string]any {
	subnets := make([]map[string]any, 0, len(live.IPAM.Config))
	for _, cfg := range live.IPAM.Config {
		entry := map[string]any{paramSubnet: cfg.Subnet.String()}
		if cfg.Gateway.IsValid() {
			entry[paramGateway] = cfg.Gateway.String()
		}
		if cfg.IPRange.IsValid() {
			entry[paramIPRange] = cfg.IPRange.String()
		}
		subnets = append(subnets, entry)
	}
	containers := make([]string, 0, len(live.Containers))
	for _, ep := range live.Containers {
		containers = append(containers, ep.Name)
	}
	slices.Sort(containers)
	labels := maps.Clone(live.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	options := maps.Clone(live.Options)
	if options == nil {
		options = map[string]string{}
	}
	return map[string]any{
		statID: live.ID, statDriver: live.Driver, statScope: live.Scope,
		statInternal: live.Internal, statIPv6: live.EnableIPv6,
		statSubnets: subnets, statLabels: labels, statOptions: options, statContainers: containers,
	}
}
