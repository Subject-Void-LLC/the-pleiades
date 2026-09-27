package crypto

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/device"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/savedlaunchconfig"
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
// re-encrypted on each call, even one already on the current version - a
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
// (client.Device.UpdateOneID(id).Where(device.VersionEQ), the same
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
// today's current key into the previous slot for a third key) until a pass
// reports RotationCount.Complete, meaning nothing it read was skipped;
// starting one early can permanently strand any row that had not yet been
// re-encrypted, since its key would no longer be configured anywhere.
// RotateAll runs this pass alongside every other encrypted column's.
func RotateDeviceProperties(ctx context.Context, client *ent.Client, svc *EnvelopeService) (RotationCount, error) {
	rows, err := client.Device.Query().All(ctx)
	if err != nil {
		return RotationCount{}, fmt.Errorf("failed to list devices for key rotation: %w", err)
	}

	var count RotationCount
	for _, row := range rows {
		if row.Properties == nil {
			continue
		}
		if isAlreadyEncryptedShape(row.Properties) {
			slog.Warn("skipping device with undecryptable properties during key rotation",
				slog.String("device", row.Name))
			count.Unreadable++
			continue
		}

		// UpdateOne rather than the bulk builder, for the reason Phase 78c
		// gives at every other write site that touches an encrypted
		// column: the hook needs the row's own secret_binding, and ent
		// exposes OldSecretBinding on UpdateOne alone. It also does this
		// pass a second favour. A row written before that column existed
		// carries no binding, and the hook assigns one to any row it
		// updates, so this pass converts the unbound envelope to the bound
		// one as a side effect of re-keying it rather than needing a
		// separate sweep.
		_, err := client.Device.UpdateOneID(row.ID).
			Where(device.VersionEQ(row.Version)).
			SetProperties(row.Properties).
			Save(ctx)
		switch {
		case err == nil:
			count.Rotated++
		case ent.IsNotFound(err):
			count.Skipped++
			slog.Warn("device changed concurrently during key rotation, will retry on a later pass",
				slog.String("device", row.Name))
		default:
			count.Skipped++
			slog.Warn("failed to rotate device properties, will retry on a later pass",
				slog.String("device", row.Name), slog.String("error", err.Error()))
		}
	}

	return count, nil
}

// RotateCredentialInputs re-encrypts every Credential row's inputs under
// svc's current key and version, the credential counterpart of
// RotateDeviceProperties above.
//
// client must already have CredentialInputsHook and
// CredentialInputsInterceptor installed with svc: reads go through the
// interceptor, so a row still on svc's previous version decrypts correctly,
// and writes go through the hook, so every row this function touches ends
// up re-encrypted under svc's current version, bound to that row.
//
// # Three differences from RotateDeviceProperties, each forced rather than
// chosen
//
// It writes through UpdateOne rather than the bulk Update builder, because
// CredentialInputsHook refuses a bulk update that touches inputs outright
// (ErrBulkCredentialInputs). That refusal is correct and is not worked
// around here: each row's ciphertext is bound to that row, so one encrypted
// value written across many rows could be correct for at most one of them.
//
// Its compare-and-swap is on updated_at rather than on a version counter,
// because Credential has no version column. The property needed is the same
// (do not overwrite a concurrent writer's content with the values read
// before they wrote) and updated_at supplies it: TimestampMixin resets it on
// every update, so a row that changed between this function's read and its
// write no longer matches. The honest difference is granularity. A counter
// cannot collide; two writes landing in the same clock tick could. Both
// dialects store sub-second precision, so that window is theoretical rather
// than practical, and losing the race is safe anyway: the row is skipped and
// stays on its current key version until a later pass.
//
// It cannot skip a row that is already current, for the reason
// RotateDeviceProperties gives at length: the interceptor consumes the
// stored version tag before any caller sees it. Every decryptable row is
// unconditionally re-encrypted on each call.
//
// A row whose inputs failed to decrypt is skipped and logged rather than
// aborting the pass. With the bound envelope that failure is either a
// corrupted row OR a ciphertext that does not belong to it, and nothing here
// can tell them apart, which is why both are reported the same way and
// neither is dismissed.
func RotateCredentialInputs(ctx context.Context, client *ent.Client, svc *EnvelopeService) (RotationCount, error) {
	rows, err := client.Credential.Query().All(ctx)
	if err != nil {
		return RotationCount{}, fmt.Errorf("failed to list credentials for key rotation: %w", err)
	}

	var count RotationCount
	for _, row := range rows {
		if len(row.Inputs) == 0 {
			continue
		}
		if isUndecryptedInputs(row.Inputs) {
			slog.Warn("skipping credential with undecryptable inputs during key rotation; this is either a corrupted row or a ciphertext that does not belong to it",
				slog.Int("credential", row.ID))
			count.Unreadable++
			continue
		}

		_, err := client.Credential.UpdateOneID(row.ID).
			Where(credential.UpdatedAtEQ(row.UpdatedAt)).
			SetInputs(row.Inputs).
			Save(ctx)
		switch {
		case err == nil:
			count.Rotated++
		case ent.IsNotFound(err):
			count.Skipped++
			// The compare-and-swap lost: this row changed between the read
			// above and this write. Skipped rather than retried, matching
			// RotateDeviceProperties, and safe for as long as the previous
			// key remains configured in svc.
			slog.Warn("credential changed concurrently during key rotation, will retry on a later pass",
				slog.Int("credential", row.ID))
		default:
			count.Skipped++
			slog.Warn("failed to rotate credential inputs, will retry on a later pass",
				slog.Int("credential", row.ID), slog.String("error", err.Error()))
		}
	}

	return count, nil
}

