// Tests of the schema builder and validator: what each Go shape becomes,
// what the builder refuses, and what the validator catches in a document.
package jsonschema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// level is an enum held as an integer, which only Fixed describes rightly.
type level int

// inner is a nested struct.
type inner struct {
	// Code is a short code.
	Code string `json:"code"`
}

// sample holds one field of every shape the builder describes.
type sample struct {
	// Name is a required string.
	Name string `json:"name"`
	// Count is an integer.
	Count int `json:"count"`
	// Ratio is a number.
	Ratio float64 `json:"ratio"`
	// On is a boolean.
	On bool `json:"on"`
	// At is a time.
	At time.Time `json:"at"`
	// Tags is a list.
	Tags []string `json:"tags,omitempty"`
	// Inner is a nested object.
	Inner inner `json:"inner"`
	// Next is an optional pointer.
	Next *inner `json:"next,omitempty"`
	// Stats holds anything.
	Stats map[string]any `json:"stats,omitempty"`
	// Level is the fixed enum.
	Level level `json:"level"`
	// Untagged has no JSON name, so it is written under its Go name.
	Untagged string
	// First and Second are a pair, one comment above both.
	First  string `json:"first"`
	Second string `json:"second"`
	// hidden is unexported, so never written.
	hidden string
	// Skipped is tagged out.
	Skipped string `json:"-"`
}

