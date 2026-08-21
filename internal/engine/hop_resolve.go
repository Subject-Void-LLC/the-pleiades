package engine

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// hopChainInventory is the narrow slice of inventory.Repository
// ResolveRoute actually needs: looking up a named hop's own inventory
// item, and walking a device's group/inventory ancestry. Interface
// Segregation (PATTERNS.md, already this codebase's own stated
// convention, e.g. pkg/inventory.InventoryItem's own doc comment on
// versioned): transportActionExecutor has no business depending on
// Create, Save or Retire to run a command.
type hopChainInventory interface {
	GetByName(ctx context.Context, name string) (pkginventory.InventoryItem, error)
	GroupAncestry(ctx context.Context, deviceName string) ([]inventory.HierarchyLayer, error)
}

// routePropertyKey is the Properties key a Group's, Inventory's, or
// Device's own settings bag carries its configured hop chain under: an
// ordered list of device names, nearest hop first, the target itself
// last of all (implicit; not part of the stored value).
const routePropertyKey = "route"

// maxRouteHops bounds how many hops a single resolved route may name,
// checked once in ResolveRoute against the final, folded value (the
// resolved route's own Layer already recorded which single layer
// contributed it) rather than against every ancestor's own raw
// Properties: a less specific layer's pathological value that a more
// specific layer legitimately overrides never reaches this check at
// all, since ResolveHierarchy's override semantics mean only the
// winning layer's value is ever inspected here.
//
// This is the "reject at parse/resolve time, not dial time" requirement
// Phase 72 owes: without a bound, a route naming an absurd number of
// hops would resolve "successfully" into a huge slice and only fail (or
// hang) one dial attempt at a time, against real inventory lookups and
// real credential lookups, for however many hops an attacker-controlled
// or simply mistaken configuration named. 16 is generous against any
// real bastion topology (this Part's own proof stays at one or two
// hops) while still bounding the pathological case to a handful of
// wasted lookups instead of thousands.
const maxRouteHops = 16

// routeExtract pulls an ordered list of hop device names out of one
// layer's Properties bag, reporting false when "route" is absent or is
// not a list of strings. A malformed entry (a route key present but not
// a valid list of strings) is treated as "no opinion" rather than a hard
// error here: ResolveRoute's own caller sees a clean, empty route rather
// than plumbing a config-validation error through a hierarchy fold that
// exists to answer "what does the hierarchy say," not to validate what
// an operator typed.
func routeExtract(props map[string]interface{}) ([]string, bool) {
	raw, ok := props[routePropertyKey]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]interface{})
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		names = append(names, s)
	}
	return names, true
}

// ResolveRoute resolves dev's configured bastion/hop-chain route into a
// fully-formed transport.Hop chain: the ordered device names
// ResolveHierarchy folds from dev's group/inventory ancestry
// (repo.GroupAncestry), with dev's own Properties applied last as the
// most specific override (AGENTS.md's hierarchical policy principle,
// most specific wins), then each named hop resolved to a real address
// and a real, independently-looked-up credential.
//
// A hop named in configuration but not found in inventory, not
// SSHTransportCapable, or with no stored credential of its own is a hard
// error naming that hop. This is the decision Phase 72 settled and
// records here in code: a hop never falls back to the target's own
// credential, because a bastion's account and a production device's
// account are almost never the same, and treating them as interchangeable
// would risk sending the wrong secret to the wrong host.
//
// No configured route (nothing at any level) returns a nil slice and a
// nil error: exactly transport.Target's own "empty Route is a direct
// connection" contract.
func ResolveRoute(ctx context.Context, repo hopChainInventory, credentials credential.Store, dev pkginventory.InventoryItem) ([]transport.Hop, error) {
	ancestry, err := repo.GroupAncestry(ctx, dev.Name())
	if err != nil {
		return nil, fmt.Errorf("resolving hop-chain hierarchy for %s: %w", dev.Name(), err)
	}

	resolved := ResolveHierarchy[[]string](nil, ancestry, routeExtract)
	if deviceRoute, ok := routeExtract(dev.Properties().Raw()); ok {
		resolved.Value = deviceRoute
	}

	if len(resolved.Value) == 0 {
		return nil, nil
	}
	if len(resolved.Value) > maxRouteHops {
		return nil, fmt.Errorf("route for %s names %d hops, more than the %d this platform permits", dev.Name(), len(resolved.Value), maxRouteHops)
	}

	hops := make([]transport.Hop, 0, len(resolved.Value))
	for _, hopName := range resolved.Value {
		hopItem, err := repo.GetByName(ctx, hopName)
		if err != nil {
			return nil, fmt.Errorf("hop %q: %w", hopName, err)
		}
		sshDev, ok := hopItem.(capability.SSHTransportCapable)
		if !ok {
			return nil, fmt.Errorf("hop %q does not declare an SSH transport target", hopName)
		}
		cred, err := credentials.Lookup(ctx, hopName)
		if err != nil {
			return nil, fmt.Errorf("hop %q: %w", hopName, err)
		}
		hops = append(hops, transport.Hop{
			Host:       sshDev.SSHHost(),
			Port:       sshDev.SSHPort(),
			DeviceName: hopName,
			Credential: cred,
		})
	}
	return hops, nil
}
