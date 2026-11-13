// A structured parameter's entries: the keys each map in a list of maps (or
// one map) may hold, and the check of a value against them.
//
// A method taking a list of maps (net.batfish.check's devices,
// accept_unrecognized) used to declare only "list of dict", so validation
// could say the value was a list of maps and nothing about the maps. YAML's
// flow syntax splits an unquoted comma into a key of its own, so
// {text: x, reason: a, b} is three keys, and the run, not validate, was the
// first to say so (FAILURE_PATTERNS 584). Param.Entries describes the keys,
// and EntryProblems is the one check of a value against them, which
// validate's entries rule makes before the run.
package collection

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// EntryProblems returns what is wrong with value given to p, a parameter
// declaring Entries, one sentence each, naming the entry by its place
// (devices[1].src): a key no entry takes, a required key left out, a value
// of another type, or one outside its choices. An entry field declaring
// Entries of its own is checked the same way, so a list of maps inside a
// map is held too. A text value holding a template (IsTemplate) is left
// alone, since it is only known once it renders; a map's keys never render,
// so they are held even where its values do. A parameter without Entries, a
// value of the wrong shape (a type finding of its own) and a nil value have
// nothing to report.
func EntryProblems(p Param, value any) []string {
	if len(p.Entries) == 0 || value == nil {
		return nil
	}
	var problems []string
	switch p.Type {
	case ParamTypeListOfDict:
		items, ok := listOf(value)
		if !ok {
			return nil
		}
		for i, item := range items {
			if m, ok := item.(map[string]any); ok {
				problems = append(problems, mapProblems(fmt.Sprintf("%s[%d]", p.Name, i), p.Entries, m)...)
			}
		}
	case ParamTypeDict:
		if m, ok := value.(map[string]any); ok {
			problems = mapProblems(p.Name, p.Entries, m)
		}
	}
	return problems
}

// mapProblems holds one map, at place, to fields.
func mapProblems(place string, fields []Param, m map[string]any) []string {
	var problems []string
	byName := make(map[string]Param, len(fields))
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		byName[f.Name] = f
		names = append(names, f.Name)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, ok := byName[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s has a key %q, which an entry does not take (it takes %s); "+
				"in a YAML flow mapping an unquoted comma ends a value, so quote a value holding one", place, k, strings.Join(names, ", ")))
			continue
		}
		v := m[k]
		if text, ok := v.(string); v == nil || (ok && IsTemplate(text)) {
			continue
		}
		at := place + "." + k
		if fits, err := ParamFits(f.Type, v); err == nil && !fits {
			problems = append(problems, fmt.Sprintf("%s is %s, and an entry's %s is %s", at, ValueKind(v), k, f.Type))
			continue
		}
		if s, ok := v.(string); ok && len(f.Choices) > 0 && !choiceHolds(f, s) {
			problems = append(problems, fmt.Sprintf("%s is %q, which is not one of %s", at, s, strings.Join(f.Choices, ", ")))
		}
		if len(f.Entries) > 0 {
			f.Name = at
			problems = append(problems, EntryProblems(f, v)...)
		}
	}
	for _, f := range fields {
		if _, given := m[f.Name]; f.Required && !given {
			problems = append(problems, fmt.Sprintf("%s has no %s, which every entry needs", place, f.Name))
		}
	}
	return problems
}

// choiceHolds reports whether s is one of f's choices, as f matches them.
func choiceHolds(f Param, s string) bool {
	if f.ChoicesIgnoreCase {
		s = strings.ToLower(strings.TrimSpace(s))
		return slices.ContainsFunc(f.Choices, func(c string) bool { return strings.ToLower(c) == s })
	}
	return slices.Contains(f.Choices, s)
}

// checkEntries refuses entries a value could not be held to: entries on a
// parameter that is not a list of maps or a map, an entry field unnamed,
// named twice, of an unknown type, both required and defaulted, an alias
// or a run setting, at any depth.
func checkEntries(fqcn, place, typ string, fields []Param) error {
	if len(fields) == 0 {
		return nil
	}
	if typ != ParamTypeListOfDict && typ != ParamTypeDict {
		return fmt.Errorf("collection: %q declares entries on %s, of type %q; only a %q or %q parameter has entries", fqcn, place, typ, ParamTypeListOfDict, ParamTypeDict)
	}
	seen := map[string]bool{}
	for _, f := range fields {
		at := place + "." + f.Name
		switch {
		case f.Name == "":
			return fmt.Errorf("collection: %q declares an unnamed entry field in %s", fqcn, place)
		case seen[f.Name]:
			return fmt.Errorf("collection: %q declares the entry field %s twice", fqcn, at)
		case !KnownParamType(f.Type):
			return fmt.Errorf("collection: %q declares the entry field %s of type %q, which is not a parameter type (one of %s)", fqcn, at, f.Type, strings.Join(ParamTypes(), ", "))
		case f.Required && f.Default != "":
			return fmt.Errorf("collection: %q declares the entry field %s both required and defaulted", fqcn, at)
		case f.AliasOf != "" || f.RunSetting || f.SecretRefs:
			return fmt.Errorf("collection: %q's entry field %s is an alias, a run setting or a secret reference, which only a parameter can be", fqcn, at)
		}
		seen[f.Name] = true
		if err := checkEntries(fqcn, at, f.Type, f.Entries); err != nil {
			return err
		}
	}
	return nil
}
