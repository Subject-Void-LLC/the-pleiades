// Package validate_test: VarsRule on a narrowed WorldView (narrowing_test.go)
// reads a device's variables wherever the target names it, as validate does
// with no narrowing.
package validate_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
)

// TestNarrowedWorld_ADevicesVariablesCountWhereverTheTargetNames: a name only
// the hidden hub defines satisfies VarsRule on a run that reaches only the
// spoke, as it does without the narrowing; a name no device defines does not.
func TestNarrowedWorld_ADevicesVariablesCountWhereverTheTargetNames(t *testing.T) {
	for name, want := range map[string]bool{"lan_net": false, "nobody_has_it": true} {
		world := narrowedWorld(t, "id: v\nhosts: c8kv\ntasks:\n  - name: show\n    noop:\n      note: \"{% if vars.role == 'hub' %}{{ vars."+name+" }}{% endif %}\"\n")
		if refused := len(validate.VarsRule(world)) > 0; refused != want {
			t.Errorf("vars.%s: refused %v, want %v", name, refused, want)
		}
	}
}
