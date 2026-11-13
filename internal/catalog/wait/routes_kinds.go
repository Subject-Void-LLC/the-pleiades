// Package wait: the kinds of router wait.routes reads, the alternatives its
// manifest declares, and the reader each one opens.
package wait

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// tableReader reads a router's routing table once, in FRR's route shape.
type tableReader func(context.Context) (map[string][]route, error)

// routeKind is one kind of router wait.routes reads: the alternative its
// manifest declares for it, and how to open a reader of its table, with
// how to close what that opened.
type routeKind struct {
	alt  collection.Alternative
	open func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (tableReader, func(), error)
}

// routeKinds are the routers wait.routes reads, in the order a device that
// is both is read as: FRR's table through vtysh over SSH, then Cisco IOS's
// through its CLI. The manifest's alternatives are built from it
// (routeAlternatives) and routeReader opens the reader of the alternative
// collection.ChooseAlternative picks, so there is no list beside the
// manifest to drift from it.
var routeKinds = []routeKind{
	{
		alt: collection.Alternative{
			Name:                 "frr",
			SupportedTransports:  []string{capability.TransportSSH},
			RequiredCapabilities: []capability.Name{capability.NameFRR, capability.NameCommandExec},
		},
		open: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (tableReader, func(), error) {
			conn, err := sdk.Connect(ctx, rc, device, params, routesFQCN)
			if err != nil {
				return nil, nil, err
			}
			return func(ctx context.Context) (map[string][]route, error) { return readFRRRoutes(ctx, conn) }, func() { _ = conn.Close() }, nil
		},
	},
	{
		alt: collection.Alternative{
			Name:                 "ios",
			SupportedTransports:  []string{capability.TransportSSH, capability.TransportTelnet, capability.TransportConsole},
			RequiredCapabilities: []capability.Name{capability.NameCiscoIOS},
		},
		open: func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (tableReader, func(), error) {
			session, err := sdk.OpenCLI(ctx, rc, device, params, routesFQCN, netcli.IOS)
			if err != nil {
				return nil, nil, err
			}
			return func(ctx context.Context) (map[string][]route, error) { return readIOSRoutes(ctx, session) }, func() { _ = session.Close() }, nil
		},
	},
}

// routeAlternatives is every routeKind's alternative, in order, for the
// manifest.
func routeAlternatives() []collection.Alternative {
	alts := make([]collection.Alternative, len(routeKinds))
	for i, k := range routeKinds {
		alts[i] = k.alt
	}
	return alts
}

// chooseRouteKind is the routeKind of device: the one whose alternative
// collection.ChooseAlternative picks from the manifest, the choice
// collection.Admits made at plan time and admission. It refuses a device
// that is none of them, and a missing device, before anything is opened:
// a grant can lapse after admission, so the run checks again.
func chooseRouteKind(device inventory.InventoryItem) (routeKind, error) {
	if device == nil {
		return routeKind{}, fmt.Errorf("%s: no target device: set the task's target or the runbook's hosts", routesFQCN)
	}
	alt, err := collection.ChooseAlternative(device, routesFQCN, routesManifest())
	if err != nil {
		return routeKind{}, err
	}
	for _, k := range routeKinds {
		if k.alt.Name == alt.Name {
			return k, nil
		}
	}
	// ChooseAlternative picks only from the manifest, which routeKinds
	// built, so this is a manifest built from something else.
	return routeKind{}, fmt.Errorf("%s: no reader for the alternative %q", routesFQCN, alt.Name)
}

// routeReader returns how to read device's routing table, and how to close
// what it opened, for the kind of router device is (chooseRouteKind). A
// device that is none of them is refused before anything is opened.
func routeReader(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (tableReader, func(), error) {
	kind, err := chooseRouteKind(device)
	if err != nil {
		return nil, nil, err
	}
	return kind.open(ctx, rc, device, params)
}
