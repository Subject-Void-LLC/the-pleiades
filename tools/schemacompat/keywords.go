// Package main: the comparer's rules, one keyword or keyword family each.
package main

import (
	"sort"
	"strconv"
)

// typeSet reads a schema's "type" as a set; an absent type is nil, which
// accepts every type.
func typeSet(m map[string]any) map[string]bool {
	switch t := m["type"].(type) {
	case string:
		return map[string]bool{t: true}
	case []any:
		out := map[string]bool{}
		for _, v := range t {
			if s, ok := v.(string); ok {
				out[s] = true
			}
		}
		return out
	}
	return nil
}

// covers reports whether set accepts every value of type t: an integer is a
// number, so a set holding number covers integer.
func covers(set map[string]bool, t string) bool {
	return set == nil || set[t] || (t == "integer" && set["number"])
}

// compareTypes reports a type set that lost a type (narrowed) or gained
// one (widened). Losing "null" is narrowing, and so is a schema with no
// type gaining one.
func (c *schemaComparer) compareTypes(om, nm map[string]any, ptr string, d direction) {
	ot, nt := typeSet(om), typeSet(nm)
	for _, t := range sortedKeys(ot) {
		if !covers(nt, t) {
			c.report("type-narrowed", ptr+"/type", narrows, d)
			break
		}
	}
	if ot != nil && nt == nil {
		c.report("type-widened", ptr+"/type", widens, d)
		return
	}
	if ot == nil && nt != nil {
		c.report("type-narrowed", ptr+"/type", narrows, d)
		return
	}
	for _, t := range sortedKeys(nt) {
		if !covers(ot, t) {
			c.report("type-widened", ptr+"/type", widens, d)
			break
		}
	}
}

// compareEnum reports enum values removed (narrowed) or added (widened). An
// enum appearing narrows, and one disappearing widens.
func (c *schemaComparer) compareEnum(om, nm map[string]any, ptr string, d direction) {
	oe, oHas := om["enum"].([]any)
	ne, nHas := nm["enum"].([]any)
	switch {
	case !oHas && !nHas:
		return
	case !oHas:
		c.report("enum-value-removed", ptr+"/enum", narrows, d)
		return
	case !nHas:
		c.report("enum-value-added", ptr+"/enum", widens, d)
		return
	}
	olds, news := valueSet(oe), valueSet(ne)
	for v := range olds {
		if !news[v] {
			c.report("enum-value-removed", ptr+"/enum", narrows, d)
			break
		}
	}
	for v := range news {
		if !olds[v] {
			c.report("enum-value-added", ptr+"/enum", widens, d)
			break
		}
	}
}

// valueSet renders each enum value canonically, as a set.
func valueSet(list []any) map[string]bool {
	out := map[string]bool{}
	for _, v := range list {
		out[canonical(v)] = true
	}
	return out
}

// lowerBounds and upperBounds are the numeric keywords whose rise or fall
// narrows a schema.
var (
	lowerBounds = []string{"minimum", "exclusiveMinimum", "minLength", "minItems", "minProperties"}
	upperBounds = []string{"maximum", "exclusiveMaximum", "maxLength", "maxItems", "maxProperties"}
)

// compareScalarKeywords holds bounds, const, pattern, format and default
// to their rules.
func (c *schemaComparer) compareScalarKeywords(om, nm map[string]any, ptr string, d direction) {
	for _, k := range lowerBounds {
		c.compareBound(om, nm, ptr, k, d, func(o, n float64) bool { return n > o })
	}
	for _, k := range upperBounds {
		c.compareBound(om, nm, ptr, k, d, func(o, n float64) bool { return n < o })
	}
	for _, k := range []string{"const", "pattern", "format"} {
		ov, oHas := om[k]
		nv, nHas := nm[k]
		switch {
		case oHas && nHas && canonical(ov) != canonical(nv):
			c.report(k+"-changed", ptr+"/"+k, both, d)
		case !oHas && nHas:
			c.report(k+"-added", ptr+"/"+k, narrows, d)
		case oHas && !nHas:
			c.report(k+"-removed", ptr+"/"+k, widens, d)
		}
	}
	// A changed default changes what an input means without failing any
	// document, so it breaks an input schema whichever way it moves.
	if d == input && canonical(om["default"]) != canonical(nm["default"]) {
		c.report("default-changed", ptr+"/default", both, d)
	}
}

// compareBound reports one bound tightened (narrowed), loosened or removed
// (widened); tighter reports whether the new value is the tighter one.
func (c *schemaComparer) compareBound(om, nm map[string]any, ptr, k string, d direction, tighter func(o, n float64) bool) {
	ov, oHas := om[k].(float64)
	nv, nHas := nm[k].(float64)
	switch {
	case !oHas && nHas:
		c.report("bound-tightened", ptr+"/"+k, narrows, d)
	case oHas && !nHas:
		c.report("bound-loosened", ptr+"/"+k, widens, d)
	case oHas && nHas && tighter(ov, nv):
		c.report("bound-tightened", ptr+"/"+k, narrows, d)
	case oHas && nHas && tighter(nv, ov):
		c.report("bound-loosened", ptr+"/"+k, widens, d)
	}
}

