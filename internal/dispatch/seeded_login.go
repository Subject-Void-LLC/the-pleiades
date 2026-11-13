// Package dispatch: the Controller's refusal of a runbook that would seed a
// login, made before anything is published.
//
// Decided 2026-11-11: a login seed reaches a Runner only through the
// claim-time fetch of the credential (Phase 105), with no interim path, and
// the refusal sits in the Controller before publish rather than in the
// Runner, whose refusal would come after the dispatch crossed the stream.
// The launch refuses such a template (internal/api's LaunchTemplate), and
// fan-out refuses such a job whatever started it (runbookDefinitionSource),
// so no path reaches the stream.
package dispatch

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/runbook"
)

// ErrSeededLogin marks a runbook refused on the Walk tier because one of its
// tasks would seed a device's login into a machine it creates.
var ErrSeededLogin = errors.New("a task seeds a device's login, which only the pleiades CLI resolves until a Runner can fetch the credential itself")

// RefuseSeededLogins returns an error wrapping ErrSeededLogin that names
// every task of rb that would seed a login, or nil when none would.
func RefuseSeededLogins(rb *runbook.Runbook) error {
	if rb == nil || len(rb.SeededLogins) == 0 {
		return nil
	}
	return fmt.Errorf("runbook %q refused before dispatch: %w: %s; run it with the pleiades CLI", rb.ID, ErrSeededLogin, strings.Join(rb.SeededLogins, "; "))
}
