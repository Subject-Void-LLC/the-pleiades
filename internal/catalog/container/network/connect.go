// container.network.connect and disconnect: a container joining or leaving
// a network, each through one body its check shares, each recording the
// other as its undo.
package network

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	ctr "github.com/moby/moby/api/types/container"
	moby "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const (
	fqcnConnect    = "container.network.connect"
	fqcnDisconnect = "container.network.disconnect"

	paramAliases = "aliases"
	paramIPv4    = "ipv4_address"

	statNetwork     = "network"
	statContainer   = "container"
	statNetworkID   = "network_id"
	statContainerID = "container_id"
	statConnected   = "connected"
)

// guards are the parameters that pin an undo to the objects it was
// recorded against.
var guards = []string{paramNetwork, paramContainer, paramNetworkID, paramContainerID}

func init() {
	collection.MustRegister(collection.Descriptor{
		Name: fqcnConnect,
		Manifest: containerengine.MethodManifest(collection.Reversibility{
			Reversible: true,
			Notes:      "A run that connected the container records disconnecting it, by both IDs; one that found it connected as declared records nothing.",
			Inverses:   []sdk.InverseSpec{{FQCN: fqcnDisconnect, Record: append(slices.Clone(guards), containerengine.ParamEngine)}},
		}, connectDoc()),
		Invoke: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return connect(ctx, rc, d, p, collection.ModeExecute)
		},
		Check: func(ctx context.Context, rc sdk.RunbookContext, d inventory.InventoryItem, p map[string]any) (collection.Result, error) {
			return connect(ctx, rc, d, p, collection.ModeCheck)
		},
	})
}

// pair is a network and a container, as both a connect and a disconnect
// read them.
type pair struct {
	network   moby.Inspect
	container ctr.InspectResponse
	// endpoint is the container's place on the network, nil when it has
	// none.
	endpoint *moby.EndpointSettings
}

// names reads and checks the network and container a task names.
func names(params map[string]any) (string, string, error) {
	network, err := sdk.RequiredStringParam(params, paramNetwork)
	if err != nil {
		return "", "", err
	}
	if err := containerengine.ValidName("network", network); err != nil {
		return "", "", err
	}
	if network == "host" || network == "none" {
		return "", "", fmt.Errorf("network %s is a container's whole network mode, which is set when it is created, never joined", network)
	}
	container, err := sdk.RequiredStringParam(params, paramContainer)
	if err != nil {
		return "", "", err
	}
	if err := containerengine.ValidName("container", container); err != nil {
		return "", "", err
	}
	return network, container, nil
}

// read reads the network and the container a task names, refusing either
// when its ID is not the one the task recorded. present is false when
// either is missing.
func read(ctx context.Context, rc sdk.RunbookContext, s *containerengine.Session, params map[string]any, network, container string) (pair, bool, error) {
	if err := recordStats(rc, map[string]any{statNetwork: network, statContainer: container}); err != nil {
		return pair{}, false, err
	}
	net, exists, err := inspectNetwork(ctx, s, network)
	if err != nil || !exists {
		return pair{}, false, err
	}
	if err := guardID(params, paramNetworkID, "network", network, net.ID); err != nil {
		return pair{}, false, err
	}
	res, err := s.API.ContainerInspect(ctx, container, client.ContainerInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return pair{}, false, nil
	}
	if err != nil {
		return pair{}, false, fmt.Errorf("reading container %s: %w", container, err)
	}
	c := res.Container
	if strings.TrimPrefix(c.Name, "/") != container && c.ID != container {
		return pair{}, false, nil
	}
	if !engineID.MatchString(c.ID) {
		return pair{}, false, fmt.Errorf("the engine answered an unusable ID for container %s", container)
	}
	if err := guardID(params, paramContainerID, "container", container, c.ID); err != nil {
		return pair{}, false, err
	}
	if err := recordStats(rc, map[string]any{statNetworkID: net.ID, statContainerID: c.ID}); err != nil {
		return pair{}, false, err
	}
	p := pair{network: net, container: c}
	if c.NetworkSettings != nil {
		p.endpoint = c.NetworkSettings.Networks[net.Name]
	}
	return p, true, nil
}

// endpointWant is the place on a network a connect declares.
type endpointWant struct {
	aliases []string
	ipv4    netip.Addr
}