// stringSet reads a list of strings, such as "required", as a set.
func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, item := range list {
		if s, ok := item.(string); ok {
			out[s] = true
		}
	}
	return out
}

// closed reports whether a schema refuses properties it does not name.
func closed(m map[string]any) bool {
	b, ok := m["additionalProperties"].(bool)
	return ok && !b
}

// compareProperties walks the properties both sides name, and reports a
// property removed and a property's required-ness changed.
//
// A removed property breaks a reader, required or not, since a reader that
// used it gets nothing (the run report's own contract counts a field removed
// as a major change); it breaks a writer only when the new schema is closed,
// since an open schema still accepts the key (the platform then ignores it,
// which a fragment should still say). A property becoming required, or a new one
// arriving required, breaks a writer; one ceasing to be required breaks a
// reader.
func (c *schemaComparer) compareProperties(om, nm map[string]any, ptr string, d direction) {
	op, _ := om["properties"].(map[string]any)
	np, _ := nm["properties"].(map[string]any)
	oreq, nreq := stringSet(om["required"]), stringSet(nm["required"])
	names := map[string]bool{}
	for k := range op {
		names[k] = true
	}
	for k := range np {
		names[k] = true
	}
	for k := range oreq {
		names[k] = true
	}
	for k := range nreq {
		names[k] = true
	}
	for _, name := range sortedKeys(names) {
		at := ptr + "/properties/" + escape(name)
		_, inOld := op[name]
		_, inNew := np[name]
		switch {
		case inOld && !inNew:
			if d == output || closed(nm) {
				c.report("property-removed", at, both, d)
			}
			continue
		case inOld && inNew:
			c.compare(op[name], np[name], at, d)
		}
		if !oreq[name] && nreq[name] {
			c.report("required-added", at, narrows, d)
		}
		if oreq[name] && !nreq[name] {
			c.report("required-removed", at, widens, d)
		}
	}
}

// compareAdditional reports additionalProperties closed, or given a schema
// where any value was accepted (both narrow), and compares the schema each
// side gives it.
func (c *schemaComparer) compareAdditional(om, nm map[string]any, ptr string, d direction) {
	ov, oOK := om["additionalProperties"].(map[string]any)
	nv, nOK := nm["additionalProperties"].(map[string]any)
	switch {
	case !closed(om) && closed(nm):
		c.report("additional-properties-closed", ptr+"/additionalProperties", narrows, d)
	case oOK && nOK:
		c.compare(ov, nv, ptr+"/additionalProperties", d)
	case !oOK && nOK && !closed(om):
		c.report("additional-properties-narrowed", ptr+"/additionalProperties", narrows, d)
	}
}

// compareItems compares an array's item schema, an item schema appearing
// narrowing and one disappearing widening.
func (c *schemaComparer) compareItems(om, nm map[string]any, ptr string, d direction) {
	ov, oHas := om["items"]
	nv, nHas := nm["items"]
	switch {
	case oHas && nHas:
		c.compare(ov, nv, ptr+"/items", d)
	case !oHas && nHas:
		c.report("items-added", ptr+"/items", narrows, d)
	case oHas && !nHas:
		c.report("items-removed", ptr+"/items", widens, d)
	}
}

// compareBranches matches oneOf or anyOf branches by content, since branches
// have no names and a generator may reorder them. What is left unmatched is
// a branch removed (narrowed) or added (widened); when exactly one branch is
// left on each side, it is the same branch changed, and is compared.
func (c *schemaComparer) compareBranches(om, nm map[string]any, ptr string, d direction, kw string) {
	ob, oHas := om[kw].([]any)
	nb, nHas := nm[kw].([]any)
	switch {
	case !oHas && !nHas:
		return
	case !oHas:
		c.report(kw+"-added", ptr+"/"+kw, narrows, d)
		return
	case !nHas:
		c.report(kw+"-removed", ptr+"/"+kw, widens, d)
		return
	}
	newIdx := map[string][]int{}
	for i, b := range nb {
		newIdx[canonical(b)] = append(newIdx[canonical(b)], i)
	}
	matchedNew := map[int]bool{}
	var leftOld []int
	for i, b := range ob {
		key := canonical(b)
		if idx := newIdx[key]; len(idx) > 0 {
			matchedNew[idx[0]] = true
			newIdx[key] = idx[1:]
			continue
		}
		leftOld = append(leftOld, i)
	}
	var leftNew []int
	for i := range nb {
		if !matchedNew[i] {
			leftNew = append(leftNew, i)
		}
	}
	if len(leftOld) == 1 && len(leftNew) == 1 {
		c.compare(ob[leftOld[0]], nb[leftNew[0]], ptr+"/"+kw+"/"+strconv.Itoa(leftNew[0]), d)
		return
	}
	for _, i := range leftOld {
		c.report("branch-removed", ptr+"/"+kw+"/"+strconv.Itoa(i), narrows, d)
	}
	for _, i := range leftNew {
		c.report("branch-added", ptr+"/"+kw+"/"+strconv.Itoa(i), widens, d)
	}
}

// sortedKeys returns a set's keys in order, so findings come out the same
// on every run.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
