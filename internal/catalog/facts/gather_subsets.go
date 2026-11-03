// Package facts: facts.gather's subsets (Phase 41c), Ansible's gather_subset.
// Each probe belongs to one subset; a task names the subsets it wants, and
// only their probes run. min and hardware are what this method always read;
// packages and services are new, each a family of one fact per item.
package facts

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// gatherParamSubset is ansible.builtin.setup's own name for the subsets to
// gather.
const gatherParamSubset = "gather_subset"

// The subsets, by the names a task gives them.
const (
	gatherSubsetMin      = "min"
	gatherSubsetHardware = "hardware"
	gatherSubsetPackages = "packages"
	gatherSubsetServices = "services"
	gatherSubsetAll      = "all"
)

// gatherSubsets is every subset, in the order a run reads them.
var gatherSubsets = []string{gatherSubsetMin, gatherSubsetHardware, gatherSubsetPackages, gatherSubsetServices}

// gatherDefaultSubsets is what a task naming no subset gathers: what this
// method always read, so a runbook written before subsets existed reads the
// same facts, and no task starts listing a host's thousands of packages
// without asking.
var gatherDefaultSubsets = []string{gatherSubsetMin, gatherSubsetHardware}

// gatherSelection reads gather_subset as Ansible does in all but one way:
// names add a subset, a name with a leading ! removes one, all adds every
// subset and !all removes every one but min, and min is kept unless a task
// removes it by name. A list of removals alone starts from the default (min
// and hardware), where Ansible's starts from all: packages and services are
// separate modules there (package_facts, service_facts), so its all never
// lists them, and here a task lists a host's packages only by naming them or
// all. An unknown name is refused, a typo being far likelier than a wish to
// gather nothing.
func gatherSelection(params map[string]any) (map[string]bool, error) {
	names, set, err := sdk.StringSlice(params, gatherParamSubset)
	if err != nil {
		return nil, err
	}
	chosen := map[string]bool{}
	if !set || len(names) == 0 {
		for _, s := range gatherDefaultSubsets {
			chosen[s] = true
		}
		return chosen, nil
	}
	onlyRemovals := true
	for _, n := range names {
		if !strings.HasPrefix(n, "!") {
			onlyRemovals = false
		}
	}
	if onlyRemovals {
		for _, s := range gatherDefaultSubsets {
			chosen[s] = true
		}
	}
	keepMin := true
	for _, raw := range names {
		name, remove := strings.CutPrefix(strings.TrimSpace(raw), "!")
		switch {
		case name == gatherSubsetAll && remove:
			for _, s := range gatherSubsets {
				delete(chosen, s)
			}
		case name == gatherSubsetAll:
			for _, s := range gatherSubsets {
				chosen[s] = true
			}
		case !knownSubset(name):
			return nil, fmt.Errorf("%s %q is not a subset this method gathers (%s, %s)", gatherParamSubset, raw, strings.Join(gatherSubsets, ", "), gatherSubsetAll)
		case remove:
			delete(chosen, name)
			if name == gatherSubsetMin {
				keepMin = false
			}
		default:
			chosen[name] = true
		}
	}
	if keepMin {
		chosen[gatherSubsetMin] = true
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("%s %q leaves no subset to gather", gatherParamSubset, names)
	}
	return chosen, nil
}

// knownSubset reports whether name is one of the subsets.
func knownSubset(name string) bool {
	for _, s := range gatherSubsets {
		if s == name {
			return true
		}
	}
	return false
}

// chosenNames is chosen as a sorted list, for a message.
func chosenNames(chosen map[string]bool) []string {
	out := make([]string, 0, len(chosen))
	for s := range chosen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// The families: facts of one key per item, under these prefixes.
const (
	gatherFamilyPackage = "package."
	gatherFamilyService = "service."
)

// itemKey is the fact key of one item of family: the family's prefix and the
// item's name, with the characters a fact key cannot hold (a space, a slash,
// anything unprintable) and the percent sign percent-encoded, byte by byte, so the key reads back
// to the item exactly and two names never meet in one key.
func itemKey(family, name string) string {
	var b strings.Builder
	b.WriteString(family)
	for _, r := range name {
		if r == '/' || r == '%' || unicode.IsSpace(r) || !unicode.IsPrint(r) {
			// Every byte of the rune, so one outside ASCII stays exact.
			for _, c := range []byte(string(r)) {
				fmt.Fprintf(&b, "%%%02X", c)
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
