// Tests holding hardwareSubsetKeys (facts_filter.go) to facts.gather's own
// subsets, so a key moved between min and hardware in internal/catalog/facts
// cannot leave the converter's copy behind.
package playbook

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

func TestHardwareSubsetKeysAreTheCatalogsHardwareSubset(t *testing.T) {
	// Every native key a converted filter can name.
	var keys []string
	for _, name := range ansiblefacts.AnsibleNames() {
		keys = append(keys, ansiblefacts.FilterKeys(name)...)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	for _, key := range keys {
		// facts.gather plans before it reaches a device: a filter matching
		// none of the chosen subsets' facts is refused there, so with min
		// alone exactly the hardware subset's keys are.
		_, err := facts.Gather(context.Background(), nil, nil, map[string]any{"gather_subset": []any{"min"}, "filter": []any{key}})
		refused := err != nil && strings.Contains(err.Error(), "matches none")
		if want := slices.Contains(hardwareSubsetKeys, key); refused != want {
			t.Errorf("%s: facts.gather under min refuses it %v, hardwareSubsetKeys holds it %v (%v)", key, refused, want, err)
		}
	}
	for _, key := range hardwareSubsetKeys {
		if !slices.Contains(keys, key) {
			t.Errorf("hardwareSubsetKeys holds %s, which no mapped Ansible fact filters", key)
		}
	}
}
