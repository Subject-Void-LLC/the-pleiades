// Package main: the module catalog's contract.
//
// module-catalog.json is not a JSON Schema but the generated record of
// every method: its parameters (what a runbook writes), its returns (what a
// runbook's register and conditions read), the facts it records (what
// targets, baselines and conditions read), and what a device must have for
// it to run. Each of those is a contract a runbook depends on, so each is
// held here: a method, parameter, return or fact gone, a parameter newly
// required or retyped or with a new default or fewer choices, a return or
// fact retyped, and a method needing a capability or losing a transport.
package main

// compareCatalog compares two module catalogs.
func (c *schemaComparer) compareCatalog(o, n map[string]any) {
	for _, fqcn := range sortedKeys(o) {
		at := "/" + escape(fqcn)
		om, _ := o[fqcn].(map[string]any)
		if om["status"] != "implemented" {
			// A method declared but never implemented has nobody depending
			// on it yet.
			continue
		}
		nm, ok := n[fqcn].(map[string]any)
		if !ok || nm["status"] != "implemented" {
			c.report("method-removed", at, both, input)
			continue
		}
		c.compareParams(docList(om, "params"), docList(nm, "params"), at+"/params")
		c.compareNamed(docList(om, "returns"), docList(nm, "returns"), at+"/returns", "return")
		c.compareNamed(keyedList(om["facts"]), keyedList(nm["facts"]), at+"/facts", "fact")
		for k := range stringSet(nm["requiredCapabilities"]) {
			if !stringSet(om["requiredCapabilities"])[k] {
				c.report("capability-required", at+"/requiredCapabilities/"+escape(k), both, input)
			}
		}
		for k := range stringSet(om["supportedTransports"]) {
			if !stringSet(nm["supportedTransports"])[k] {
				c.report("transport-removed", at+"/supportedTransports/"+escape(k), both, input)
			}
		}
	}
}

// docList keys a method's doc list ("params" or "returns") by name.
func docList(method map[string]any, field string) map[string]map[string]any {
	doc, _ := method["doc"].(map[string]any)
	return named(doc[field], "name")
}

// keyedList keys a method's facts by key, or by prefix for an item family.
func keyedList(v any) map[string]map[string]any {
	out := named(v, "key")
	for k, m := range named(v, "prefix") {
		out[k+"*"] = m
	}
	return out
}

// named keys a list of objects by one of their string fields, skipping an
// object without it.
func named(v any, field string) map[string]map[string]any {
	out := map[string]map[string]any{}
	list, _ := v.([]any)
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := m[field].(string); ok && name != "" {
			out[name] = m
		}
	}
	return out
}

// compareParams holds a method's parameters, which a runbook writes.
func (c *schemaComparer) compareParams(o, n map[string]map[string]any, at string) {
	for _, name := range sortedKeys(n) {
		pAt := at + "/" + escape(name)
		np := n[name]
		op, had := o[name]
		if !had {
			if truthy(np["required"]) {
				c.report("param-added-required", pAt, both, input)
			}
			continue
		}
		if !truthy(op["required"]) && truthy(np["required"]) {
			c.report("param-became-required", pAt, both, input)
		}
		if canonical(op["type"]) != canonical(np["type"]) {
			c.report("param-type-changed", pAt+"/type", both, input)
		}
		if canonical(op["default"]) != canonical(np["default"]) {
			c.report("param-default-changed", pAt+"/default", both, input)
		}
		if oc, ok := op["choices"].([]any); ok {
			nc, has := np["choices"].([]any)
			if !has {
				continue
			}
			newChoices := valueSet(nc)
			for v := range valueSet(oc) {
				if !newChoices[v] {
					c.report("param-choice-removed", pAt+"/choices", both, input)
					break
				}
			}
		} else if _, now := np["choices"]; now {
			c.report("param-choices-added", pAt+"/choices", both, input)
		}
	}
	for _, name := range sortedKeys(o) {
		if _, still := n[name]; !still {
			c.report("param-removed", at+"/"+escape(name), both, input)
		}
	}
}

// compareNamed holds a method's returns or facts, which a runbook reads: one
// gone, or one whose type or kind changed.
func (c *schemaComparer) compareNamed(o, n map[string]map[string]any, at, what string) {
	for _, name := range sortedKeys(o) {
		nAt := at + "/" + escape(name)
		nv, ok := n[name]
		if !ok {
			c.report(what+"-removed", nAt, both, output)
			continue
		}
		for _, k := range []string{"type", "kind"} {
			if canonical(o[name][k]) != canonical(nv[k]) {
				c.report(what+"-"+k+"-changed", nAt+"/"+k, both, output)
			}
		}
	}
}
