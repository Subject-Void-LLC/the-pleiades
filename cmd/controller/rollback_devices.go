// Package main: resolving the devices a rollback undoes changes on, by the
// ids a journal holds, composed over the ent client since the inventory
// repository answers by name only.
package main

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
)

// rollbackDeviceChunk bounds one IN list, well under SQLite's limit on
// bound parameters.
const rollbackDeviceChunk = 500

// deviceNamesByID returns the api.DeviceNamer over client: each id the
// inventory holds, with its name now.
func deviceNamesByID(client *ent.Client) api.DeviceNamer {
	return func(ctx context.Context, ids []string) (map[string]string, error) {
		names := make(map[string]string, len(ids))
		for start := 0; start < len(ids); start += rollbackDeviceChunk {
			rows, err := client.Device.Query().
				Where(entdevice.DeviceIDIn(ids[start:min(start+rollbackDeviceChunk, len(ids))]...)).
				Select(entdevice.FieldDeviceID, entdevice.FieldName).
				All(ctx)
			if err != nil {
				return nil, fmt.Errorf("resolving devices by id: %w", err)
			}
			for _, row := range rows {
				names[row.DeviceID] = row.Name
			}
		}
		return names, nil
	}
}
