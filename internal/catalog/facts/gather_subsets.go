// Package facts: facts.gather's subsets (Phase 41c), Ansible's gather_subset.
// Each probe belongs to one subset; a task names the subsets it wants, and
// only their probes run. min and hardware are what this method always read;
// packages, services, ports and users are each a family of one fact per item,
// and files is one fact per path the task names in files.
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
	gatherSubsetFiles    = "files"
	gatherSubsetPorts    = "ports"
	gatherSubsetUsers    = "users"
	gatherSubsetAll      = "all"
)

// gatherSubsets is every subset, in the order a run reads them.
var gatherSubsets = []string{gatherSubsetMin, gatherSubsetHardware, gatherSubsetPackages, gatherSubsetServices,
	gatherSubsetFiles, gatherSubsetPorts, gatherSubsetUsers}

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
//
// files is the one subset that reads what the task names: paths in files add
// it, a task naming files with no paths is refused, and all gathers it only
// when there are paths to read.
func gatherSelection(params map[string]any) (map[string]bool, error) {
	names, set, err := sdk.StringSlice(params, gatherParamSubset)
	if err != nil {
		return nil, err
	}
	paths, _, err := sdk.StringSlice(params, gatherParamFiles)
	if err != nil {
		return nil, err
	}
	chosen, err := gatherNamed(names, set)
	if err != nil {
		return nil, err
	}
	switch named, removed := gatherNamesFiles(names); {
	case len(paths) > 0 && removed:
		return nil, fmt.Errorf("%s names paths and %s leaves %s out; drop one of them", gatherParamFiles, gatherParamSubset, gatherSubsetFiles)
	case len(paths) > 0:
		chosen[gatherSubsetFiles] = true
	case named:
		return nil, fmt.Errorf("%s %s reads the paths %s names, and it names none", gatherParamSubset, gatherSubsetFiles, gatherParamFiles)
	default:
		// all chose it, with nothing to read.
		delete(chosen, gatherSubsetFiles)
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("%s %q leaves no subset to gather", gatherParamSubset, names)
	}
	return chosen, nil
}

// gatherExplicit is the subsets a task names itself, not through all or the
// default: those a task fails over when no source on the device answers for
// them (familyVerdict). An exclusion does not take a name back, as it does
// not in gatherNamed. A malformed value reads as naming none;
// gatherSelection refuses it first.
func gatherExplicit(params map[string]any) map[string]bool {
	names, _, _ := sdk.StringSlice(params, gatherParamSubset)
	explicit := map[string]bool{}
	for _, raw := range names {
		if name, remove := strings.CutPrefix(strings.TrimSpace(raw), "!"); !remove && name != gatherSubsetAll {
			explicit[name] = true
		}
	}
	return explicit
}

// gatherNamesFiles reports whether names asks for the files subset by name,
// and whether it leaves it out by name (!files).
func gatherNamesFiles(names []string) (named, removed bool) {
	for _, raw := range names {
		name, remove := strings.CutPrefix(strings.TrimSpace(raw), "!")
		if name == gatherSubsetFiles {
			named, removed = named || !remove, removed || remove
		}
	}
	return named, removed
}

// gatherNamed is the subsets names chooses, before files is settled, by
// Ansible's own rule (get_collector_names), whatever order they come in: all
// adds every subset, a name adds itself, !name and !all (every subset but
// min) exclude, and a subset is gathered when it was added and not excluded,
// or named itself, which an exclusion does not take back. So [!packages, all]
// leaves packages out, where reading the list in order once gathered them
// (sweep item 16). A list of exclusions alone starts from the default, and
// min is gathered unless !min is named.
func gatherNamed(names []string, set bool) (map[string]bool, error) {
	chosen := map[string]bool{}
	if !set || len(names) == 0 {
		for _, s := range gatherDefaultSubsets {
			chosen[s] = true
		}
		return chosen, nil
	}
	named, excluded := map[string]bool{}, map[string]bool{}
	added := false
	for _, raw := range names {
		name, remove := strings.CutPrefix(strings.TrimSpace(raw), "!")
		if name != gatherSubsetAll && !knownSubset(name) {
			return nil, fmt.Errorf("%s %q is not a subset this method gathers (%s, %s)", gatherParamSubset, raw, strings.Join(gatherSubsets, ", "), gatherSubsetAll)
		}
		switch {
		case name == gatherSubsetAll && remove:
			for _, s := range gatherSubsets {
				if s != gatherSubsetMin {
					excluded[s] = true
				}
			}
		case name == gatherSubsetAll:
			added = true
			for _, s := range gatherSubsets {
				chosen[s] = true
			}
		case remove:
			excluded[name] = true
		default:
			added = true
			named[name] = true
			chosen[name] = true
		}
	}
	if !added {
		for _, s := range gatherDefaultSubsets {
			chosen[s] = true
		}
	}
	if !excluded[gatherSubsetMin] || named[gatherSubsetMin] {
		chosen[gatherSubsetMin] = true
	}
	for name := range excluded {
		if !named[name] {
			delete(chosen, name)
		}
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
	gatherFamilyFile    = "file."
	gatherFamilyPort    = "port."
	gatherFamilyUser    = "user."
	gatherFamilyGroup   = "group."
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
