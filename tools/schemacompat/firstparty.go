// Package main: the first-party lists' contract (Phase 136a).
//
// first-party.json is what the core reserves for first-party Collections: the
// groups, the method namespaces and the fact floor no external Collection may
// use, which groups' methods fill each fact prefix, and the routing table from
// each first-party method to its group. A name taken off the list frees it for
// anyone, a method moved to another group changes what a user is told to
// install, and a group losing a fact prefix strands a program that writes
// there. A name added to a list refuses a program the release before loaded.
// Each of those is a break: a minor release names it in a
// `changelog/*.breaking.md` fragment, and a patch release cannot make it. A
// first-party group gaining a fact prefix it may fill refuses nobody, so it is
// held only on a patch line. A new method routed to a group is not a break: a
// patch release may add a method.
package main

// compareFirstParty compares two first-party documents; patch says the tree
// is on a patch line, where the lists may not grow. It returns how many groups
// the base lists, so a document of another shape compares as nothing.
func (c *schemaComparer) compareFirstParty(o, n map[string]any, patch bool) int {
	oldGroups, newGroups := groupsByName(o["groups"]), groupsByName(n["groups"])
	for _, name := range sortedKeys(oldGroups) {
		at := "/groups/" + escape(name)
		ng, still := newGroups[name]
		switch {
		case !still:
			c.report("first-party-group-removed", at, both, input)
		case oldGroups[name]["core"] != ng["core"]:
			c.report("first-party-group-core-changed", at+"/core", both, input)
		}
	}
	c.compareLists(o["namespaces"], n["namespaces"], "/namespaces", "first-party-namespace", true)
	c.compareLists(o["fact_floor"], n["fact_floor"], "/fact_floor", "first-party-fact-prefix", true)
	for _, name := range sortedKeys(newGroups) {
		if _, was := oldGroups[name]; !was {
			c.report("first-party-group-added", "/groups/"+escape(name), both, input)
		}
	}
	oldOwners, _ := o["fact_family_owners"].(map[string]any)
	newOwners, _ := n["fact_family_owners"].(map[string]any)
	for _, prefix := range sortedKeys(oldOwners) {
		c.compareLists(oldOwners[prefix], newOwners[prefix], "/fact_family_owners/"+escape(prefix), "first-party-fact-owner", patch)
	}
	oldMethods, _ := o["methods"].(map[string]any)
	newMethods, _ := n["methods"].(map[string]any)
	for _, fqcn := range sortedKeys(oldMethods) {
		if g, still := newMethods[fqcn]; still && g != oldMethods[fqcn] {
			c.report("first-party-method-moved", "/methods/"+escape(fqcn), both, input)
		}
	}
	return len(oldGroups)
}

// compareLists reports each entry of the old list the new one lacks as
// rule+"-removed", and, when added says growth counts, each entry the new one
// adds as rule+"-added".
func (c *schemaComparer) compareLists(o, n any, at, rule string, added bool) {
	olds, news := stringSet(o), stringSet(n)
	for _, v := range sortedKeys(olds) {
		if !news[v] {
			c.report(rule+"-removed", at+"/"+escape(v), both, input)
		}
	}
	if added {
		for _, v := range sortedKeys(news) {
			if !olds[v] {
				c.report(rule+"-added", at+"/"+escape(v), both, input)
			}
		}
	}
}

// groupsByName keys a groups list by its name.
func groupsByName(v any) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := v.([]any)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				out[name] = m
			}
		}
	}
	return out
}