// parseEndpoint reads a connect's aliases and static address.
func parseEndpoint(params map[string]any) (endpointWant, error) {
	var w endpointWant
	aliases, err := textList(params, paramAliases)
	if err != nil {
		return w, err
	}
	for _, a := range aliases {
		if err := containerengine.ValidName("alias", a); err != nil {
			return w, err
		}
	}
	w.aliases = aliases
	if raw, err := containerengine.TextParam(params, paramIPv4); err != nil {
		return w, err
	} else if raw != "" {
		if w.ipv4, err = netip.ParseAddr(raw); err != nil || !w.ipv4.Is4() {
			return w, fmt.Errorf("%s: %q is not an IPv4 address", paramIPv4, raw)
		}
	}
	return w, nil
}

// textList reads a list of text: a list from a runbook, or the list of
// text an undo recorded in this process.
func textList(params map[string]any, key string) ([]string, error) {
	if list, ok := params[key].([]string); ok {
		return slices.Clone(list), nil
	}
	list, _, err := sdk.StringSlice(params, key)
	return list, err
}

// differences names what a container already on the network lacks of the
// declared place on it.
func (w endpointWant) differences(ep *moby.EndpointSettings) []string {
	var d []string
	for _, a := range w.aliases {
		if !slices.Contains(ep.Aliases, a) {
			d = append(d, fmt.Sprintf("%s: %s is not among %v", paramAliases, a, ep.Aliases))
		}
	}
	if w.ipv4.IsValid() {
		var have netip.Addr
		if ep.IPAMConfig != nil {
			have = ep.IPAMConfig.IPv4Address
		}
		if have != w.ipv4 {
			d = append(d, fmt.Sprintf("%s: %s, the task names %s", paramIPv4, have, w.ipv4))
		}
	}
	return d
}

// connect is container.network.connect's body: a container not on the
// network joins it; one on it as declared is no change; one on it
// otherwise is refused. A container whose network mode is none, host or
// another container's has no network to join, and is refused.
func connect(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, mode collection.Mode) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnConnect, err)
	}
	network, container, err := names(params)
	if err != nil {
		return fail(err)
	}
	want, err := parseEndpoint(params)
	if err != nil {
		return fail(err)
	}
	s, err := containerengine.Open(ctx, rc, device, params, fqcnConnect,
		containerengine.OpNetworkInspect, containerengine.OpContainerInspect, containerengine.OpNetworkConnect)
	if err != nil {
		return collection.Result{}, err
	}
	defer s.Close()
	p, present, err := read(ctx, rc, s, params, network, container)
	if err != nil {
		return fail(err)
	}
	if !present {
		return fail(fmt.Errorf("network %s and container %s must both exist", network, container))
	}
	if p.endpoint != nil {
		if diffs := want.differences(p.endpoint); len(diffs) > 0 {
			return fail(fmt.Errorf("container %s is on network %s otherwise than the task declares, and is not changed here; disconnect it and connect it again:\n  %s", container, network, strings.Join(diffs, "\n  ")))
		}
		return collection.Result{}, rc.SetStat(statConnected, true)
	}
	if p.container.HostConfig != nil {
		if m := p.container.HostConfig.NetworkMode; m.IsNone() || m.IsHost() || m.IsContainer() {
			return fail(fmt.Errorf("container %s runs with network mode %s, which joins no other network; container.recreate it with network %s", container, m, network))
		}
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, rc.SetStat(statConnected, true)
	}
	ep := &moby.EndpointSettings{Aliases: want.aliases}
	if want.ipv4.IsValid() {
		ep.IPAMConfig = &moby.EndpointIPAMConfig{IPv4Address: want.ipv4}
	}
	if _, err := s.API.NetworkConnect(ctx, p.network.ID, client.NetworkConnectOptions{Container: p.container.ID, EndpointConfig: ep}); err != nil {
		return fail(fmt.Errorf("connecting container %s to network %s: %w", container, network, err))
	}
	if err := containerengine.RecordUndo(rc, s, sdk.Inverse{
		FQCN:        fqcnDisconnect,
		Params:      map[string]any{paramNetwork: network, paramContainer: container, paramNetworkID: p.network.ID, paramContainerID: p.container.ID},
		Description: "Disconnect the container this task connected.",
	}); err != nil {
		return collection.Result{}, err
	}
	return collection.Result{Changed: true}, rc.SetStat(statConnected, true)
}
