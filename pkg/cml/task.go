// Package cml: the client a virt.cml task works through, logged in from
// its target device, with what the device's settings make the run say.
package cml

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ForTask logs in to the controller device stands for, with the device's
// own credential from rc, and records the warnings its settings carry (a
// pinned certificate, a login over plain HTTP) on the task, every run.
func ForTask(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem) (*Client, error) {
	client, _, warnings, err := ClientFor(ctx, device, device.Name(), rc.InjectSecrets())
	if len(warnings) > 0 {
		if werr := sdk.RecordWarnings(rc, warnings); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return nil, err
	}
	return client, nil
}
