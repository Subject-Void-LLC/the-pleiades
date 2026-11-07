// Tests for the shipped routing table's reader (Phase 136a).
package firstparty_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/firstparty"
)

// TestHint_NamesTheCollectionThatHoldsAMethod: a first-party method's hint
// names its Collection, a core method's says the build is incomplete, and a
// name no first-party Collection provides gets no hint (a typo is not told to
// install anything).
func TestHint_NamesTheCollectionThatHoldsAMethod(t *testing.T) {
	if h := firstparty.Hint("container.image.pull"); !strings.Contains(h, "first-party container Collection") {
		t.Errorf("container.image.pull hint = %q", h)
	}
	if h := firstparty.Hint("virt.clab.lab.deploy"); !strings.Contains(h, "virt.clab Collection") {
		t.Errorf("virt.clab.lab.deploy hint = %q", h)
	}
	if h := firstparty.Hint("facts.gather"); !strings.Contains(h, "core method") {
		t.Errorf("facts.gather hint = %q", h)
	}
	for _, name := range []string{"container.image.pul", "acme.widget.make", ""} {
		if h := firstparty.Hint(name); h != "" {
			t.Errorf("%q got the hint %q, want none", name, h)
		}
	}
}
