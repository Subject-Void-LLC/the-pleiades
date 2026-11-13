// Tests that a structured parameter's value is held to its declared
// entries, and that Register refuses entries no value could be held to.
package collection_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// acceptParam is net.batfish.check's accept_unrecognized as declared.
func acceptParam() collection.Param {
	return collection.Param{Name: "accept_unrecognized", Type: collection.ParamTypeListOfDict, Description: "x", Entries: []collection.Param{
		{Name: "text", Type: "string", Description: "x"},
		{Name: "feature", Type: "string", Description: "x"},
		{Name: "reason", Type: "string", Required: true, Description: "x"},
	}}
}

// TestEntryProblems_NamesEachProblemByPlace: FAILURE_PATTERNS 584's split
// entry (a third key, nil), a missing required key, a value of another
// type, a choice outside its set, and a nested list's own entries are each
// named by place; a templated value, a value of the wrong shape, a nil one
// and a parameter declaring no entries report nothing.
func TestEntryProblems_NamesEachProblemByPlace(t *testing.T) {
	split := []any{map[string]any{"text": "ip ospf authentication-key", "reason": "lab only", "not on a real network": nil}}
	if got := collection.EntryProblems(acceptParam(), split); len(got) != 1 || !strings.Contains(got[0], `accept_unrecognized[0] has a key "not on a real network", which an entry does not take (it takes text, feature, reason)`) {
		t.Errorf("the split entry: %q", got)
	}
	if got := collection.EntryProblems(acceptParam(), []any{map[string]any{"text": "bfd"}}); len(got) != 1 || !strings.Contains(got[0], "accept_unrecognized[0] has no reason") {
		t.Errorf("no reason: %q", got)
	}
	if got := collection.EntryProblems(acceptParam(), []any{map[string]any{"text": 7, "reason": "r"}}); len(got) != 1 || !strings.Contains(got[0], "accept_unrecognized[0].text is") {
		t.Errorf("a number for text: %q", got)
	}

	devices := collection.Param{Name: "devices", Type: collection.ParamTypeListOfDict, Description: "x", Entries: []collection.Param{
		{Name: "name", Type: "string", Required: true, Description: "x"},
		{Name: "format", Type: "string", Choices: []string{"frr", "cisco_ios"}, Description: "x"},
	}}
	if got := collection.EntryProblems(devices, []any{map[string]any{"name": "r1", "format": "junos"}, map[string]any{"name": "r2", "format": "frr"}}); len(got) != 1 || !strings.Contains(got[0], `devices[0].format is "junos", which is not one of frr, cisco_ios`) {
		t.Errorf("a choice outside its set: %q", got)
	}
	devices.Entries[1].ChoicesIgnoreCase = true
	if got := collection.EntryProblems(devices, []any{map[string]any{"name": "r1", "format": " FRR "}}); len(got) != 0 {
		t.Errorf("a choice matched ignoring case: %q", got)
	}

	scenarios := collection.Param{Name: "scenarios", Type: collection.ParamTypeListOfDict, Description: "x", Entries: []collection.Param{
		{Name: "name", Type: "string", Required: true, Description: "x"},
		{Name: "assertions", Type: collection.ParamTypeListOfDict, Description: "x", Entries: []collection.Param{{Name: "ospf_established", Type: "bool", Description: "x"}}},
		{Name: "where", Type: collection.ParamTypeDict, Description: "x", Entries: []collection.Param{{Name: "column", Type: "string", Description: "x"}}},
	}}
	nested := []any{map[string]any{"name": "s", "assertions": []any{map[string]any{"ospf_up": true}}, "where": map[string]any{"row": "x"}}}
	got := collection.EntryProblems(scenarios, nested)
	if len(got) != 2 || !strings.Contains(strings.Join(got, "\n"), `scenarios[0].assertions[0] has a key "ospf_up"`) || !strings.Contains(strings.Join(got, "\n"), `scenarios[0].where has a key "row"`) {
		t.Errorf("nested entries: %q", got)
	}

	for name, value := range map[string]any{
		"a templated value":  []any{map[string]any{"text": "{{ vars.line }}", "reason": "{{ vars.why }}"}},
		"a value not a list": "{{ vars.accepts }}",
		"a nil value":        nil,
		"a list of text":     []any{"x"},
	} {
		if got := collection.EntryProblems(acceptParam(), value); len(got) != 0 {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := collection.EntryProblems(collection.Param{Name: "p", Type: collection.ParamTypeListOfDict}, split); len(got) != 0 {
		t.Errorf("a parameter declaring no entries: %q", got)
	}
}

// TestRegister_RefusesEntriesNoValueCouldBeHeldTo: entries on a parameter
// that is not a list of maps or a map, an unnamed, repeated or unknown-typed
// field, a field both required and defaulted, and an alias among them, at
// any depth, are each refused; net.batfish.check's shape registers.
func TestRegister_RefusesEntriesNoValueCouldBeHeldTo(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	field := func(name, typ string) collection.Param {
		return collection.Param{Name: name, Type: typ, Description: "x"}
	}
	for want, p := range map[string]collection.Param{
		"only a \"list of dict\" or \"dict\" parameter has entries": {Name: "p", Type: "string", Description: "x", Entries: []collection.Param{field("a", "string")}},
		"an unnamed entry field":                                    {Name: "p", Type: collection.ParamTypeListOfDict, Description: "x", Entries: []collection.Param{field("", "string")}},
		"declares the entry field p.a twice":                        {Name: "p", Type: collection.ParamTypeListOfDict, Description: "x", Entries: []collection.Param{field("a", "string"), field("a", "int")}},
		"which is not a parameter type":                             {Name: "p", Type: collection.ParamTypeDict, Description: "x", Entries: []collection.Param{field("a", "text")}},
		"both required and defaulted":                               {Name: "p", Type: collection.ParamTypeDict, Description: "x", Entries: []collection.Param{{Name: "a", Type: "string", Required: true, Default: "x", Description: "x"}}},
		"an alias, a run setting":                                   {Name: "p", Type: collection.ParamTypeDict, Description: "x", Entries: []collection.Param{{Name: "a", Type: "string", AliasOf: "b", Description: "x"}}},
		"on p.a.b, of type \"string\"": {Name: "p", Type: collection.ParamTypeListOfDict, Description: "x", Entries: []collection.Param{
			{Name: "a", Type: collection.ParamTypeDict, Description: "x", Entries: []collection.Param{{Name: "b", Type: "string", Description: "x", Entries: []collection.Param{field("c", "string")}}}},
		}},
	} {
		d := collection.Descriptor{Name: "entrytest.method", Manifest: collection.Manifest{Status: collection.StatusDeclared, Doc: collection.Doc{Params: []collection.Param{p}}}}
		if err := collection.Register(d); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Register = %v", want, err)
		}
	}
	good := collection.Descriptor{Name: "entrytest.method", Manifest: collection.Manifest{Status: collection.StatusDeclared, Doc: collection.Doc{Params: []collection.Param{acceptParam()}}}}
	if err := collection.Register(good); err != nil {
		t.Errorf("accept_unrecognized's shape: %v", err)
	}
}
