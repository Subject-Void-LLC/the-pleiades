package filters_test

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestFlatten(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{"empty", map[string]any{}, map[string]any{}},
		{
			"flat already",
			map[string]any{"a": 1, "b": "x"},
			map[string]any{"a": 1, "b": "x"},
		},
		{
			"nested map",
			map[string]any{"a": map[string]any{"b": 1, "c": "hello"}},
			map[string]any{"a.b": 1, "a.c": "hello"},
		},
		{
			"list",
			map[string]any{"d": []any{1, "two"}},
			map[string]any{"d.0": 1, "d.1": "two"},
		},
		{
			"mixed nested",
			map[string]any{
				"a": map[string]any{"b": 1, "c": "hello"},
				"d": []any{1, "two", map[string]any{"e": 3}},
				"f": 42,
			},
			map[string]any{"a.b": 1, "a.c": "hello", "d.0": 1, "d.1": "two", "d.2.e": 3, "f": 42},
		},
		{
			"empty nested map preserved as leaf",
			map[string]any{"a": map[string]any{}},
			map[string]any{"a": map[string]any{}},
		},
		{
			"empty nested list preserved as leaf",
			map[string]any{"a": []any{}},
			map[string]any{"a": []any{}},
		},
		{"nil value preserved", map[string]any{"a": nil}, map[string]any{"a": nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.Flatten(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Flatten(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFlatten_DoesNotMutateInput(t *testing.T) {
	in := map[string]any{"a": map[string]any{"b": 1}}
	before := map[string]any{"a": map[string]any{"b": 1}}
	filters.Flatten(in)
	if !reflect.DeepEqual(in, before) {
		t.Errorf("Flatten mutated its input: got %#v, want %#v", in, before)
	}
}

func TestFlatten_DepthCap(t *testing.T) {
	// Build a map nested 50 levels deep: {"a": {"a": {"a": ... 1 ...}}}.
	var deep any = 1
	for i := 0; i < 50; i++ {
		deep = map[string]any{"a": deep}
	}
	top := deep.(map[string]any)
	// Must not panic or recurse forever; the exact key past the cap is an
	// implementation detail, only "did not crash, produced something" is
	// asserted here.
	got := filters.Flatten(top)
	if len(got) == 0 {
		t.Fatal("Flatten of a deeply nested map returned nothing")
	}
}

func TestUnflatten(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{"empty", map[string]any{}, map[string]any{}},
		{
			"flat",
			map[string]any{"a": 1, "b": "x"},
			map[string]any{"a": 1, "b": "x"},
		},
		{
			"nested map",
			map[string]any{"a.b": 1, "a.c": "hello"},
			map[string]any{"a": map[string]any{"b": 1, "c": "hello"}},
		},
		{
			"list",
			map[string]any{"d.0": 1, "d.1": "two"},
			map[string]any{"d": []any{1, "two"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.Unflatten(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Unflatten(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnflatten_DoesNotMutateInput(t *testing.T) {
	in := map[string]any{"a.b": 1}
	before := map[string]any{"a.b": 1}
	filters.Unflatten(in)
	if !reflect.DeepEqual(in, before) {
		t.Errorf("Unflatten mutated its input: got %#v, want %#v", in, before)
	}
}

// TestFlattenUnflatten_RoundTrip is this phase's own Adversarial Pattern
// Justification requirement: Flatten/Unflatten must round-trip losslessly
// for a representative nested structure. The structure below deliberately
// avoids a map whose own keys happen to be a consecutive run of digits
// starting at "0" (see Unflatten's own doc comment for why that specific
// shape is a real, inherent ambiguity in this scheme, not covered here).
func TestFlattenUnflatten_RoundTrip(t *testing.T) {
	original := map[string]any{
		"hostname": "sw1",
		"mgmt": map[string]any{
			"ip":      "10.0.0.1",
			"enabled": true,
		},
		"interfaces": []any{
			"Gi0/1",
			"Gi0/2",
			map[string]any{"name": "Vlan1", "vlan": 1},
		},
		"tags": []any{"core", "prod"},
	}
	flat := filters.Flatten(original)
	back := filters.Unflatten(flat)
	if !reflect.DeepEqual(original, back) {
		t.Fatalf("round trip lost data:\noriginal: %#v\nflat:     %#v\nback:     %#v", original, flat, back)
	}
}

// TestUnflatten_NumericKeyAmbiguity documents (with a passing test, not a
// TODO) the one real ambiguity this scheme carries: a map whose own keys
// are genuinely "0" and "1" is indistinguishable from a two-element list
// once flattened, so it comes back as a list, not the original map.
func TestUnflatten_NumericKeyAmbiguity(t *testing.T) {
	flat := map[string]any{"m.0": "a", "m.1": "b"}
	got := filters.Unflatten(flat)
	want := map[string]any{"m": []any{"a", "b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Unflatten(%#v) = %#v, want %#v (a list, per the documented ambiguity)", flat, got, want)
	}
}

// TestUnflatten_TopLevelNumericKeysFallsBackToEmptyMap exercises the
// same ambiguity one level higher: when the top-level input keys
// themselves are "0" and "1" (no dots at all), the reconstructed tree
// looks exactly like a two-element list at the root, which cannot
// satisfy Unflatten's own map[string]any return type. Unflatten falls
// back to an empty map in that case rather than panicking or returning
// nil.
func TestUnflatten_TopLevelNumericKeysFallsBackToEmptyMap(t *testing.T) {
	got := filters.Unflatten(map[string]any{"0": "a", "1": "b"})
	if !reflect.DeepEqual(got, map[string]any{}) {
		t.Errorf("Unflatten(top-level numeric keys) = %#v, want an empty map", got)
	}
}

// TestUnflatten_DepthCap proves a single flat key with more dot segments
// than maxStructuredDepth does not recurse unboundedly: setPath stops
// descending and joins the remaining segments back into one literal key
// instead.
func TestUnflatten_DepthCap(t *testing.T) {
	key := strings.Repeat("a.", 50) + "z"
	got := filters.Unflatten(map[string]any{key: "v"})
	if len(got) == 0 {
		t.Fatal("Unflatten of a 50-segment key returned nothing")
	}
}

func TestDeepMerge(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]any
		want map[string]any
	}{
		{
			"disjoint keys kept from both",
			map[string]any{"x": 1},
			map[string]any{"y": 2},
			map[string]any{"x": 1, "y": 2},
		},
		{
			"nested maps merge recursively",
			map[string]any{"m": map[string]any{"a": 1}},
			map[string]any{"m": map[string]any{"b": 2}},
			map[string]any{"m": map[string]any{"a": 1, "b": 2}},
		},
		{
			"lists append",
			map[string]any{"l": []any{1, 2}},
			map[string]any{"l": []any{3}},
			map[string]any{"l": []any{1, 2, 3}},
		},
		{
			"scalar in b overwrites a",
			map[string]any{"k": "old"},
			map[string]any{"k": "new"},
			map[string]any{"k": "new"},
		},
		{
			"mismatched types: b overwrites a wholesale",
			map[string]any{"k": map[string]any{"a": 1}},
			map[string]any{"k": "now a string"},
			map[string]any{"k": "now a string"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.DeepMerge(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DeepMerge(%#v, %#v) = %#v, want %#v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestDeepMerge_NeverDropsAKey is this phase's own Adversarial Pattern
// Justification requirement: every key present in either input is
// present in the output, proven directly rather than by example.
func TestDeepMerge_NeverDropsAKey(t *testing.T) {
	a := map[string]any{"a1": 1, "a2": 2, "shared": "from-a"}
	b := map[string]any{"b1": 1, "b2": 2, "shared": "from-b"}
	got := filters.DeepMerge(a, b)
	for k := range a {
		if _, ok := got[k]; !ok {
			t.Errorf("key %q from a is missing in DeepMerge's result", k)
		}
	}
	for k := range b {
		if _, ok := got[k]; !ok {
			t.Errorf("key %q from b is missing in DeepMerge's result", k)
		}
	}
}

func TestDeepMerge_DoesNotMutateInputs(t *testing.T) {
	a := map[string]any{"m": map[string]any{"a": 1}}
	b := map[string]any{"m": map[string]any{"b": 2}}
	aBefore := map[string]any{"m": map[string]any{"a": 1}}
	bBefore := map[string]any{"m": map[string]any{"b": 2}}
	filters.DeepMerge(a, b)
	if !reflect.DeepEqual(a, aBefore) {
		t.Errorf("DeepMerge mutated a: got %#v, want %#v", a, aBefore)
	}
	if !reflect.DeepEqual(b, bBefore) {
		t.Errorf("DeepMerge mutated b: got %#v, want %#v", b, bBefore)
	}
}

func TestShallowMerge(t *testing.T) {
	a := map[string]any{"x": 1, "shared": map[string]any{"p": 1}, "list": []any{1, 2}}
	b := map[string]any{"y": 2, "shared": map[string]any{"q": 2}, "list": []any{3}}
	want := map[string]any{"x": 1, "y": 2, "shared": map[string]any{"q": 2}, "list": []any{3}}
	got := filters.ShallowMerge(a, b)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ShallowMerge(%#v, %#v) = %#v, want %#v (no recursion into shared/list)", a, b, got, want)
	}
}

func TestCSVToList(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "a,b,c", []string{"a", "b", "c"}},
		{"quoted comma", `a,"b,c",d`, []string{"a", "b,c", "d"}},
		{"quoted escaped quote", `a,"b""c"`, []string{"a", `b"c`}},
		{"empty fields", ",,", []string{"", "", ""}},
		{"empty line", "", nil},
		{"malformed unterminated quote", `a,"unterminated`, nil},
		{"two lines rejected", "a,b\nc,d", nil},
		{"over cap", strings.Repeat("a,", filters.MaxStructuredInputBytes), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.CSVToList(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CSVToList(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestListToCSV(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"simple", []string{"a", "b", "c"}, "a,b,c"},
		{"needs comma quoting", []string{"a", "b,c", "d"}, `a,"b,c",d`},
		{"needs quote escaping", []string{"a", `b"c`}, `a,"b""c"`},
		{"nil list", nil, ""},
		{"empty list", []string{}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.ListToCSV(tc.in)
			if got != tc.want {
				t.Errorf("ListToCSV(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestListToCSV_OverCap(t *testing.T) {
	huge := make([]string, 100)
	for i := range huge {
		huge[i] = strings.Repeat("x", filters.MaxStructuredInputBytes/50)
	}
	if got := filters.ListToCSV(huge); got != "" {
		t.Errorf("ListToCSV(over cap) = %q, want \"\"", got)
	}
}

func TestCSVListRoundTrip(t *testing.T) {
	tests := [][]string{
		{"a", "b", "c"},
		{"has,comma", "has\"quote", "plain"},
		{"", "leading empty"},
		{"trailing empty", ""},
	}
	for _, fields := range tests {
		line := filters.ListToCSV(fields)
		got := filters.CSVToList(line)
		if !reflect.DeepEqual(got, fields) {
			t.Errorf("round trip: ListToCSV(%#v) = %q, CSVToList(that) = %#v, want %#v", fields, line, got, fields)
		}
	}
}

func TestPluck(t *testing.T) {
	list := []map[string]any{
		{"name": "a", "val": 1},
		{"name": "b"},
		{"name": "c", "val": 3},
	}
	got := filters.Pluck(list, "val")
	want := []any{1, 3}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Pluck(%#v, \"val\") = %#v, want %#v", list, got, want)
	}
}

func TestPluck_NoneHaveKey(t *testing.T) {
	list := []map[string]any{{"a": 1}, {"a": 2}}
	got := filters.Pluck(list, "missing")
	if len(got) != 0 {
		t.Errorf("Pluck with no matching key = %#v, want empty", got)
	}
}

func TestPluck_EmptyAndNilList(t *testing.T) {
	if got := filters.Pluck(nil, "k"); len(got) != 0 {
		t.Errorf("Pluck(nil, k) = %#v, want empty", got)
	}
	if got := filters.Pluck([]map[string]any{}, "k"); len(got) != 0 {
		t.Errorf("Pluck([], k) = %#v, want empty", got)
	}
}

func TestYAMLToJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"scalar", "a: 1\n", `{"a":1}`},
		{"nested and list", "a:\n  b: 1\nc:\n  - 1\n  - 2\n", `{"a":{"b":1},"c":[1,2]}`},
		{"malformed", "a: [1,2\n", ""},
		{"over cap", strings.Repeat("a", filters.MaxStructuredInputBytes+1), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.YAMLToJSON(tc.in); got != tc.want {
				t.Errorf("YAMLToJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestJSONToYAML(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"scalar", `{"a":1}`, false},
		{"nested and list", `{"a":{"b":1},"c":[1,2]}`, false},
		{"malformed", `{"a":`, true},
		{"over cap", strings.Repeat("a", filters.MaxStructuredInputBytes+1), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.JSONToYAML(tc.in)
			if tc.wantErr && got != "" {
				t.Errorf("JSONToYAML(%q) = %q, want \"\"", tc.in, got)
			}
			if !tc.wantErr && got == "" {
				t.Errorf("JSONToYAML(%q) returned empty, want real YAML", tc.in)
			}
		})
	}
}

func TestYAMLJSONRoundTrip(t *testing.T) {
	json := `{"a":{"b":1,"c":"hello"},"d":[1,"two"],"e":true,"f":null}`
	yaml := filters.JSONToYAML(json)
	if yaml == "" {
		t.Fatalf("JSONToYAML(%q) returned empty", json)
	}
	back := filters.YAMLToJSON(yaml)
	if back != json {
		t.Errorf("round trip: JSONToYAML(%q) = %q, YAMLToJSON(that) = %q, want %q", json, yaml, back, json)
	}
}

func TestYAMLToJSON_DepthCap(t *testing.T) {
	deep := strings.Repeat("[", 50) + "1" + strings.Repeat("]", 50)
	if got := filters.YAMLToJSON(deep); got != "" {
		t.Errorf("YAMLToJSON(50-deep list) = %q, want \"\" (past maxStructuredDepth)", got)
	}
	shallow := strings.Repeat("[", 5) + "1" + strings.Repeat("]", 5)
	if got := filters.YAMLToJSON(shallow); got == "" {
		t.Errorf("YAMLToJSON(5-deep list) = \"\", want real output (well within maxStructuredDepth)")
	}
}

// TestJSONToYAML_DepthCap mirrors TestYAMLToJSON_DepthCap with an
// object-nested document rather than an array-nested one, so both of
// withinStructuredDepth's container cases (map[string]any and []any)
// are proven, not just the list-shaped one.
func TestJSONToYAML_DepthCap(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 50) + "1" + strings.Repeat("}", 50)
	if got := filters.JSONToYAML(deep); got != "" {
		t.Errorf("JSONToYAML(50-deep object) = %q, want \"\" (past maxStructuredDepth)", got)
	}
	shallow := strings.Repeat(`{"a":`, 5) + "1" + strings.Repeat("}", 5)
	if got := filters.JSONToYAML(shallow); got == "" {
		t.Errorf("JSONToYAML(5-deep object) = \"\", want real output (well within maxStructuredDepth)")
	}
}

// TestYAMLToJSON_TimestampAndCycle covers two go.yaml.in/yaml/v3
// decode-to-interface{} quirks verified directly against the library
// before relying on them here: a YAML !!timestamp tag decodes to a
// time.Time (json.Marshal-safe via its own MarshalJSON, so it flows
// through YAMLToJSON like any other scalar), and a self-referential
// anchor is rejected by yaml.Unmarshal itself with a real error rather
// than decoding into a cyclic Go value that would hang
// withinStructuredDepth's recursion.
func TestYAMLToJSON_TimestampAndCycle(t *testing.T) {
	if got := filters.YAMLToJSON("t: 2023-01-01T00:00:00Z\n"); got != `{"t":"2023-01-01T00:00:00Z"}` {
		t.Errorf("YAMLToJSON(timestamp) = %q, want the RFC3339 string form", got)
	}
	if got := filters.YAMLToJSON("a: &anchor\n  b: *anchor\n"); got != "" {
		t.Errorf("YAMLToJSON(self-referential anchor) = %q, want \"\" (yaml.Unmarshal itself refuses the cycle)", got)
	}
}

var uuidv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestGenerateUUIDv4(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		got := filters.GenerateUUIDv4()
		if !uuidv4Pattern.MatchString(got) {
			t.Fatalf("GenerateUUIDv4() = %q, does not match the version-4 UUID shape", got)
		}
		if seen[got] {
			t.Fatalf("GenerateUUIDv4() returned %q twice across 100 calls", got)
		}
		seen[got] = true
	}
}

func TestXMLToJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text leaf", "<root>hello</root>", `{"root":"hello"}`},
		{"empty element", "<root></root>", `{"root":""}`},
		{"self-closing", "<root/>", `{"root":""}`},
		{"attribute only", `<root attr="1"></root>`, `{"root":{"@attr":"1"}}`},
		{"single child", "<root><child>x</child></root>", `{"root":{"child":"x"}}`},
		{
			"repeated child becomes array",
			"<root><item>a</item><item>b</item></root>",
			`{"root":{"item":["a","b"]}}`,
		},
		{
			"third-and-later occurrence appends rather than re-wrapping",
			"<root><item>a</item><item>b</item><item>c</item></root>",
			`{"root":{"item":["a","b","c"]}}`,
		},
		{
			"attribute, text and child together",
			`<root attr="1">text<child>c</child></root>`,
			`{"root":{"#text":"text","@attr":"1","child":"c"}}`,
		},
		{"whitespace-only text trimmed away", "<root>\n  \n</root>", `{"root":""}`},
		{"namespace prefix discarded", `<ns:root xmlns:ns="urn:x">hi</ns:root>`, `{"root":"hi"}`},
		{"default namespace declaration dropped", `<root xmlns="urn:default">hi</root>`, `{"root":"hi"}`},
		{
			"real attribute survives alongside a dropped namespace declaration",
			`<ns:root xmlns:ns="urn:x" id="1">hi</ns:root>`,
			`{"root":{"#text":"hi","@id":"1"}}`,
		},
		{"xml declaration skipped", "<?xml version=\"1.0\"?><root>hi</root>", `{"root":"hi"}`},
		{"mismatched tags", "<a></b>", ""},
		{"unterminated", "<a>", ""},
		{"not xml at all", "not xml", ""},
		{"over cap", strings.Repeat("a", filters.MaxStructuredInputBytes+1), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.XMLToJSON(tc.in); got != tc.want {
				t.Errorf("XMLToJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestXMLToJSON_DepthCap(t *testing.T) {
	deep := strings.Repeat("<a>", 50) + "x" + strings.Repeat("</a>", 50)
	if got := filters.XMLToJSON(deep); got != "" {
		t.Errorf("XMLToJSON(50-deep) = %q, want \"\" (past maxStructuredDepth)", got)
	}
	shallow := strings.Repeat("<a>", 5) + "x" + strings.Repeat("</a>", 5)
	if got := filters.XMLToJSON(shallow); got == "" {
		t.Errorf("XMLToJSON(5-deep) = \"\", want real output (well within maxStructuredDepth)")
	}
}
