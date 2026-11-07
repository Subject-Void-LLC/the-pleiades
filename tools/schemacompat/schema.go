// Package main: the JSON Schema comparer.
//
// A change to a schema either narrows the set of documents it accepts,
// widens it, or both. Which of those breaks someone depends on who writes
// the documents: a schema for something a user writes (a runbook, an API
// request) breaks when it narrows, since a document that was valid stops
// being valid; a schema for something the platform writes and a tool reads
// (the run report, an API response) breaks when it widens, since a reader
// meets a value it was never told about. This is Confluent Schema Registry's
// BACKWARD and FORWARD, and oasdiff's request and response checks, as one
// rule with a direction.
//
// Only breaking changes are reported. A rename is a removal plus an addition,
// as every tool of this kind treats it, and is reported as the removal.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// direction says who writes the documents a schema describes.
type direction int

const (
	// input is a document a user or client writes and the platform reads.
	input direction = iota
	// output is a document the platform writes and a user or tool reads.
	output
)

// String names the direction for the memo key and for messages.
func (d direction) String() string {
	if d == input {
		return "input"
	}
	return "output"
}

// effect is what one change does to the set of accepted documents.
type effect int

const (
	// narrows means some document the old schema accepted is now refused.
	narrows effect = iota
	// widens means some document the new schema accepts was refused before.
	widens
	// both means the change can do either, such as a changed pattern.
	both
)

// breaks reports whether a change with effect e breaks a schema of direction d.
func breaks(e effect, d direction) bool {
	return e == both || (e == narrows && d == input) || (e == widens && d == output)
}

// Finding is one breaking change: a rule id and the place it happened, as a
// file name and an RFC 6901 pointer. Together they are the token a
// `changelog/*.breaking.md` fragment names to accept it.
type Finding struct {
	// Rule is the kind of change, such as property-removed.
	Rule string `json:"rule"`
	// Path is the file and pointer, such as run-report.json#/properties/nodes.
	Path string `json:"path"`
}

// Token is how a changelog fragment names the finding.
func (f Finding) Token() string { return f.Rule + " " + f.Path }

// annotations are the keywords that never change which documents validate.
var annotations = map[string]bool{
	"title": true, "description": true, "examples": true, "$comment": true,
	"deprecated": true, "readOnly": true, "writeOnly": true, "$schema": true, "$id": true,
}

// schemaComparer compares one file's old and new schemas.
type schemaComparer struct {
	// file is the schema's file name, the prefix of every finding's path.
	file string
	// oldRoot and newRoot are the whole documents, for resolving $ref.
	oldRoot, newRoot any
	// seen holds the reference pairs already compared, so a schema that
	// refers to itself (a runbook task's block) is compared once.
	seen map[string]bool
	// findings collects what was found, in walk order.
	findings []Finding
}

// newSchemaComparer starts a comparison of one file.
func newSchemaComparer(file string, oldRoot, newRoot any) *schemaComparer {
	return &schemaComparer{file: file, oldRoot: oldRoot, newRoot: newRoot, seen: map[string]bool{}}
}

// report records a finding when the change breaks a schema of direction d.
func (c *schemaComparer) report(rule, ptr string, e effect, d direction) {
	if breaks(e, d) {
		c.findings = append(c.findings, Finding{Rule: rule, Path: c.file + "#" + ptr})
	}
}

// compare walks one schema pair at pointer ptr.
func (c *schemaComparer) compare(o, n any, ptr string, d direction) {
	om, oOK := o.(map[string]any)
	nm, nOK := n.(map[string]any)
	if !oOK || !nOK {
		// A boolean schema, or a schema replaced by one: compare whole.
		if canonical(o) != canonical(n) {
			c.report("schema-changed", ptr, both, d)
		}
		return
	}
	if c.followRefs(om, nm, ptr, d) {
		return
	}
	c.compareTypes(om, nm, ptr, d)
	c.compareEnum(om, nm, ptr, d)
	c.compareScalarKeywords(om, nm, ptr, d)
	c.compareProperties(om, nm, ptr, d)
	c.compareAdditional(om, nm, ptr, d)
	c.compareItems(om, nm, ptr, d)
	c.compareBranches(om, nm, ptr, d, "oneOf")
	c.compareBranches(om, nm, ptr, d, "anyOf")
	for _, k := range []string{"allOf", "not", "if", "then", "else", "patternProperties", "prefixItems"} {
		if canonical(om[k]) != canonical(nm[k]) {
			c.report("schema-changed", ptr+"/"+k, both, d)
		}
	}
}

// followRefs resolves a $ref on either side and compares what it names,
// reporting true when it did so and the caller should stop.
//
// Two sides naming the same definition are compared once per direction, at
// the definition's own pointer, so a change to a shared definition is
// reported once under one stable name. Sides naming different definitions
// (a renamed type) are compared by what they resolve to, at the place that
// refers to them, and a rename with no change in shape reports nothing.
func (c *schemaComparer) followRefs(om, nm map[string]any, ptr string, d direction) bool {
	oref, oHas := om["$ref"].(string)
	nref, nHas := nm["$ref"].(string)
	if !oHas && !nHas {
		return false
	}
	key := oref + "|" + nref + "|" + d.String()
	if oref != nref {
		key += "|" + ptr
	}
	if c.seen[key] {
		return true
	}
	c.seen[key] = true
	var o, n any = om, nm
	at := ptr
	if oHas {
		o = resolve(c.oldRoot, oref)
	}
	if nHas {
		n = resolve(c.newRoot, nref)
	}
	if oHas && nHas && oref == nref {
		at = strings.TrimPrefix(oref, "#")
	}
	c.compare(o, n, at, d)
	return true
}

// resolve follows a local reference ("#/$defs/task") in root, returning nil
// for one it cannot follow, which then compares as a changed schema.
func resolve(root any, ref string) any {
	if !strings.HasPrefix(ref, "#") {
		return nil
	}
	cur := root
	for _, tok := range strings.Split(strings.TrimPrefix(ref, "#"), "/")[1:] {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")]
	}
	return cur
}

// escape writes one pointer token per RFC 6901.
func escape(tok string) string {
	return strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
}

// canonical renders a value with annotations removed and map keys sorted
// (encoding/json sorts them), so two equal schemas render the same.
func canonical(v any) string {
	b, err := json.Marshal(strip(v))
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// strip removes annotation keywords at every depth, and orders the arrays
// that are sets (type, required, enum) so their order never counts.
func strip(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if annotations[k] {
				continue
			}
			if k == "type" || k == "required" || k == "enum" {
				out[k] = sortedSet(val)
				continue
			}
			out[k] = strip(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = strip(val)
		}
		return out
	}
	return v
}

// sortedSet renders a keyword that is a set as a sorted list of canonical
// strings, or returns a single value unchanged.
func sortedSet(v any) any {
	list, ok := v.([]any)
	if !ok {
		return v
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, canonical(item))
	}
	sort.Strings(out)
	return out
}
