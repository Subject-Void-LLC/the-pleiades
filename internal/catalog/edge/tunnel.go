// edge.tunnel: a Cloudflare Tunnel's status and its connectors.
package edge

import (
	"context"
	"fmt"
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Parameters.
const (
	paramTunnel        = "tunnel"
	paramExpectStatus  = "expect_status"
	paramMinConnectors = "min_connectors"
)

// tunnelStatuses are the statuses Cloudflare reports, and any.
var tunnelStatuses = []string{"healthy", "degraded", "down", "inactive", "any"}

func init() { method(fqcnTunnel, tunnelDoc(), Tunnel) }

// Tunnel implements edge.tunnel.
func Tunnel(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	fail := func(err error) (collection.Result, error) {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcnTunnel, err)
	}
	name, err := sdk.RequiredStringParam(params, paramTunnel)
	if err != nil {
		return fail(err)
	}
	want := sdk.StringParam(params, paramExpectStatus)
	if want == "" {
		want = "healthy"
	}
	if !slices.Contains(tunnelStatuses, want) {
		return fail(fmt.Errorf("%s %q is not one of %v", paramExpectStatus, want, tunnelStatuses))
	}
	minUp, set, err := sdk.IntParam(params, paramMinConnectors)
	if err != nil {
		return fail(err)
	}
	if !set {
		minUp = 1
	}
	if minUp < 0 || minUp > 1000 {
		return fail(fmt.Errorf("%s is %d; it must be from 0 to 1000", paramMinConnectors, minUp))
	}
	c, acct, err := account(rc, device)
	if err != nil {
		return fail(err)
	}
	t, err := c.Tunnel(ctx, acct, name)
	if err != nil {
		return fail(ctxDone(ctx, err))
	}
	conns, err := c.Connectors(ctx, acct, t.ID)
	if err != nil {
		return fail(ctxDone(ctx, err))
	}
	up, connections := 0, 0
	listed := make([]any, 0, len(conns))
	for _, cn := range conns {
		var colos []string
		for _, x := range cn.Conns {
			colos = append(colos, x.ColoName)
		}
		if len(cn.Conns) > 0 {
			up++
		}
		connections += len(cn.Conns)
		listed = append(listed, map[string]any{"id": cn.ID, "version": cn.Version, "arch": cn.Arch, "connections": len(cn.Conns), "colos": strs(colos)})
	}
	var diffs []string
	if want != "any" && t.Status != want {
		diffs = append(diffs, fmt.Sprintf("the tunnel is %s, not %s", t.Status, want))
	}
	if up < minUp {
		diffs = append(diffs, fmt.Sprintf("%d connector(s) are up, fewer than %d", up, minUp))
	}
	return finish(rc, fqcnTunnel, map[string]any{
		"id": t.ID, "name": t.Name, "status": t.Status, "connectors": listed, "connectors_up": up, "connections": connections,
	}, diffs)
}