// sampleFile writes a Go file holding sample's declarations, for FieldDocs.
func sampleFile(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("jsonschema_test.go")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "model.go")
	if err := os.WriteFile(path, src, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// buildSample builds sample's schema, open or closed, as a consumer reads it.
func buildSample(t *testing.T, open bool) map[string]any {
	t.Helper()
	docs, err := FilesDocs(sampleFile(t))
	if err != nil {
		t.Fatal(err)
	}
	b := &Builder{Docs: docs, Source: "the test", Open: open, AbsentNotNull: open,
		Known: map[string][]string{"inner.Code": {"a", "b"}},
		Fixed: map[reflect.Type]map[string]any{reflect.TypeFor[level](): {"enum": []any{"low", "high"}}}}
	built, err := b.Root(reflect.TypeFor[sample](), "Sample")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(built)
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestBuilder_DescribesEachShape(t *testing.T) {
	schema := buildSample(t, false)
	props := schema["properties"].(map[string]any)
	for name, want := range map[string]string{
		"name": `"type":"string"`, "count": `"type":"integer"`, "ratio": `"type":"number"`, "on": `"type":"boolean"`,
		"at": `"format":"date-time"`, "tags": `"type":["array","null"]`, "inner": `"$ref":"#/$defs/inner"`,
		"second": `"description":"First and Second are a pair, one comment above both."`, "next": `"oneOf"`, "stats": `"additionalProperties":{}`, "level": `"enum":["low","high"]`, "Untagged": `"type":"string"`,
	} {
		got, _ := json.Marshal(props[name])
		if !strings.Contains(string(got), want) {
			t.Errorf("%s = %s, want it to hold %s", name, got, want)
		}
	}
	for _, absent := range []string{"hidden", "Skipped", "-"} {
		if _, ok := props[absent]; ok {
			t.Errorf("%s is in the schema, but encoding/json never writes it", absent)
		}
	}
	if schema["additionalProperties"] != false {
		t.Error("a closed schema leaves its object open")
	}
	required := schema["required"].([]any)
	if !slices.Contains(required, any("name")) || slices.Contains(required, any("tags")) {
		t.Errorf("required = %v, want name and not the omitempty tags", required)
	}
	code := schema["$defs"].(map[string]any)["inner"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)
	if !strings.Contains(code["description"].(string), "Known values: a, b") || code["examples"] == nil {
		t.Errorf("a known field's schema does not publish its values: %v", code)
	}
}

func TestBuilder_OpenAndAbsentNotNull(t *testing.T) {
	schema := buildSample(t, true)
	if _, closed := schema["additionalProperties"]; closed {
		t.Error("an open schema closes its object")
	}
	next, _ := json.Marshal(schema["properties"].(map[string]any)["next"])
	if strings.Contains(string(next), "null") {
		t.Errorf("an omitempty pointer admits null, but a nil one is absent: %s", next)
	}
}

func TestBuilder_FixedIsNotShared(t *testing.T) {
	fixed := map[string]any{"enum": []any{"x"}}
	type two struct {
		// A is one.
		A level `json:"a"`
		// B is the other.
		B level `json:"b"`
	}
	b := &Builder{Docs: map[string]string{"two.A": "first", "two.B": "second"}, Fixed: map[reflect.Type]map[string]any{reflect.TypeFor[level](): fixed}}
	obj, err := b.Object(reflect.TypeFor[two]())
	if err != nil {
		t.Fatal(err)
	}
	props := obj["properties"].(map[string]any)
	if props["a"].(map[string]any)["description"] != "first" || props["b"].(map[string]any)["description"] != "second" {
		t.Errorf("two fields of one fixed type share a description: %v", props)
	}
	if _, written := fixed["description"]; written {
		t.Error("the builder wrote into the Fixed schema it was given")
	}
}

func TestBuilder_Refusals(t *testing.T) {
	type undocumented struct {
		X string `json:"x"`
	}
	b := &Builder{Docs: map[string]string{}, Defs: map[string]any{}, Source: "nowhere"}
	if _, err := b.Object(reflect.TypeFor[undocumented]()); err == nil || !strings.Contains(err.Error(), "undocumented.X has no doc comment in nowhere") {
		t.Errorf("an undocumented field: %v", err)
	}
	if _, err := b.Of(reflect.TypeFor[chan int]()); err == nil {
		t.Error("a channel was described")
	}
	if _, err := b.Of(reflect.TypeFor[map[int]string]()); err == nil {
		t.Error("a map with integer keys was described")
	}
	// A comment that does not name the next field leaves it undocumented.
	unnamed := filepath.Join(t.TempDir(), "unnamed.go")
	if err := os.WriteFile(unnamed, []byte("package x\n\n// T is a type.\ntype T struct {\n\t// A is one.\n\tA string\n\tB string\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if docs, err := FieldDocs(unnamed); err != nil || docs["T.A"] != "A is one." || docs["T.B"] != "" {
		t.Errorf("docs = %v, %v: want A documented and B not", docs, err)
	}
	if _, err := FieldDocs(filepath.Join(t.TempDir(), "missing.go")); err == nil {
		t.Error("a missing file gave docs")
	}
	if _, err := FilesDocs(filepath.Join(t.TempDir(), "missing.go")); err == nil {
		t.Error("a missing file gave docs")
	}
}

func TestValidator(t *testing.T) {
	schema := buildSample(t, true)
	good := `{"name":"n","count":2,"ratio":0.5,"on":true,"at":"2026-10-28T00:00:00Z","tags":["x"],"inner":{"code":"a"},
		"next":{"code":"b"},"stats":{"any":[1,"two",{"three":null}]},"level":"high","Untagged":"u","first":"f","second":"s"}`
	var doc any
	if err := json.Unmarshal([]byte(good), &doc); err != nil {
		t.Fatal(err)
	}
	v := NewValidator(schema)
	if problems := v.Check("sample", doc, schema); len(problems) != 0 {
		t.Errorf("a good document: %v", problems)
	}
	if unfilled := v.Unfilled(); len(unfilled) != 0 {
		t.Errorf("unfilled after a full document: %v", unfilled)
	}
	for name, c := range map[string]struct{ doc, want string }{
		"a wrong type":        {`{"name":1}`, "sample.name = 1 is not string"},
		"a missing required":  {`{}`, "lacks required name"},
		"an undeclared field": {`{"name":"n","extra":1}`, "has extra, which the schema does not declare"},
		"an unknown value":    {`{"inner":{"code":"z"}}`, `"z" is not one of the values the schema publishes`},
		"an enum miss":        {`{"level":"mid"}`, "is not one of"},
		"a non-object":        {`{"inner":3}`, "sample.inner is not an object"},
		"a bad item":          {`{"tags":[1]}`, "sample.tags[0] = 1 is not string"},
		"a non-integer":       {`{"count":1.5}`, "is not integer"},
		"a bad pointer":       {`{"next":4}`, "sample.next is not an object"},
	} {
		var bad any
		if err := json.Unmarshal([]byte(c.doc), &bad); err != nil {
			t.Fatal(err)
		}
		problems := strings.Join(NewValidator(schema).Check("sample", bad, schema), "\n")
		if !strings.Contains(problems, c.want) {
			t.Errorf("%s: problems %q, want one holding %q", name, problems, c.want)
		}
	}
	partial := NewValidator(schema)
	var small any
	_ = json.Unmarshal([]byte(`{"name":"n"}`), &small)
	partial.Check("sample", small, schema)
	if unfilled := partial.Unfilled(); !slices.Contains(unfilled, "Report.count") {
		t.Errorf("unfilled = %v, want Report.count among them", unfilled)
	}
}
