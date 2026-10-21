// Package lock: who holds an item, recorded with each hold and named when
// another caller contends for it (Phase 103a).
package lock

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ownerPattern is what AcquireOptions.Owner must look like. An owner is a
// value other processes read back from the lock record, so it is held to one
// alphabet rather than trusted: the Runner's is a topology.DispatchKey, a
// single subject token.
var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

// validateOwner refuses an owner outside ownerPattern. Empty is allowed and
// means the hold names no owner.
func validateOwner(owner string) error {
	if owner != "" && !ownerPattern.MatchString(owner) {
		return fmt.Errorf("lock owner %q is not 1 to 256 of [A-Za-z0-9_-]", owner)
	}
	return nil
}

// HeldError is ErrLockHeld naming the item's current holders: each one's
// AcquireOptions.Owner, or "" for a hold that named none (one taken by a
// Runner from before owners were recorded). errors.Is(err, ErrLockHeld)
// holds for it, so every caller that only asks whether the item is held is
// unchanged.
//
// The owners come from the record read in the attempt that found the item
// held. A hold can end a moment later, so they say who held it then, which
// is enough to tell a duplicate of one's own work from another's.
type HeldError struct {
	Owners []string
}

// Error names the holders after ErrLockHeld's own text.
func (e *HeldError) Error() string {
	names := make([]string, 0, len(e.Owners))
	for _, o := range e.Owners {
		if o == "" {
			o = "a holder that named no owner"
		}
		names = append(names, o)
	}
	if len(names) == 0 {
		return ErrLockHeld.Error()
	}
	return fmt.Sprintf("%v by %s", ErrLockHeld, strings.Join(names, ", "))
}

// Unwrap makes errors.Is(err, ErrLockHeld) hold.
func (e *HeldError) Unwrap() error { return ErrLockHeld }

// HeldBy reports whether owner is among the holders. An owner-less hold
// never matches, so a hold from an older Runner always reads as another's.
func (e *HeldError) HeldBy(owner string) bool {
	return owner != "" && slices.Contains(e.Owners, owner)
}
