package credstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// ReconcileManaged brings the stored managed credential types in line with
// the ones this build ships.
//
// # Why a reconcile rather than a migration
//
// A migration runs once, and managed types change between releases: a later
// release adds a namespace, or corrects an injector template in one that
// already exists. A migration cannot express either, because by the time
// the new build runs, the migration that would have installed the type has
// already been recorded as applied. So this runs at every startup and is
// keyed on namespace, which is the stable identifier AWX gives every
// managed type and the one an import already matches on.
//
// It is idempotent by construction rather than by a guard: EnsureManagedType
// creates when the namespace is absent and updates when it is present, so a
// second run writes the same rows a first run did.
//
// # What it deliberately does not do
//
// It never deletes. A namespace this build no longer ships is left alone,
// because credentials may reference it and a credential whose type vanished
// cannot be injected. The operator's symptom would be a job failing at
// launch with no record of why the type went away, which is strictly worse
// than a stale type sitting unused in a list.
//
// It never touches a custom type. EnsureManagedType refuses a namespace held
// by one, and that refusal is reported here rather than swallowed: an
// operator who created a custom type called "aws" before upgrading needs to
// know that this build now ships one, and needs their own type left exactly
// as it was while they decide.
//
// # Why a partial failure does not stop the controller
//
// One type failing to reconcile is not a reason to refuse to start. The
// deployment still runs every template that does not use that type, and a
// controller that will not boot because a managed credential type is
// unhappy converts a small problem into a total outage. Every failure is
// logged with the namespace, and the error returned names how many failed,
// so a caller that wants to be strict can be.
func ReconcileManaged(ctx context.Context, store Store, types []credtype.CredentialType, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	var failed []string
	for _, ct := range types {
		if _, err := store.EnsureManagedType(ctx, ct); err != nil {
			failed = append(failed, ct.Namespace)

			// A namespace held by a custom type is the one failure an
			// operator has to act on, so it is logged as its own case
			// rather than as a generic reconcile error.
			if errors.Is(err, ErrExists) {
				logger.WarnContext(ctx, "a managed credential type could not be installed because a custom type holds its namespace",
					"namespace", ct.Namespace,
					"name", ct.Name,
					"action", "rename the custom credential type to free the namespace",
					"error", err)
				continue
			}
			logger.ErrorContext(ctx, "failed to reconcile a managed credential type",
				"namespace", ct.Namespace, "name", ct.Name, "error", err)
			continue
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("credstore: %d of %d managed credential types could not be reconciled: %v",
			len(failed), len(types), failed)
	}
	logger.InfoContext(ctx, "managed credential types reconciled", "count", len(types))
	return nil
}
