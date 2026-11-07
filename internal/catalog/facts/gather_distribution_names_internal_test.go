// Package facts: the distributions facts.gather names and the mapping's.
package facts

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

// unmeasuredIDs are os-release IDs facts.gather knows the family of but that
// nobody has measured ansible.builtin.setup on, so the mapping names no
// ansible_distribution for them rather than guessing NAME.
var unmeasuredIDs = map[string]bool{"opensuse": true, "sled": true, "sles_sap": true}

// TestEveryKnownDistributionMapsToAnsiblesNameOrSaysItCannot holds the two
// tables to each other: an ID facts.gather knows (familyByID) either has
// Ansible's name in pkg/ansiblefacts, measured, or is listed here as
// unmeasured, so adding an ID to one table without the other fails.
func TestEveryKnownDistributionMapsToAnsiblesNameOrSaysItCannot(t *testing.T) {
	for id := range familyByID {
		known := ansiblefacts.HasDistributionName(id)
		switch {
		case !known && !unmeasuredIDs[id]:
			t.Errorf("%s: facts.gather knows its family and the mapping has no Ansible name for it; measure it, or list it as unmeasured", id)
		case known && unmeasuredIDs[id]:
			t.Errorf("%s: listed as unmeasured, and the mapping names it", id)
		}
	}
}
