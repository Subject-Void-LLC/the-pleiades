package crypto

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
)

// RotateDeviceProperties re-encrypts every Device row's properties under
// svc's current key and version (PLAN.md Section 17.2's "the system loads
// a secondary key, decrypts secrets using the old key, and re-encrypts
// them with the new key"). client must already have
// DeviceEnvelopePropertiesHook and DeviceEnvelopePropertiesInterceptor
// installed with svc: reads go through the interceptor, so a row still on
// svc's previous version decrypts correctly; writes go through the hook,
// so every row this function touches ends up re-encrypted under svc's
// current version.
//
// Every row with non-nil, decryptable Properties is unconditionally
// re-encrypted on each call, even one already on the current version — a
// deliberate simplification given this platform's expected inventory
// scale (a device fleet, not a hyperscale table), not an oversight.
// Determining "is this row already current" without re-encrypting it
// would require a second connection bypassing the registered interceptor,
// since the interceptor consumes and discards the stored version tag
// before any caller of client.Device.Query sees it.
//
// A row whose properties fail to decrypt under svc (an unrecognized key
// version, corrupted ciphertext, or any other decrypt failure) is skipped
// and logged, never aborting the whole pass:
// DeviceEnvelopePropertiesInterceptor (device_hook.go) leaves such a row's
// Properties in its raw, still-encrypted shape rather than erroring the
// whole batch query, and isAlreadyEncryptedShape recognizes that exact
// shape here to skip a pointless no-op re-write of it.
//
// Each successful write is conditional on the row's stored version column
// still matching what was just read
// (client.Device.Update().Where(device.IDEQ, device.VersionEQ), the same
// compare-and-swap shape internal/inventory's own entRepository.Save
// uses), deliberately WITHOUT setting Version itself: re-wrapping a DEK
// under a new KEK is not a domain content change, so the row's
// optimistic-concurrency counter must stay exactly what it was. This
// closes a real race an adversarial review of this phase caught in an
// earlier draft, which wrote via UpdateOneID with no conditional at all:
// a legitimate concurrent domain write landing between this function's
// read and write was silently overwritten (lost), while the stored
// version column was left describing the concurrent writer's content, not
// the properties this function had just persisted. A row that loses this
// compare-and-swap (affected == 0) is skipped, not retried and not
// treated as an error: it stays on its current key version until a later
// rotation pass picks it up, which is safe for as long as that version's
// key remains configured in svc's previous slot.
//
// This deliberately bypasses internal/inventory's entRepository.Save
// itself (as opposed to reusing its exact CAS shape, which it does): this
// is an infrastructure maintenance operation on the storage
// representation, not a domain mutation recording a Revision, and Save's
// own audit-trail bookkeeping has nothing to record here since the
// content itself never changes.
//
// Operational note on multi-key rotation: svc supports exactly one
// previous key/version at a time. Do not start a second rotation (moving
// today's current key into the previous slot for a third key) until this
// function's returned count equals the total device row count with
// non-nil properties; starting one early can permanently strand any row
// that had not yet been re-encrypted, since its key would no longer be
// configured anywhere.
func RotateDeviceProperties(ctx context.Context, client *ent.Client, svc *EnvelopeService) (int, error) {
	rows, err := client.Device.Query().All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to list devices for key rotation: %w", err)
	}

	rotated := 0
	for _, row := range rows {
		if row.Properties == nil {
			continue
		}
		if isAlreadyEncryptedShape(row.Properties) {
			slog.Warn("skipping device with undecryptable properties during key rotation",
				slog.String("device", row.Name))
			continue
		}

		affected, err := client.Device.Update().
			Where(device.IDEQ(row.ID), device.VersionEQ(row.Version)).
			SetProperties(row.Properties).
			Save(ctx)
		if err != nil {
			slog.Warn("failed to rotate device properties, will retry on a later pass",
				slog.String("device", row.Name), slog.String("error", err.Error()))
			continue
		}
		if affected == 0 {
			slog.Warn("device changed concurrently during key rotation, will retry on a later pass",
				slog.String("device", row.Name))
			continue
		}
		rotated++
	}

	return rotated, nil
}
