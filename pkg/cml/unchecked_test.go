// Tests of Unchecked: what a check answers for a lab, node or interface
// that is not there yet.
package cml_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestUnchecked: in a check, a missing lab makes the call unchecked; a
// real run keeps its error, and so does a check for any other error.
func TestUnchecked(t *testing.T) {
	missing := fmt.Errorf("no lab has the id or title %q: %w", "later", cml.ErrNotFound)
	var unchecked *collection.CannotCheckError
	if err := cml.Unchecked(collection.ModeCheck, missing); !errors.As(err, &unchecked) {
		t.Errorf("a check of a missing lab: %v", err)
	}
	if err := cml.Unchecked(collection.ModeExecute, missing); err != missing {
		t.Errorf("a run of a missing lab: %v", err)
	}
	other := errors.New("the controller answered 500")
	if err := cml.Unchecked(collection.ModeCheck, other); err != other {
		t.Errorf("a check that failed otherwise: %v", err)
	}
}
