// Python re semantics over Go's regexp: re.match anchors at the start,
// re.search does not, and a group that took no part in a match is None.
package iosparse

import "regexp"

// rx is one compiled genieparser expression. RE2 chooses the same match a
// backtracking engine does for the leftmost-first expressions these
// parsers use, so the only rewrites needed are for lookaround, which RE2
// does not support; each rewrite is noted where it is made and proven by
// the parser's fixtures.
type rx struct {
	// any matches anywhere, for re.search.
	any *regexp.Regexp
	// start matches only at the start of the text, for re.match.
	start *regexp.Regexp
}

// re compiles a Python expression. It panics on an expression RE2 cannot
// compile, which can only be a defect in this package and is caught by any
// test that loads it.
func re(pattern string) *rx {
	return &rx{
		any:   regexp.MustCompile(pattern),
		start: regexp.MustCompile(`^(?:` + pattern + `)`),
	}
}

// match is Python's pattern.match(s): a match beginning at the start of s,
// or nil.
func (r *rx) match(s string) *mo {
	return newMo(r.start, s)
}

// search is Python's pattern.search(s): the leftmost match in s, or nil.
func (r *rx) search(s string) *mo {
	return newMo(r.any, s)
}

// mo is a Python match object.
type mo struct {
	// re is the expression that matched, for its group names.
	re *regexp.Regexp
	// s is the text matched against.
	s string
	// loc holds each group's start and end, -1 for a group that took no
	// part in the match.
	loc []int
}

// newMo runs re against s and wraps the result, or returns nil.
func newMo(re *regexp.Regexp, s string) *mo {
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return nil
	}
	return &mo{re: re, s: s, loc: loc}
}

// index returns the number of the named group, raising KeyError for a
// name the expression does not have, as m.groupdict()[name] does.
func (m *mo) index(name string) int {
	i := m.re.SubexpIndex(name)
	if i < 0 {
		raise("KeyError", "%q", name)
	}
	return i
}

// has reports whether the named group took part in the match, Python's
// m.groupdict()[name] is not None.
func (m *mo) has(name string) bool {
	return m.loc[2*m.index(name)] >= 0
}

// g is m.groupdict()[name] used as a string: the text the group matched,
// or "" when it took no part (which Python holds as None; callers that
// need the difference use has or v).
func (m *mo) g(name string) string {
	return m.n(m.index(name))
}

// v is m.groupdict()[name] as a value: the matched string, or nil for
// None.
func (m *mo) v(name string) any {
	if !m.has(name) {
		return nil
	}
	return m.g(name)
}

// opt is m.groupdict().get(name): "" and false when the expression has no
// such group or the group took no part, never raising.
func (m *mo) opt(name string) (string, bool) {
	i := m.re.SubexpIndex(name)
	if i < 0 || m.loc[2*i] < 0 {
		return "", false
	}
	return m.n(i), true
}

// groupItem is one entry of m.groupdict().items().
type groupItem struct {
	// name is the group's name.
	name string
	// value is the matched text, or nil for a group that took no part.
	value any
}

// items is m.groupdict().items(): every named group in the order the
// expression names them.
func (m *mo) items() []groupItem {
	var out []groupItem
	for i, name := range m.re.SubexpNames() {
		if name == "" {
			continue
		}
		var v any
		if m.loc[2*i] >= 0 {
			v = m.n(i)
		}
		out = append(out, groupItem{name: name, value: v})
	}
	return out
}

// updateTruthy is d.update({k: v for k, v in m.groupdict().items() if v}).
func updateTruthy(d *dict, m *mo) {
	for _, it := range m.items() {
		if truthy(it.value) {
			d.set(it.name, it.value)
		}
	}
}

// n is m.group(i) for a numbered group, "" when it took no part.
func (m *mo) n(i int) string {
	if m.loc[2*i] < 0 {
		return ""
	}
	return m.s[m.loc[2*i]:m.loc[2*i+1]]
}
