// This file holds Field.KeepsOriginal to its contract: which submissions may
// carry an original, what Values reads back, what Validate skips, what an
// edit form renders, and which fields registration refuses it on.
package view

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

// originalsFields is a form with text and lookup fields that keep their
// originals, and one that keeps none.
func originalsFields() []Field {
	members := func(context.Context) ([]Option, error) {
		return []Option{{Label: "a", Value: "1"}, {Label: "b", Value: "2"}, {Label: "c", Value: "3"}}, nil
	}
	return []Field{
		{Name: "name", Label: "NAME", Kind: KindText, InForm: true, Required: true, MaxLen: 5, KeepsOriginal: true},
		{Name: "members", Label: "MEMBERS", Kind: KindLookup, InForm: true, Options: members, KeepsOriginal: true},
		{Name: "note", Label: "NOTE", Kind: KindText, InForm: true},
	}
}

// TestNewValues_AnOriginalIsDeclaredOnlyForAFieldThatKeepsOne: an original is
// accepted on an edit for a field that keeps one, and is undeclared input on
// a create, for a field that keeps none, and for a field nobody declared.
func TestNewValues_AnOriginalIsDeclaredOnlyForAFieldThatKeepsOne(t *testing.T) {
	fields := originalsFields()
	cases := map[string]struct {
		key     string
		editing bool
		want    bool // want it declared
	}{
		"an edit, a field keeping one":  {key: OriginalKey("name"), editing: true, want: true},
		"a create":                      {key: OriginalKey("name"), editing: false},
		"a field keeping none":          {key: OriginalKey("note"), editing: true},
		"an undeclared field":           {key: OriginalKey("owner"), editing: true},
		"the bare prefix":               {key: "_original.", editing: true},
		"a near miss on the prefix":     {key: "_originalname", editing: true},
		"an edit, a lookup keeping one": {key: OriginalKey("members"), editing: true, want: true},
	}
	for name, tc := range cases {
		_, undeclared := NewValues(fields, url.Values{tc.key: {"x"}}, tc.editing)
		if got := len(undeclared) == 0; got != tc.want {
			t.Errorf("%s: declared %v, want %v (undeclared %v)", name, got, tc.want, undeclared)
		}
	}
}

// TestValues_OriginalAndUnchanged reads originals back: exactly one value is
// an original, a lookup's is split, and a field is unchanged only when its
// submission equals its original (a lookup's in any order).
func TestValues_OriginalAndUnchanged(t *testing.T) {
	fields := originalsFields()
	raw := url.Values{
		"name": {"edge"}, OriginalKey("name"): {"edge"},
		"members": {"2", "1"}, OriginalKey("members"): {" 1, 2 ,"},
		"note": {"n"},
	}
	v, undeclared := NewValues(fields, raw, true)
	if len(undeclared) > 0 {
		t.Fatalf("undeclared %v", undeclared)
	}
	if got, ok := v.Original("name"); !ok || got != "edge" {
		t.Errorf("Original(name) = %q, %v", got, ok)
	}
	if got, ok := v.OriginalSelected("members"); !ok || strings.Join(got, ",") != "1,2" {
		t.Errorf("OriginalSelected(members) = %v, %v, want [1 2]", got, ok)
	}
	if !v.Unchanged("name") || !v.Unchanged("members") {
		t.Error("an untouched name and a reordered selection should be unchanged")
	}
	if _, ok := v.Original("note"); ok || v.Unchanged("note") {
		t.Error("a field keeping no original reported one")
	}

	raw["name"], raw["members"] = []string{"core"}, []string{"1", "3"}
	if v, _ = NewValues(fields, raw, true); v.Unchanged("name") || v.Unchanged("members") {
		t.Error("an edited name and a changed selection read as unchanged")
	}
	raw[OriginalKey("name")] = []string{"a", "b"}
	if v, _ = NewValues(fields, raw, true); func() bool { _, ok := v.Original("name"); return ok }() {
		t.Error("two originals for one field were read as one")
	}
	if v, _ = NewValues(fields, url.Values{"name": {"edge"}}, true); func() bool { _, ok := v.OriginalSelected("members"); return ok }() {
		t.Error("a missing lookup original was read as present")
	}
}

// TestValidate_SkipsAFieldLeftAsTheFormShowedIt: a stored name past MaxLen
// does not refuse an edit that leaves it, and does refuse one that changes it.
func TestValidate_SkipsAFieldLeftAsTheFormShowedIt(t *testing.T) {
	ctx := context.Background()
	fields := originalsFields()
	long := "too-long-name"
	v, _ := NewValues(fields, url.Values{"name": {long}, OriginalKey("name"): {long}}, true)
	if errs := Validate(ctx, fields, v); errs.Any() {
		t.Errorf("an unchanged long name was refused: %v", errs)
	}
	v, _ = NewValues(fields, url.Values{"name": {long + "x"}, OriginalKey("name"): {long}}, true)
	if errs := Validate(ctx, fields, v); len(errs["name"]) == 0 {
		t.Error("a changed long name was not refused")
	}
	v, _ = NewValues(fields, url.Values{"name": {long}}, true)
	if errs := Validate(ctx, fields, v); len(errs["name"]) == 0 {
		t.Error("a long name with no original was not checked")
	}
}

