// Package playbook: the Adjust functions module tables name, for modules
// whose defaults cannot be written as fixed parameters.
package playbook

import (
	"fmt"
	"strings"
)

// noted is an Adjustment raising the one note why, or nothing when why is
// empty.
func noted(why string) Adjustment {
	if why == "" {
		return Adjustment{}
	}
	return Adjustment{Notes: []string{why}}
}

// firewalldTimes writes the permanent and immediate firewalld's Ansible
// module means. Ansible leaves permanent off and immediate off unless
// given, then turns immediate on whenever permanent is off; the native
// methods default both to on, so a task giving neither would otherwise
// also persist the rule.
func firewalldTimes(params map[string]any) Adjustment {
	permanent, _ := params["permanent"].(bool)
	immediate, _ := params["immediate"].(bool)
	params["permanent"] = permanent
	params["immediate"] = immediate || !permanent
	return Adjustment{}
}

// copyMode notes the mode a file created without one gets: 0600 here,
// the device's umask (usually 0644) under Ansible.
func copyMode(params map[string]any) Adjustment {
	if _, ok := params["mode"]; ok {
		return Adjustment{}
	}
	return noted("a file this task creates gets mode 0600, where Ansible leaves it to the device's umask (usually 0644); give mode to keep the old result")
}

// netconfAdjust writes Ansible's spellings for netconf_config as the
// native ones. lock's if-supported becomes if_supported (Ansible's
// default, always, is the call's fixed parameter). target, which arrives
// here renamed datastore because target is the engine's device selector,
// keeps candidate and running; Ansible's auto (candidate when the device
// offers one) has no native equivalent, so it is dropped for the native
// default, running, and the change is noted for review.
func netconfAdjust(params map[string]any) Adjustment {
	if params["lock"] == "if-supported" {
		params["lock"] = "if_supported"
	}
	if params["datastore"] == "auto" {
		delete(params, "datastore")
		return noted("target: auto edits the candidate datastore and commits when the device offers one; this task edits running instead, so write datastore: candidate to keep a staged, committed change")
	}
	return Adjustment{}
}

// setupSubset writes Ansible's gather_subset as the facts.gather subsets it
// means. Ansible collects the names it adds and the !names it removes apart,
// removals winning whatever their order, starts a list of removals alone
// from all, and keeps min unless !min; of its subsets only min and hardware
// have a facts.gather counterpart, and its all never meant packages or
// services (package_facts and service_facts are separate modules), so the
// call names min and hardware explicitly, each as Ansible would have
// gathered it. Any other name added (network, virtual, facter, ohai, a
// fine-grained collector) gathers nothing here, and the note says which.
func setupSubset(params map[string]any) string {
	raw, ok := params["gather_subset"]
	if !ok {
		return ""
	}
	added, removed, unmatched := parseSubset(raw)
	hardware := subsetHasHardware(added, removed)
	subsets := []any{"min"}
	if hardware {
		subsets = append(subsets, "hardware")
	}
	params["gather_subset"] = subsets
	switch {
	case !subsetHasMin(removed) && !hardware:
		return "gather_subset leaves only Ansible's base facts, which facts.gather does not have; it gathers min"
	case len(unmatched) > 0:
		return fmt.Sprintf("gather_subset's %s have no facts.gather counterpart and are left out; it gathers %v", strings.Join(unmatched, ", "), subsets)
	}
	return ""
}

// parseSubset reads Ansible's gather_subset, one name or a list of them, into
// the names it adds and the !names it removes, and the added names with no
// facts.gather counterpart, in order.
func parseSubset(raw any) (added, removed map[string]bool, unmatched []string) {
	var names []string
	switch v := raw.(type) {
	case string:
		// One name (all); a comma-joined list is refused before this.
		names = []string{v}
	case []any:
		for _, n := range v {
			names = append(names, fmt.Sprint(n))
		}
	}
	added, removed = map[string]bool{}, map[string]bool{}
	for _, n := range names {
		name, remove := strings.CutPrefix(strings.TrimSpace(n), "!")
		switch {
		case name == "":
			continue
		case !remove && name != "all" && name != "hardware" && name != "min":
			// Removing one (!facter) asks for nothing this gathers anyway.
			unmatched = append(unmatched, name)
		}
		if remove {
			removed[name] = true
		} else {
			added[name] = true
		}
	}
	return added, removed, unmatched
}

// subsetHasHardware reports whether Ansible gathers its hardware subset under
// a gather_subset that adds added and removes removed: it is in all, which a
// list of removals alone starts from, and a removal wins.
func subsetHasHardware(added, removed map[string]bool) bool {
	return (len(added) == 0 || added["all"] || added["hardware"]) && !removed["all"] && !removed["hardware"]
}

// subsetHasMin reports whether Ansible gathers its min subset: it keeps min
// unless the list removes it.
func subsetHasMin(removed map[string]bool) bool {
	return !removed["min"]
}
