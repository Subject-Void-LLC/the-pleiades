// Package playbook: finding a module's table entry by any name it is
// written under.
package playbook

import (
	"maps"
	"slices"
	"strings"
	"sync"
)

// allEntries is every module table, assembled once.
var allEntries = sync.OnceValue(func() []Entry {
	return slices.Concat(pkgModules, fileModules, execModules, miscModules, manualModules)
})

// moduleIndex maps every name an entry is written under to it.
var moduleIndex = sync.OnceValue(func() map[string]*Entry {
	entries := allEntries()
	index := make(map[string]*Entry, len(entries)*3)
	for i := range entries {
		e := &entries[i]
		for _, name := range append([]string{e.Module}, e.Aliases...) {
			index[name] = e
		}
		// Ansible resolves ansible.legacy.x to ansible.builtin.x's action.
		if rest, ok := strings.CutPrefix(e.Module, "ansible.builtin."); ok {
			index["ansible.legacy."+rest] = e
		}
	}
	return index
})

// Entries returns every module table entry, in table order, for the
// documentation generator and the check-mode cross-check. The slice is a
// copy; the entries' own slices and maps are shared and must not be
// changed.
func Entries() []Entry { return slices.Clone(allEntries()) }

// lookupModule finds name's entry. A short name is tried as
// ansible.builtin.name, then under each of collections, as Ansible
// searches them.
func lookupModule(name string, collections []string) (*Entry, bool) {
	index := moduleIndex()
	if e, ok := index[name]; ok {
		return e, true
	}
	if strings.Contains(name, ".") {
		return nil, false
	}
	for _, prefix := range append([]string{"ansible.builtin", "ansible.legacy"}, collections...) {
		if e, ok := index[prefix+"."+name]; ok {
			return e, true
		}
	}
	return nil, false
}

// isModuleKey reports whether key on a task names a module rather than a
// keyword: any key the keyword tables do not list, and no with_ loop.
func isModuleKey(key string) bool {
	if _, ok := taskKeywords[key]; ok {
		return false
	}
	return !strings.HasPrefix(key, "with_")
}

// Codes returns every finding code and its meaning, for the documentation
// generator and an editor extension. The map is a copy.
func Codes() map[Code]CodeDoc { return maps.Clone(codes) }
