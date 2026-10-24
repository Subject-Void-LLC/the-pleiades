// Package cml: the client a virt.cml task works through, logged in from
// its target device, with what the device's settings make the run say.
package cml

import (
	"context"
	"errors"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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

// Unchecked is err, except in a check, where a lab, node or interface that
// does not exist (ErrNotFound) makes the call unchecked rather than failed:
// an earlier task in the same run may be what makes it.
func Unchecked(mode collection.Mode, err error) error {
	if mode == collection.ModeCheck && errors.Is(err, ErrNotFound) {
		return collection.CannotCheck(err.Error() + " yet; a real run fails on it unless an earlier task in the run makes it")
	}
	return err
}
