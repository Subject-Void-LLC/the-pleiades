// Package entstore: finishing a change of the facts key without waiting for
// every device to be read or written (rekey.go), modeled on
// internal/crypto's RotateFactPayloads.
package entstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	entdevice "github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	entfact "github.com/Subject-Void-LLC/the-pleiades/internal/ent/fact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// rekeyPage is how many devices one page of RekeyAll reads: a fleet is far
// more devices than fit in memory at once, and each is re-keyed in a
// transaction of its own.
const rekeyPage = 200

// MaxRefusedNamed bounds the devices RekeyCount names as refused.
const MaxRefusedNamed = 20

// RekeyCount is what one pass of RekeyAll did. It names devices by their ID
// and counts rows, never a key, a token or a value.
type RekeyCount struct {
	// Devices were re-keyed by this pass: every row, coverage and
	// observation of each is under the current keys now.
	Devices int
	// Facts, Coverages and Observations count the rows those re-keys
	// rewrote, and Dropped the coverage tokens no row held.
	Facts, Coverages, Observations, Dropped int
	// Refused devices hold rows that do not verify under the previous keys,
	// or rows under both keys, or under a third (ErrRekeyRefused,
	// ErrKeysChanged). They are left exactly as they were; removing the
	// previous key makes them unreadable for good.
	Refused int
	// RefusedDevices names the first MaxRefusedNamed of them.
	RefusedDevices []string
	// Failed devices hit a database error; a later pass, or the device's
	// next read or write, tries again.
	Failed int
	// Remaining is how many devices still hold a row under the previous keys
	// once the pass ends, read after it: refused and failed devices, and any
	// a replica still holding only the previous key wrote to meanwhile.
	Remaining int
}

// Complete reports whether no device needs the previous keys any more.
func (c RekeyCount) Complete() bool { return c.Remaining == 0 }

// RekeyAll re-keys every device whose rows still name the previous keys, a
// page of devices at a time in id order, each in a transaction of its own
// (rekey.go), and reports what it did. A device another replica re-keys
// meanwhile is found already done. A device refused or failed is counted
// and left; the pass goes on. With no previous keys there is nothing to do.
// An error is returned only when the devices cannot be listed or counted,
// or ctx ends.
func (s *Store) RekeyAll(ctx context.Context) (RekeyCount, error) {
	var count RekeyCount
	previous := s.previousID()
	if previous == "" {
		return count, nil
	}
	if err := s.ready(); err != nil {
		return count, err
	}
	after := 0
	for {
		devices, err := s.client.Device.Query().
			Where(entdevice.IDGT(after), entdevice.HasFactsWith(entfact.KeyedByEQ(previous))).
			Order(ent.Asc(entdevice.FieldID)).
			Limit(rekeyPage).
			Select(entdevice.FieldDeviceID).
			All(ctx)
		if err != nil {
			return count, fmt.Errorf("facts: listing devices to re-key: %w", err)
		}
		if len(devices) == 0 {
			break
		}
		for _, d := range devices {
			after = d.ID
			done, err := s.settle(ctx, d.ID, d.DeviceID)
			switch {
			case err == nil:
				if done.facts > 0 {
					count.Devices++
				}
				count.Facts += done.facts
				count.Coverages += done.coverages
				count.Observations += done.observations
				count.Dropped += done.dropped
			case ctx.Err() != nil:
				return count, ctx.Err()
			case errors.Is(err, ErrRekeyRefused) || errors.Is(err, ErrKeysChanged):
				count.Refused++
				if len(count.RefusedDevices) < MaxRefusedNamed {
					count.RefusedDevices = append(count.RefusedDevices, d.DeviceID)
				}
				// The reason is the store's own text: a row's place, or a
				// chain break's, never a stored key or value.
				slog.Warn("not re-keying a device's stored facts under the new facts key",
					slog.String("device", termsafe.Quote(d.DeviceID)), slog.String("reason", err.Error()))
			default:
				count.Failed++
				slog.Warn("failed to re-key a device's stored facts under the new facts key, will retry on a later pass or its next read",
					slog.String("device", termsafe.Quote(d.DeviceID)), slog.String("error", err.Error()))
			}
		}
	}
	remaining, err := s.client.Device.Query().
		Where(entdevice.HasFactsWith(entfact.KeyedByEQ(previous))).
		Count(ctx)
	if err != nil {
		return count, fmt.Errorf("facts: counting devices left to re-key: %w", err)
	}
	count.Remaining = remaining
	return count, nil
}
