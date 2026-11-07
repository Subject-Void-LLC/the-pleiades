// Package main: tests of the comparer's rules, each in the direction it
// breaks and the direction it does not.
package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// decode parses one JSON literal for a test.
func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad test JSON %s: %v", s, err)
	}
	return m
}

// tokens runs one comparison and returns its findings' tokens.
func tokens(t *testing.T, k kind, oldS, newS string) []string {
	t.Helper()
	f, err := compareFile("x.json", k, []byte(oldS), []byte(newS))
	if err != nil {
		t.Fatalf("compareFile: %v", err)
	}
	out := []string{}
	for _, x := range f {
		out = append(out, x.Token())
	}
	return out
}

func TestSchemaRulesBreakOnlyInTheirDirection(t *testing.T) {
	obj := func(props, req string) string {
		return `{"type":"object","properties":{` + props + `},"required":[` + req + `]}`
	}
	cases := []struct {
		name       string
		old, new   string
		wantInput  []string
		wantOutput []string
	}{
		{
			name: "a required property removed breaks a reader, not a writer of an open schema",
			old:  obj(`"a":{"type":"string"}`, `"a"`), new: obj(``, ``),
			wantInput:  []string{},
			wantOutput: []string{"property-removed x.json#/properties/a"},
		},
		{
			name:       "a property removed from a closed schema breaks a writer and a reader",
			old:        `{"type":"object","additionalProperties":false,"properties":{"a":{}}}`,
			new:        `{"type":"object","additionalProperties":false,"properties":{}}`,
			wantInput:  []string{"property-removed x.json#/properties/a"},
			wantOutput: []string{"property-removed x.json#/properties/a"},
		},
		{
			name: "an optional property removed breaks a reader that used it",
			old:  obj(`"a":{"type":"string"}`, ``), new: obj(``, ``),
			wantInput:  []string{},
			wantOutput: []string{"property-removed x.json#/properties/a"},
		},
		{
			name:       "a keyword beside a $ref is compared where it is written",
			old:        `{"properties":{"a":{"$ref":"#/$defs/s"}},"$defs":{"s":{"type":"string"}}}`,
			new:        `{"properties":{"a":{"$ref":"#/$defs/s","enum":["x"]}},"$defs":{"s":{"type":"string"}}}`,
			wantInput:  []string{"enum-value-removed x.json#/properties/a/enum"},
			wantOutput: []string{},
		},
		{
			name: "additionalProperties given a schema where any value was accepted narrows",
			old:  `{"type":"object"}`, new: `{"type":"object","additionalProperties":{"type":"string"}}`,
			wantInput:  []string{"additional-properties-narrowed x.json#/additionalProperties"},
			wantOutput: []string{},
		},
		{
			name: "a property becoming required breaks a writer",
			old:  obj(`"a":{"type":"string"}`, ``), new: obj(`"a":{"type":"string"}`, `"a"`),
			wantInput:  []string{"required-added x.json#/properties/a"},
			wantOutput: []string{},
		},
		{
			name: "a new required property breaks a writer",
			old:  obj(``, ``), new: obj(`"b":{"type":"string"}`, `"b"`),
			wantInput:  []string{"required-added x.json#/properties/b"},
			wantOutput: []string{},
		},
		{
			name: "a property no longer required breaks a reader",
			old:  obj(`"a":{"type":"string"}`, `"a"`), new: obj(`"a":{"type":"string"}`, ``),
			wantInput:  []string{},
			wantOutput: []string{"required-removed x.json#/properties/a"},
		},
		{
			name: "an optional property added breaks nobody",
			old:  obj(``, ``), new: obj(`"c":{"type":"string"}`, ``),
			wantInput: []string{}, wantOutput: []string{},
		},
		{
			name: "null dropped from a type narrows",
			old:  `{"type":["string","null"]}`, new: `{"type":"string"}`,
			wantInput: []string{"type-narrowed x.json#/type"}, wantOutput: []string{},
		},
		{
			name: "number to integer narrows, and back widens",
			old:  `{"type":"number"}`, new: `{"type":"integer"}`,
			wantInput: []string{"type-narrowed x.json#/type"}, wantOutput: []string{},
		},
		{
			name: "integer to number widens",
			old:  `{"type":"integer"}`, new: `{"type":"number"}`,
			wantInput: []string{}, wantOutput: []string{"type-widened x.json#/type"},
		},
		{
			name: "an enum value added breaks a reader, one removed a writer",
			old:  `{"enum":["a","b"]}`, new: `{"enum":["b","c"]}`,
			wantInput:  []string{"enum-value-removed x.json#/enum"},
			wantOutput: []string{"enum-value-added x.json#/enum"},
		},
		{
			name: "enum order and annotations are not changes",
			old:  `{"enum":["a","b"],"description":"one"}`, new: `{"enum":["b","a"],"description":"two","title":"t"}`,
			wantInput: []string{}, wantOutput: []string{},
		},
		{
			name: "a maximum lowered narrows and a minimum lowered widens",
			old:  `{"type":"integer","minimum":1,"maximum":10}`, new: `{"type":"integer","minimum":0,"maximum":5}`,
			wantInput:  []string{"bound-tightened x.json#/maximum"},
			wantOutput: []string{"bound-loosened x.json#/minimum"},
		},
		{
			name: "a pattern changed breaks both",
			old:  `{"type":"string","pattern":"^a$"}`, new: `{"type":"string","pattern":"^b$"}`,
			wantInput:  []string{"pattern-changed x.json#/pattern"},
			wantOutput: []string{"pattern-changed x.json#/pattern"},
		},
		{
			name: "a default changed breaks a writer only",
			old:  `{"type":"integer","default":1}`, new: `{"type":"integer","default":2}`,
			wantInput: []string{"default-changed x.json#/default"}, wantOutput: []string{},
		},
		{
			name: "additionalProperties closed narrows",
			old:  `{"type":"object"}`, new: `{"type":"object","additionalProperties":false}`,
			wantInput: []string{"additional-properties-closed x.json#/additionalProperties"}, wantOutput: []string{},
		},
		{
			name: "reordered oneOf branches are not a change",
			old:  `{"oneOf":[{"type":"string"},{"type":"integer"}]}`, new: `{"oneOf":[{"type":"integer"},{"type":"string"}]}`,
			wantInput: []string{}, wantOutput: []string{},
		},
		{
			name:      "one changed oneOf branch is compared as itself",
			old:       `{"oneOf":[{"type":"integer","maximum":86400},{"type":"string"}]}`,
			new:       `{"oneOf":[{"type":"string"},{"type":"integer","maximum":3600}]}`,
			wantInput: []string{"bound-tightened x.json#/oneOf/1/maximum"}, wantOutput: []string{},
		},
		{
			name:       "a oneOf branch removed narrows",
			old:        `{"oneOf":[{"type":"integer"},{"type":"string"},{"type":"array"}]}`,
			new:        `{"oneOf":[{"type":"string"}]}`,
			wantInput:  []string{"branch-removed x.json#/oneOf/0", "branch-removed x.json#/oneOf/2"},
			wantOutput: []string{},
		},
		{
			name: "a keyword with no rule fails closed",
			old:  `{"type":"array"}`, new: `{"type":"array","uniqueItems":true}`,
			wantInput:  []string{"schema-changed x.json#/uniqueItems"},
			wantOutput: []string{"schema-changed x.json#/uniqueItems"},
		},
		{
			name: "an item schema change is found under items",
			old:  `{"type":"array","items":{"type":["string","null"]}}`, new: `{"type":"array","items":{"type":"string"}}`,
			wantInput: []string{"type-narrowed x.json#/items/type"}, wantOutput: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokens(t, inputSchema, tc.old, tc.new); !reflect.DeepEqual(got, tc.wantInput) {
				t.Errorf("as input: %q, want %q", got, tc.wantInput)
			}
			if got := tokens(t, outputSchema, tc.old, tc.new); !reflect.DeepEqual(got, tc.wantOutput) {
				t.Errorf("as output: %q, want %q", got, tc.wantOutput)
			}
		})
	}
}

