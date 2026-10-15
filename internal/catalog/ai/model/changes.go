// What every model method shares between reading the task and acting.
package model

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// setStats sets each stat.
func setStats(rc sdk.RunbookContext, stats map[string]any) error {
	for k, v := range stats {
		if err := rc.SetStat(k, v); err != nil {
			return err
		}
	}
	return nil
}

// begin is what every changing method does first: the guarded host, the
// model, a bounded context, and what the host has.
func begin(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (host, string, context.Context, context.CancelFunc, []string, []string, error) {
	h, err := hostFor(rc, device, true)
	if err != nil {
		return host{}, "", nil, nil, nil, nil, err
	}
	model, err := modelParam(params)
	if err != nil {
		return host{}, "", nil, nil, nil, nil, err
	}
	timeout, err := timeoutParam(params)
	if err != nil {
		return host{}, "", nil, nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	disk, memory, err := h.present(ctx)
	if err != nil {
		cancel()
		return host{}, "", nil, nil, nil, nil, err
	}
	return h, model, ctx, cancel, disk, memory, nil
}