// isUndecryptedInputs reports whether a credential's inputs came back from
// the interceptor still sealed.
//
// The check is the strict shape encryptCredentialInputs itself produces, a
// single EncryptedKeyMarker key, for the same reason
// isAlreadyEncryptedShape is strict on the Device side: a laxer test that
// merely looked for the marker's presence would let a credential holding a
// real input named EncryptedKeyMarker, alongside others, be mistaken for an
// unreadable row and silently skipped by every rotation forever.
func isUndecryptedInputs(inputs map[string]string) bool {
	if len(inputs) != 1 {
		return false
	}
	_, marked := inputs[EncryptedKeyMarker]
	return marked
}

// RotateSavedLaunchConfigAnswers re-encrypts every SavedLaunchConfig row's
// answers under svc's current key and version.
//
// client must already have SavedLaunchConfigAnswersHook and
// SavedLaunchConfigAnswersInterceptor installed with svc.
//
// # Why this pass matters more than the other two
//
// A Device migrates itself. Its properties are written by ordinary
// operation, so the hook converts a pre-Phase-78c unbound row to the bound
// form the first time anything touches it, and this kind of pass is the
// sweep for rows nobody touches. A saved launch configuration is different:
// nothing in this platform ever updates its answers. internal/launch's
// store creates one and reads it back, and there is no edit path at all.
//
// So for this entity a rotation pass is not a sweep, it is the ONLY way a
// row written before that phase ever becomes bound. A deployment that never
// runs one keeps every stored survey answer in the relocatable form
// indefinitely, and survey answers are the one path by which a password
// reaches a stored row at all, which is what encrypting them was for.
//
// Its compare-and-swap is on updated_at, for the reason
// RotateCredentialInputs gives: this entity has no version column either.
func RotateSavedLaunchConfigAnswers(ctx context.Context, client *ent.Client, svc *EnvelopeService) (RotationCount, error) {
	rows, err := client.SavedLaunchConfig.Query().All(ctx)
	if err != nil {
		return RotationCount{}, fmt.Errorf("failed to list saved launch configurations for key rotation: %w", err)
	}

	var count RotationCount
	for _, row := range rows {
		if len(row.Answers) == 0 {
			continue
		}
		if isAlreadyEncryptedShape(row.Answers) {
			slog.Warn("skipping saved launch configuration with undecryptable answers during key rotation",
				slog.Int("saved_launch_config", row.ID))
			count.Unreadable++
			continue
		}

		_, err := client.SavedLaunchConfig.UpdateOneID(row.ID).
			Where(savedlaunchconfig.UpdatedAtEQ(row.UpdatedAt)).
			SetAnswers(row.Answers).
			Save(ctx)
		switch {
		case err == nil:
			count.Rotated++
		case ent.IsNotFound(err):
			count.Skipped++
			slog.Warn("saved launch configuration changed concurrently during key rotation, will retry on a later pass",
				slog.Int("saved_launch_config", row.ID))
		default:
			count.Skipped++
			slog.Warn("failed to rotate saved launch configuration answers, will retry on a later pass",
				slog.Int("saved_launch_config", row.ID), slog.String("error", err.Error()))
		}
	}

	return count, nil
}