func TestASharedDefinitionIsReportedOnceAtItsOwnPointer(t *testing.T) {
	// Two properties refer to one definition, and the definition refers to
	// itself (as a runbook task's block does): the change is reported once,
	// at the definition, and the cycle ends.
	old := `{"type":"object","properties":{"a":{"$ref":"#/$defs/task"},"b":{"$ref":"#/$defs/task"}},
	  "$defs":{"task":{"type":"object","properties":{"name":{"type":["string","null"]},"block":{"type":"array","items":{"$ref":"#/$defs/task"}}}}}}`
	cur := `{"type":"object","properties":{"a":{"$ref":"#/$defs/task"},"b":{"$ref":"#/$defs/task"}},
	  "$defs":{"task":{"type":"object","properties":{"name":{"type":"string"},"block":{"type":"array","items":{"$ref":"#/$defs/task"}}}}}}`
	got := tokens(t, inputSchema, old, cur)
	want := []string{"type-narrowed x.json#/$defs/task/properties/name/type"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings %q, want %q", got, want)
	}
}

func TestARenamedDefinitionOfTheSameShapeIsNotAChange(t *testing.T) {
	old := `{"properties":{"a":{"$ref":"#/$defs/Old"}},"$defs":{"Old":{"type":"string"}}}`
	cur := `{"properties":{"a":{"$ref":"#/$defs/New"}},"$defs":{"New":{"type":"string"}}}`
	if got := tokens(t, outputSchema, old, cur); len(got) != 0 {
		t.Errorf("findings %q, want none", got)
	}
	// The same rename with a narrowed shape is found where it is used.
	cur = `{"properties":{"a":{"$ref":"#/$defs/New"}},"$defs":{"New":{"type":"string","maxLength":3}}}`
	want := []string{"bound-tightened x.json#/properties/a/maxLength"}
	if got := tokens(t, inputSchema, old, cur); !reflect.DeepEqual(got, want) {
		t.Errorf("findings %q, want %q", got, want)
	}
}

func TestEscapeFollowsRFC6901(t *testing.T) {
	if got := escape("/jobs/{id}~x"); got != "~1jobs~1{id}~0x" {
		t.Errorf("escape = %q", got)
	}
	root := decode(t, `{"a":{"b/c":{"d~e":1}}}`)
	if got := resolve(root, "#/a/b~1c/d~0e"); got != float64(1) {
		t.Errorf("resolve = %v, want 1", got)
	}
	if got := resolve(root, "other.json#/a"); got != nil {
		t.Errorf("a remote reference resolved to %v, want nil", got)
	}
}
