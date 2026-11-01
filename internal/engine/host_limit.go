// Package engine: a run's limit, Ansible's --limit: which of the devices a
// run targets it may reach, by name, group and glob, with ! to exclude and &
// to intersect. One matcher for both tiers, so a CLI run and a Controller's
// job read the same pattern the same way.
package engine

import (
	"fmt"
	"path"
	"strings"
)

// Bounds on a limit, which arrives in a launch field: long enough for any
// real list of hosts, short enough that matching it costs little per device.
const (
	maxLimitLength = 4096
	maxLimitTerms  = 256
)

// limitTerm is one pattern of a limit.
type limitTerm struct {
	// glob is the pattern, matched against a device's name and each of its
	// groups (path.Match); "all" and "*" match every device.
	glob string
	// exclude (!) and intersect (&) are the term's prefix; neither is a plain
	// term, and the plain terms of a limit are a union.
	exclude, intersect bool
}

// matches reports whether the term names a device called name in groups.
func (t limitTerm) matches(name string, groups []string) bool {
	if t.glob == "all" || t.glob == "*" {
		return true
	}
	if ok, _ := path.Match(t.glob, name); ok {
		return true
	}
	for _, g := range groups {
		if ok, _ := path.Match(t.glob, g); ok {
			return true
		}
	}
	return false
}

// Limit is a parsed limit; the zero Limit admits every device.
type Limit struct {
	terms []limitTerm
}

// ParseLimit reads text, Ansible's --limit grammar: patterns separated by
// commas or colons, each a device name, a group, or a glob of either, "all"
// for every device, a leading ! to exclude what it matches and a leading & to
// keep only what it matches. A regular expression (~) and a file of names
// (@) are refused by name. Empty text is no limit.
func ParseLimit(text string) (Limit, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Limit{}, nil
	}
	if len(text) > maxLimitLength {
		return Limit{}, fmt.Errorf("limit: %d characters is more than the %d a limit may hold", len(text), maxLimitLength)
	}
	fields := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ':' })
	// Text made only of separators (",", or "$HOSTS," with HOSTS empty)
	// leaves no pattern, and the zero Limit admits every device: a limit
	// someone wrote must never widen to all of them.
	if len(fields) == 0 {
		return Limit{}, fmt.Errorf("limit: %q has no pattern, only separators; name the devices or groups to run on", text)
	}
	if len(fields) > maxLimitTerms {
		return Limit{}, fmt.Errorf("limit: %d patterns is more than the %d a limit may hold", len(fields), maxLimitTerms)
	}
	var l Limit
	for _, field := range fields {
		raw := strings.TrimSpace(field)
		t := limitTerm{}
		switch {
		case strings.HasPrefix(raw, "!"):
			t.exclude, raw = true, raw[1:]
		case strings.HasPrefix(raw, "&"):
			t.intersect, raw = true, raw[1:]
		}
		raw = strings.TrimSpace(raw)
		switch {
		case raw == "":
			return Limit{}, fmt.Errorf("limit: %q has an empty pattern", text)
		case strings.HasPrefix(raw, "~"):
			return Limit{}, fmt.Errorf("limit: %q is a regular expression (~), which is not supported; use a glob such as web-*", raw)
		case strings.HasPrefix(raw, "@"):
			return Limit{}, fmt.Errorf("limit: %q reads names from a file (@), which is not supported; list the names", raw)
		case IsTemplate(raw):
			return Limit{}, fmt.Errorf("limit: %q is a template; a limit says which devices run, so it is written literally", raw)
		}
		if _, err := path.Match(raw, ""); err != nil {
			return Limit{}, fmt.Errorf("limit: %q is not a valid glob: %w", raw, err)
		}
		t.glob = raw
		l.terms = append(l.terms, t)
	}
	return l, nil
}

// IsZero reports whether l admits every device because it says nothing.
func (l Limit) IsZero() bool { return len(l.terms) == 0 }

// Admits reports whether a device called name in groups is inside l: in the
// union of its plain patterns (every device when it has none), in every &
// pattern, and in no ! pattern. The order patterns are written in does not
// matter, as in Ansible.
func (l Limit) Admits(name string, groups []string) bool {
	in, plain := false, false
	for _, t := range l.terms {
		if !t.exclude && !t.intersect {
			plain = true
			in = in || t.matches(name, groups)
		}
	}
	if !plain {
		in = true
	}
	for _, t := range l.terms {
		switch {
		case t.intersect && !t.matches(name, groups):
			return false
		case t.exclude && t.matches(name, groups):
			return false
		}
	}
	return in
}
