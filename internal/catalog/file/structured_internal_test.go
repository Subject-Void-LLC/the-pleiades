// The parts of file.json and file.yaml that need no device: reading a
// call's parameters, and each format's edit of a document.
package file

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/docedit"
)

// TestStructuredParse_Refuses: each way a call can be malformed is refused,
// naming the parameter.
func TestStructuredParse_Refuses(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"no path":               {map[string]any{"key": "a", "value": 1}, "path"},
		"a relative path":       {map[string]any{"path": "etc/x.json", "key": "a", "value": 1}, "absolute, clean"},
		"an unclean path":       {map[string]any{"path": "/etc/../x.json", "key": "a", "value": 1}, "absolute, clean"},
		"no key":                {map[string]any{"path": "/x.json", "value": 1}, "key"},
		"no value":              {map[string]any{"path": "/x.json", "key": "a"}, "value is required"},
		"a value with absent":   {map[string]any{"path": "/x.json", "key": "a", "value": 1, "state": "absent"}, "goes with state present"},
		"another state":         {map[string]any{"path": "/x.json", "key": "a", "state": "gone"}, "not present or absent"},
		"create that is a word": {map[string]any{"path": "/x.json", "key": "a", "value": 1, "create": "yes"}, "true or false"},
	} {
		if _, err := structuredParse(tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	req, err := structuredParse(map[string]any{"path": "/x.json", "key": "a.b", "state": "absent", "create": true})
	if err != nil || req.present || !req.create || docedit.PathText(req.keyPath) != "a.b" {
		t.Errorf("a removal = %+v, %v", req, err)
	}
}

// TestFormats_Edit: both formats create from nothing, set, leave a value
// already held, remove, leave an absent key absent, and refuse what they
// cannot do, with the value before and after as text.
func TestFormats_Edit(t *testing.T) {
	path := func(s string) []docedit.Segment {
		p, err := docedit.ParsePath(s)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, f := range map[string]structuredFormat{"json": jsonFormat{}, "yaml": yamlFormat{}} {
		start := map[string][]byte{"json": []byte("{\"a\": 1}\n"), "yaml": []byte("a: 1\n")}[name]
		out, before, after, changed, err := f.edit(nil, path("n"), 2, true)
		if err != nil || !changed || before != "" || after != "2" || !strings.Contains(string(out), "2") {
			t.Errorf("%s: create = %q %q %q %v %v", name, out, before, after, changed, err)
		}
		if _, before, after, changed, err := f.edit(start, path("a"), 1, true); err != nil || changed || before != "1" || after != "1" {
			t.Errorf("%s: the same value = %q %q %v %v", name, before, after, changed, err)
		}
		if out, before, after, changed, err := f.edit(start, path("a"), 5, true); err != nil || !changed || before != "1" || after != "5" || !strings.Contains(string(out), "5") {
			t.Errorf("%s: set = %q %q %q %v %v", name, out, before, after, changed, err)
		}
		if out, before, after, changed, err := f.edit(start, path("a"), nil, false); err != nil || !changed || before != "1" || after != "" || strings.Contains(string(out), "1") {
			t.Errorf("%s: remove = %q %q %q %v %v", name, out, before, after, changed, err)
		}
		if out, _, _, changed, err := f.edit(start, path("b"), nil, false); err != nil || changed || string(out) != string(start) {
			t.Errorf("%s: remove an absent key = %q %v %v", name, out, changed, err)
		}
		if _, _, _, _, err := f.edit([]byte("{[: no"), path("a"), 1, true); err == nil {
			t.Errorf("%s: a document that does not parse was edited", name)
		}
		if _, _, _, _, err := f.edit(start, path("a.b"), 1, true); err == nil {
			t.Errorf("%s: a key under a scalar was set", name)
		}
		if _, _, _, _, err := f.edit(start, path("a"), make(chan int), true); err == nil {
			t.Errorf("%s: a value no document can hold was set", name)
		}
	}
	// A YAML key reached through an alias is refused at the read.
	if _, _, _, _, err := (yamlFormat{}).edit([]byte("b: &x {c: 1}\nd: *x\n"), path("d.c"), 2, true); err == nil {
		t.Error("an alias on the path was edited")
	}
	if _, _, _, _, err := (yamlFormat{}).edit([]byte("b: &x {c: 1}\nd: *x\n"), path("b.c"), nil, false); err == nil {
		t.Error("a key under an aliased anchor was removed")
	}
}