// TestFormModel_OriginalInputs renders one hidden input per field keeping an
// original that has one, on an edit only, and a lookup's holds only the
// selections the control offers.
func TestFormModel_OriginalInputs(t *testing.T) {
	fields := originalsFields()
	opts, _ := fields[1].Options(context.Background())
	m := FormModel{
		ID:        "1",
		FieldSet:  fields,
		Options:   map[string][]Option{"members": opts},
		Originals: map[string]string{"name": "edge", "members": "1,99,3", "note": "n"},
	}
	got := map[string]string{}
	for _, in := range m.OriginalInputs() {
		got[in.Name] = in.Value
	}
	want := map[string]string{OriginalKey("name"): "edge", OriginalKey("members"): "1,3"}
	if len(got) != len(want) || got[OriginalKey("name")] != "edge" || got[OriginalKey("members")] != "1,3" {
		t.Errorf("OriginalInputs() = %v, want %v", got, want)
	}

	delete(m.Originals, "name")
	if inputs := m.OriginalInputs(); len(inputs) != 1 {
		t.Errorf("a field with no original rendered an input: %v", inputs)
	}
	m.ID = ""
	if inputs := m.OriginalInputs(); inputs != nil {
		t.Errorf("a create form rendered originals: %v", inputs)
	}
}

// TestValidateFields_RefusesAnOriginalThatCouldNotBeKept: a password control
// or a RefusedValue field would put a stored secret in the page, and an
// immutable or form-less field has no edit form to carry one.
func TestValidateFields_RefusesAnOriginalThatCouldNotBeKept(t *testing.T) {
	cases := map[string]Field{
		"a password":       {Name: "p", Label: "P", Kind: KindPassword, InForm: true, KeepsOriginal: true},
		"a refused value":  {Name: "u", Label: "U", Kind: KindText, InForm: true, KeepsOriginal: true, RefusedValue: func(string) string { return "" }},
		"an immutable one": {Name: "o", Label: "O", Kind: KindText, InForm: true, Immutable: true, KeepsOriginal: true},
		"one in no form":   {Name: "r", Label: "R", Kind: KindText, InList: true, KeepsOriginal: true},
	}
	for name, f := range cases {
		if err := validateFields([]Field{f}); err == nil || !strings.Contains(err.Error(), "keeps its original") {
			t.Errorf("%s: validateFields() = %v, want a refusal", name, err)
		}
	}
	if err := validateFields(originalsFields()); err != nil {
		t.Errorf("validateFields() on fields that may keep originals = %v", err)
	}
}

// TestUnchanged_ComparesWhatABrowserSendsBack: a browser posts every line
// break as CRLF, drops line breaks from a text input, and the parser drops a
// textarea's leading line break. A value stored with LF, a name holding a line
// break and a description opening with one each read as unchanged when the
// form posts them back as a browser does, and as changed when they differ.
func TestUnchanged_ComparesWhatABrowserSendsBack(t *testing.T) {
	fields := []Field{
		{Name: "name", Label: "NAME", Kind: KindText, InForm: true, KeepsOriginal: true},
		{Name: "notes", Label: "NOTES", Kind: KindLongText, InForm: true, KeepsOriginal: true},
	}
	cases := map[string]struct {
		name, origName, notes, origNotes string
		want                             bool
	}{
		"LF stored, CRLF posted":        {name: "a", origName: "a", notes: "x\r\ny", origNotes: "x\r\ny", want: true},
		"a line break in a name":        {name: "ab", origName: "a\r\nb", notes: "x", origNotes: "x", want: true},
		"a textarea's leading break":    {name: "a", origName: "a", notes: "x", origNotes: "\r\nx", want: true},
		"a lone CR":                     {name: "a", origName: "a", notes: "x\r\ny", origNotes: "x\ry", want: true},
		"a real change in the notes":    {name: "a", origName: "a", notes: "x\r\nz", origNotes: "x\r\ny"},
		"a second leading break counts": {name: "a", origName: "a", notes: "x", origNotes: "\n\nx"},
	}
	for label, tc := range cases {
		v, _ := NewValues(fields, url.Values{
			"name": {tc.name}, OriginalKey("name"): {tc.origName},
			"notes": {tc.notes}, OriginalKey("notes"): {tc.origNotes},
		}, true)
		if got := v.Unchanged("name") && v.Unchanged("notes"); got != tc.want {
			t.Errorf("%s: unchanged %v, want %v", label, got, tc.want)
		}
	}
	if got := Comparable(KindLookup, "a\r\nb"); got != "a\r\nb" {
		t.Errorf("Comparable(lookup) = %q, want the value as it is", got)
	}
}
