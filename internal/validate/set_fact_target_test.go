// Tests that validate reads a set_fact's target as the task's target and
// never as a fact, through the real validate entry point.
package validate_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestSetFact_TargetIsNotAFact: target on a set_fact is the task's target, so a
// set_fact with nothing else sets no variable and is refused, and one with a
// fact beside it is not.
func TestSetFact_TargetIsNotAFact(t *testing.T) {
	report := validate.Validate(varsWorld(t, `id: r
hosts: web1
tasks:
  - set_fact:
      target: web1
`, nil, nil))
	found := messages(report, "varnames")
	if len(found) != 1 || !strings.Contains(found[0], "needs at least one variable") {
		t.Errorf("varnames findings %q, want the set_fact refused for setting nothing", found)
	}
	report = validate.Validate(varsWorld(t, `id: r
hosts: web1
tasks:
  - set_fact:
      target: web1
      x: one
`, nil, nil))
	if found := messages(report, "varnames"); len(found) != 0 {
		t.Errorf("varnames findings %q, want none", found)
	}
}
