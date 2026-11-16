// Package yanglib: the rules every module entry and protocol capability a
// server sends is held to before it is kept, whichever protocol carried it.
package yanglib

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// yangIdentifier is RFC 7950 section 6.2's identifier grammar, which a module,
// feature or deviation name must match before it is kept.
var yangIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,254}$`)

// yangRevision is a revision date, RFC 7950 section 7.1.9.
var yangRevision = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// IsIdentifier reports whether s is a YANG identifier (RFC 7950 section
// 6.2), at most 255 bytes.
func IsIdentifier(s string) bool {
	return yangIdentifier.MatchString(s)
}

// IsRevision reports whether s is a revision date (RFC 7950 section 7.1.9),
// such as 2024-07-01.
func IsRevision(s string) bool {
	return yangRevision.MatchString(s)
}

// KeepModules is modules with every malformed entry refused (counted on d's
// Dropped and ModulesDropped),
// each feature and deviation list checked the same way, duplicates removed,
// sorted by name then revision. An entry is refused whole: its name must be
// a YANG identifier, its revision (when given) a date, its namespace present,
// printable and within MaxNamespaceBytes, and its lists within
// MaxListEntries; past MaxModules every further entry is refused.
func KeepModules(modules []Module, d *Discovery) []Module {
	seen := map[string]bool{}
	out := make([]Module, 0, len(modules))
	for _, m := range modules {
		if len(out) == MaxModules {
			d.dropModule(m.Name)
			continue
		}
		if !yangIdentifier.MatchString(m.Name) || (m.Revision != "" && !yangRevision.MatchString(m.Revision)) ||
			m.Namespace == "" || len(m.Namespace) > MaxNamespaceBytes || !Printable(m.Namespace) ||
			len(m.Features) > MaxListEntries || len(m.Deviations) > MaxListEntries {
			d.dropModule(m.Name + "@" + m.Revision + " " + m.Namespace)
			continue
		}
		m.Features, m.Deviations = Identifiers(m.Features), Identifiers(m.Deviations)
		// One module at one revision is kept once, however many module
		// sets list it.
		key := m.Name + "@" + m.Revision
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Revision < out[j].Revision
	})
	return out
}

// Identifiers is names with every one that is not a YANG identifier left
// out, sorted, without duplicates; nil when none is left.
func Identifiers(names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if yangIdentifier.MatchString(n) && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Printable reports whether s holds no control or invisible formatting
// character, so it can reach a log, a terminal or a fact store as it is.
func Printable(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}

// ValidID is id when it is a short printable token (a content-id or a
// module-set-id), else empty.
func ValidID(id string) string {
	if id == "" || len(id) > 256 || !Printable(id) || strings.ContainsAny(id, " \t") {
		return ""
	}
	return id
}

// KeepCapabilities is capabilities as a Discovery's Protocol keeps them:
// trimmed, without empty entries or duplicates, each refused whole (counted
// on d) when it is not printable or is longer than MaxNamespaceBytes, sorted.
func KeepCapabilities(capabilities []string, d *Discovery) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range capabilities {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		if !Printable(c) || len(c) > MaxNamespaceBytes {
			d.Drop(c)
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
