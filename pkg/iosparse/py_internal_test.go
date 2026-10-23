// Package iosparse: tests of the helpers that reproduce Python's behavior
// the ported parsers rely on.
package iosparse

import (
	"reflect"
	"strings"
	"testing"
)

// raises runs fn and returns the Python exception kind it raised, or "".
func raises(fn func()) (kind string) {
	defer func() {
		if r := recover(); r != nil {
			kind = r.(*pyErr).kind
		}
	}()
	fn()
	return ""
}

// TestPythonSemantics holds the helpers to the Python behavior each one
// names.
func TestPythonSemantics(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"a\nb", []string{"a", "b"}},
		{"a\r\nb\rc\n", []string{"a", "b", "c"}},
		{"a\x0bb\x0cc\x1cd e\n\n", []string{"a", "b", "c", "d", "e", ""}},
		{"", nil},
	} {
		if got := splitlines(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitlines(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := strip("\x1f \t x y \xc2\xa0\n"); got != "x y" {
		t.Errorf("strip = %q", got)
	}
	if got := fields(" a  b\tc "); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("fields = %q", got)
	}
	for in, want := range map[string]int64{"12": 12, " +7 ": 7, "-3": -3, "1_000": 1000} {
		if got := pyInt(in); got != want {
			t.Errorf("pyInt(%q) = %d", in, got)
		}
	}
	for _, in := range []string{"", "_1", "1_", "1__0", "x", "1.5", "99999999999999999999", "+"} {
		if _, ok := tryInt(in); ok {
			t.Errorf("tryInt(%q) succeeded", in)
		}
	}
	if raises(func() { pyInt("abc") }) != "ValueError" {
		t.Error("pyInt did not raise ValueError")
	}
	truths := map[any]bool{nil: false, false: false, true: true, int64(0): false, int64(2): true, "": false, "0": true}
	for v, want := range truths {
		if truthy(v) != want {
			t.Errorf("truthy(%#v) != %v", v, want)
		}
	}
	if truthy(newDict()) || truthy(newList()) || !truthy(newList("x")) || !truthy(1.5) {
		t.Error("truthy of a container or other value is wrong")
	}
	if capitalizeFirst("gi0") != "Gi0" || capitalizeFirst("") != "" {
		t.Error("capitalizeFirst")
	}
	if intOrStr("12") != int64(12) || intOrStr("1.2") != "1.2" {
		t.Error("intOrStr")
	}
}

// TestDict covers the dict and list operations and the exceptions they
// raise.
func TestDict(t *testing.T) {
	d := newDict()
	d.set("b", int64(1))
	d.set("a", newList())
	d.set("b", int64(2))
	if !reflect.DeepEqual(d.keyList(), []string{"b", "a"}) {
		t.Fatalf("keys = %v", d.keys)
	}
	d.l("a").appendItem("x")
	d.del("b")
	if d.has("b") || d.size() != 1 {
		t.Fatal("del")
	}
	for kind, fn := range map[string]func(){
		"KeyError":  func() { d.at("missing") },
		"TypeError": func() { d.d("a") },
	} {
		if got := raises(fn); got != kind {
			t.Errorf("raised %q, want %s", got, kind)
		}
	}
	if raises(func() { d.del("missing") }) != "KeyError" || raises(func() { asList(int64(1)) }) != "TypeError" {
		t.Error("del or asList did not raise")
	}
	if !try(func() { raise("KeyError", "x") }) || try(func() {}) {
		t.Error("try")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("try swallowed a non-Python panic")
			}
		}()
		try(func() { panic("defect") })
	}()
	plain := toPlain(d).(map[string]any)
	if !reflect.DeepEqual(plain["a"], []any{"x"}) {
		t.Fatalf("toPlain = %#v", plain)
	}
}

// TestConvertIntfName covers convert_intf_name's three outcomes.
func TestConvertIntfName(t *testing.T) {
	for in, want := range map[string]string{
		"Gi1/0/1":           "GigabitEthernet1/0/1",
		" Te0/0/4":          "TenGigabitEthernet0/0/4",
		"Gig 0/2/0":         "GigabitEthernet0/2/0",
		"foo ethernet 1":    "FooEthernet1",
		"virtual ethernet1": "VirtualEthernet1",
		"Loopback":          "Loopback",
		"":                  "",
	} {
		if got := convertIntfName(in); got != want {
			t.Errorf("convertIntfName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSchemaCheck covers the schema check's refusals.
func TestSchemaCheck(t *testing.T) {
	s := sch(req("a", kInt), opt("b", orKinds{kStr, kNone}), opt("c", sch(anyKey(kBool))), opt("d", kList))
	ok := newDict()
	ok.set("a", true) // a bool is a Python int
	ok.set("b", nil)
	ok.set("extra", "kept")
	if err := check(s, ok, nil); err != nil {
		t.Fatal(err)
	}
	for name, d := range map[string]*dict{
		"missing": newDict(),
		"type":    func() *dict { x := newDict(); x.set("a", "1"); return x }(),
		"or":      func() *dict { x := newDict(); x.set("a", int64(1)); x.set("b", int64(1)); return x }(),
		"nested": func() *dict {
			x := newDict()
			x.set("a", int64(1))
			c := newDict()
			c.set("k", "v")
			x.set("c", c)
			return x
		}(),
		"notdict": func() *dict { x := newDict(); x.set("a", int64(1)); x.set("c", "v"); return x }(),
		"list":    func() *dict { x := newDict(); x.set("a", int64(1)); x.set("d", newDict()); return x }(),
	} {
		err := check(s, d, nil)
		if err == nil || !strings.Contains(err.Error(), "schema") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if joinPath(nil) != "(top)" || pyTypeName(1.5) != "float64" {
		t.Error("joinPath or pyTypeName")
	}
}
