// Tests that hold the module tables to the registry they map onto: every
// target a real, implemented method, every parameter one it declares, and
// every code one the closed set documents.
package playbook

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// entryCalls returns every call e can make.
func entryCalls(e Entry) []*Call {
	var out []*Call
	if e.Default != nil {
		out = append(out, e.Default)
	}
	for _, s := range e.Selectors {
		for _, c := range s.Choices {
			if c.Call != nil {
				out = append(out, c.Call)
			}
		}
	}
	return out
}

// checkCode fails when code is not documented, or its outcome is not one
// of want.
func checkCode(t *testing.T, where string, code Code, want ...Outcome) {
	t.Helper()
	doc, ok := codes[code]
	switch {
	case !ok:
		t.Errorf("%s raises %q, which codes.go does not document", where, code)
	case !slices.Contains(want, doc.Outcome):
		t.Errorf("%s raises %s, a %s code, where it needs one of %v", where, code, doc.Outcome, want)
	}
}

// TestEntries_TargetsImplemented checks each table entry against the
// registry: its calls name implemented methods with a declared class, its
// fixed and mapped parameters are ones those methods declare, and its
// returns are fields every one of them reports.
func TestEntries_TargetsImplemented(t *testing.T) {
	for _, e := range Entries() {
		if e.Rating == RatingManual {
			checkCode(t, e.Module, e.Code, OutcomeBlocked)
			if e.Reason == "" || e.Default != nil || e.Selectors != nil || e.Args != nil {
				t.Errorf("%s: a manual entry needs a reason and no calls or arguments", e.Module)
			}
			continue
		}
		calls := entryCalls(e)
		if len(calls) == 0 {
			t.Errorf("%s maps to no call", e.Module)
		}
		for _, c := range calls {
			checkCall(t, e.Module, c)
		}
		checkArgs(t, e, calls)
		for field, native := range e.Returns {
			for _, c := range calls {
				if !returnsField(c.FQCN, native) {
					t.Errorf("%s: Ansible's %s maps to %s, which %s does not return", e.Module, field, native, c.FQCN)
				}
			}
		}
	}
}

// checkCall checks one call against the registry.
func checkCall(t *testing.T, module string, c *Call) {
	t.Helper()
	d, ok := collection.Lookup(c.FQCN)
	if !ok || d.Manifest.Status != collection.StatusImplemented {
		t.Errorf("%s maps to %s, which is not a registered, implemented method", module, c.FQCN)
		return
	}
	if c.Class == ClassUnclassified || c.Basis == "" {
		t.Errorf("%s: the call to %s declares no class, or no basis for it", module, c.FQCN)
	}
	types := paramTypes(c.FQCN)
	for name := range c.Fixed {
		if _, ok := types[name]; !ok {
			t.Errorf("%s: %s is given %s, which it does not declare", module, c.FQCN, name)
		}
	}
}

// checkArgs checks e's arguments and selectors against calls.
func checkArgs(t *testing.T, e Entry, calls []*Call) {
	t.Helper()
	byName := map[string]Arg{}
	for _, a := range e.Args {
		for _, name := range append([]string{a.Name}, a.Aliases...) {
			if _, dup := byName[name]; dup {
				t.Errorf("%s lists argument %s twice", e.Module, name)
			}
			byName[name] = a
		}
		switch a.Handling {
		case ArgMap, ArgUnroll:
			declared := slices.ContainsFunc(calls, func(c *Call) bool {
				_, ok := paramTypes(c.FQCN)[a.To]
				return ok
			})
			if !declared {
				t.Errorf("%s maps %s to %s, which none of its calls declares", e.Module, a.Name, a.To)
			}
		case ArgDrop:
			checkCode(t, e.Module+" "+a.Name, a.Code, OutcomeInfo, OutcomeReview)
		case ArgBlock:
			checkCode(t, e.Module+" "+a.Name, a.Code, OutcomeBlocked)
		case ArgSelector:
			if !slices.ContainsFunc(e.Selectors, func(s Selector) bool { return s.Arg == a.Name }) {
				t.Errorf("%s: %s is a selector argument no selector reads", e.Module, a.Name)
			}
		}
	}
	if e.FreeForm == FreeFormCommand && byName[e.RawArg].Name == "" {
		t.Errorf("%s: free-form text goes to %s, which is not an argument", e.Module, e.RawArg)
	}
	for _, s := range e.Selectors {
		if byName[s.Arg].Handling != ArgSelector {
			t.Errorf("%s: selector %s is not a selector argument", e.Module, s.Arg)
		}
		seen := map[string]bool{}
		boolValues := slices.ContainsFunc(s.Choices, func(c Choice) bool {
			return slices.Contains(c.Values, "true") || slices.Contains(c.Values, "false")
		})
		if boolValues != s.Bool {
			t.Errorf("%s: selector %s has Bool %v, but its values say %v", e.Module, s.Arg, s.Bool, boolValues)
		}
		for _, c := range s.Choices {
			for _, v := range c.Values {
				if seen[v] {
					t.Errorf("%s: %s=%s is in two choices", e.Module, s.Arg, v)
				}
				seen[v] = true
			}
			if c.Call == nil && c.Code != "" {
				checkCode(t, e.Module+" "+s.Arg, c.Code, OutcomeBlocked)
			}
			for _, name := range c.Ignores {
				if h := byName[name].Handling; byName[name].Name == "" || (h != ArgMap && h != ArgUnroll) {
					t.Errorf("%s: %s ignores %s, which is not a mapped argument", e.Module, s.Arg, name)
				}
			}
		}
	}
}

// returnsField reports whether fqcn's documentation lists field.
func returnsField(fqcn, field string) bool {
	d, ok := collection.Lookup(fqcn)
	if !ok {
		return false
	}
	return slices.ContainsFunc(d.Manifest.Doc.Returns, func(r collection.ReturnField) bool { return r.Name == field })
}

// TestEntries_AliasesUnique checks that no name is claimed by two entries
// and that none is a task keyword, which would make a keyword read as a
// module.
func TestEntries_AliasesUnique(t *testing.T) {
	owner := map[string]string{}
	for _, e := range Entries() {
		for _, name := range append([]string{e.Module}, e.Aliases...) {
			if prev, dup := owner[name]; dup {
				t.Errorf("%s is claimed by %s and %s", name, prev, e.Module)
			}
			owner[name] = e.Module
			if !isModuleKey(name) {
				t.Errorf("%s names %s, which is a task keyword", e.Module, name)
			}
		}
	}
}

// TestIgnoredByAll covers the rule that an argument is dropped as ignored
// only when every chosen value ignores it.
func TestIgnoredByAll(t *testing.T) {
	a := &Choice{Ignores: []string{"x", "y"}}
	b := &Choice{Ignores: []string{"y"}}
	if got := ignoredByAll(nil); len(got) != 0 {
		t.Errorf("no choice ignores %v", got)
	}
	if got := ignoredByAll([]*Choice{a, b}); len(got) != 1 || !got["y"] {
		t.Errorf("= %v, want only y", got)
	}
	if got := ignoredByAll([]*Choice{b, {}}); len(got) != 0 {
		t.Errorf("= %v, want nothing, since the second choice ignores nothing", got)
	}
}
