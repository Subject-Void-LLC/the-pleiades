// Package render: the statements, operators and tests the engine
// supports and what it refuses, as data for pleiades doc filters.
package render

import "sort"

// The grammar as `pleiades doc filters` shows it, beside Filters: each
// statement, operator and test, how it is written and what it does. Kept
// here, beside the grammar, so the page cannot list a form the parser does
// not have.

// SyntaxDoc is one statement, operator or test.
type SyntaxDoc struct {
	// Name is the statement, operator or test.
	Name string `json:"name"`
	// Usage is how a template writes it.
	Usage string `json:"usage"`
	// Description says what it does.
	Description string `json:"description"`
}

// Statements returns every statement a template may use.
func Statements() []SyntaxDoc {
	return []SyntaxDoc{
		{"if", "{% if c %}...{% elif d %}...{% else %}...{% endif %}", "the first branch whose condition is true; false, none, 0 and empty text, lists and mappings are false"},
		{"for", "{% for x in list %}...{% else %}...{% endfor %}", "the body once per item, or the else when there is none; a mapping gives its keys in sorted order, text its characters; loop.index, loop.index0, loop.revindex, loop.revindex0, loop.first, loop.last and loop.length count; at most 10000 passes per render"},
		{"for pairs", "{% for k, v in m.items() %}...{% endfor %}", "two names take each item's two values, as .items() gives them"},
		{"set", "{% set n = x %}", "binds n from here on; inside a for loop only for that pass"},
		{"comment", "{# text #}", "writes nothing"},
		{"raw", "{% raw %}{{ x }}{% endraw %}", "writes what is inside as it is, delimiters included"},
		{"trim", "{%- x -%}", "a - inside a tag trims the whitespace beside it, in every kind of tag"},
		{"keep", "{%+ x +%}", "a + inside a statement or comment keeps the indentation lstrip_blocks would strip and the newline trim_blocks would remove; file.template turns trim_blocks on, as Ansible's template module does"},
	}
}

// Operators returns every operator, loosest first.
func Operators() []SyntaxDoc {
	return []SyntaxDoc{
		{"or", "a or b", "a when a is true, else b, which is read only then"},
		{"and", "a and b", "a when a is false, else b, which is read only then"},
		{"not", "not a", "true when a is false"},
		{"compare", "a == b, !=, <, <=, >, >=", "true or false; a number never equals text, and ordering two values that have no order (a number and text, two mappings, none) is an error; a < b < c is a < b and b < c"},
		{"in", "a in b, a not in b", "an item of a list, a key of a mapping, or text inside text"},
		{"concat", "a ~ b", "the two values' text joined; none, a list or a mapping is an error (use to_json)"},
		{"literal", "'text' \"text\" 42 0x2a 1.5 true false none [a, b] (a, b) {'k': v}", "constants; True, False and None are the same constants; a list or mapping literal holds at most 256 items"},
		{"method", "m.items(), m.keys(), m.values()", "a mapping's pairs, keys or values as a list, in sorted key order"},
		{"group", "(a or b) and c", "parentheses group; an expression nests at most 32 deep"},
	}
}

// Tests returns every test, written "x is name" or "x is not name".
func Tests() []SyntaxDoc {
	out := make([]SyntaxDoc, 0, len(tests))
	for name, t := range tests {
		out = append(out, SyntaxDoc{Name: name, Usage: t.usage, Description: t.doc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// NotSupported returns the Jinja2 forms this renderer refuses, each with
// why, so an author learns what to write instead.
func NotSupported() []ExcludedFilter {
	return []ExcludedFilter{
		{"arithmetic", "+ - * / // % ** are left out: * and ** turn a short template into unbounded text or numbers; compute in a task"},
		{"inline if", "a if c else b; use an if statement"},
		{"computed subscripts", "a[b] takes a literal key or index only, so what a template reads is known before it runs"},
		{"macro, include, import, extends, block, call, filter, with", "refused by name; a template is one file with no functions"},
	}
}
