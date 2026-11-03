// Package collection: every fact a set of methods declares, by key (Phase
// 41b), for a reader that holds a catalog rather than the registry, such as
// the Controller, which reads the generated module catalog it serves.
package collection

import "strings"

// FactIndex is the fact declarations of a set of methods, looked up by key.
// Register keeps two methods from declaring one key differently, so a key
// has one declaration whichever method emitted it.
type FactIndex struct {
	exact    map[string]FactSpec
	prefixes []FactSpec
}

// NewFactIndex indexes every fact the manifests declare.
func NewFactIndex(manifests map[string]Manifest) FactIndex {
	x := FactIndex{exact: map[string]FactSpec{}}
	for _, m := range manifests {
		for _, s := range m.Facts {
			if s.Prefix {
				x.prefixes = append(x.prefixes, s)
				continue
			}
			x.exact[s.Key] = s
		}
	}
	return x
}

// Lookup is key's declaration: an exact key first, then the longest prefix.
func (x FactIndex) Lookup(key string) (FactSpec, bool) {
	if s, ok := x.exact[key]; ok {
		return s, true
	}
	return FactFor(x.prefixes, key)
}

// Len is how many declarations the index holds.
func (x FactIndex) Len() int { return len(x.exact) + len(x.prefixes) }

// overlaps reports whether two declarations can name the same key: two equal
// keys, a key inside a family, or two families one of which holds the other.
func overlaps(a, b FactSpec) bool {
	switch {
	case !a.Prefix && !b.Prefix:
		return a.Key == b.Key
	case a.Prefix && !b.Prefix:
		return a.Matches(b.Key)
	case !a.Prefix && b.Prefix:
		return b.Matches(a.Key)
	}
	return strings.HasPrefix(a.Key, b.Key) || strings.HasPrefix(b.Key, a.Key)
}
