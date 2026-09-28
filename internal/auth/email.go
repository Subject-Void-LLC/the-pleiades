// The one rule that turns a submitted email address into an account key.
//
// An address is the join key between a local credential, a User row and a
// token's subject, so every place that accepts one must reduce it to the
// same key or the same person becomes two identities, or a credential
// authenticates nobody. internal/access (creating a user) and
// internal/localauth (signing one in) each carried their own copy of this
// rule until 2026-09-27, identical by agreement only. They now both call
// NormalizeEmail, so there is one rule to change.
package auth

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// ErrInvalidEmail is returned when a submitted address cannot be an
// account's key. Callers wrap it in their own vocabulary: internal/access
// as bad input, internal/localauth as an account that does not exist.
var ErrInvalidEmail = errors.New("auth: not a usable email address")

// NormalizeEmail trims and lowercases a submitted address, refusing one
// that is empty, has no "@", or holds a character an address cannot hold.
//
// Lowercased because the address is the join key against a token's
// subject, and two rows differing only in case would be two identities for
// one person. The lowercasing is Unicode's, on purpose: an address written
// with a non-ASCII capital (U+00C4) and the same address in lower case are
// one account, and a lookalike that folds onto an existing address is
// refused by the unique index rather than becoming a second user. Unicode
// normalization (NFC) is NOT applied, so a precomposed accent and the same
// accent written as a combining mark are two different keys; neither can
// match the other.
//
// The refusal runs on the trimmed input BEFORE lowercasing, never after.
// strings.ToLower writes U+FFFD in place of every byte that is not UTF-8,
// so checking afterwards would repair a malformed address into a valid key,
// and any number of different malformed inputs into the SAME key, which
// could then match a stored address holding that character. What is
// refused is what termsafe.CheckLine refuses: invalid UTF-8, control
// characters (a NUL, which Postgres rejects as a query parameter, among
// them), text direction marks, newline and tab. An address is shown in the
// users list, the activity stream and the controller's terminal output, so
// it must be one line that displays as what it is.
//
// The error never contains the submitted address unquoted: CheckLine's
// names a byte offset, and the missing-@ case quotes a value that has
// already passed CheckLine.
func NormalizeEmail(email string) (string, error) {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return "", fmt.Errorf("%w: the address is empty", ErrInvalidEmail)
	}
	if err := termsafe.CheckLine(trimmed); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidEmail, err)
	}
	lowered := strings.ToLower(trimmed)
	if !strings.Contains(lowered, "@") {
		return "", fmt.Errorf("%w: %q has no @", ErrInvalidEmail, lowered)
	}
	return lowered, nil
}
