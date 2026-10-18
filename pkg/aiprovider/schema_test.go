// Tests for holding a structured answer to its schema.
package aiprovider_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
)

// TestSchema_ChecksTheSubsetAndRefusesTheRest.
func TestSchema_ChecksTheSubsetAndRefusesTheRest(t *testing.T) {
	s, err := aiprovider.ParseSchema([]byte(`{"type":"object","required":["label","score"],"additionalProperties":false,
"properties":{"label":{"type":"string","enum":["bug","feature"]},"score":{"type":"integer","minimum":0,"maximum":10},
"tags":{"type":"array","items":{"type":"string","maxLength":8},"maxItems":3}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate([]byte(`{"label":"bug","score":7,"tags":["ui"]}`)); err != nil {
		t.Errorf("a valid answer = %v", err)
	}
	for answer, want := range map[string]string{
		`{"label":"bug"}`:                                    "has no score",
		`{"label":"chore","score":1}`:                        "not one of",
		`{"label":"bug","score":1.5}`:                        "not integer",
		`{"label":"bug","score":11}`:                         "above 10",
		`{"label":"bug","score":1,"extra":true}`:             "does not allow",
		`{"label":"bug","score":1,"tags":["toolong!!"]}`:     "longer than 8",
		`{"label":"bug","score":1,"tags":["a","b","c","d"]}`: "more than 3 items",
		`[]`:    "is array, not object",
		`{} {}`: "more than one JSON value",
		`nope`:  "not JSON",
	} {
		if err := s.Validate([]byte(answer)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Validate(%s) = %v, want %q", answer, err, want)
		}
	}
	for schema, want := range map[string]string{
		`{"type":"string","pattern":"^a"}`:             `uses "pattern" at #`,
		`{"properties":{"x":{"oneOf":[]}}}`:            `"oneOf" at #/properties/x`,
		`{"type":"array","items":{"$ref":"#/defs/x"}}`: `"$ref" at #/items`,
		`[1]`: "not a JSON object",
	} {
		if _, err := aiprovider.ParseSchema([]byte(schema)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseSchema(%s) = %v, want %q", schema, err, want)
		}
	}
}

// TestSchema_RefusesAKeywordOfTheWrongType: a keyword whose value is not
// the JSON type it takes would be read as absent, so the schema would parse
// and check nothing for it; it is refused instead.
func TestSchema_RefusesAKeywordOfTheWrongType(t *testing.T) {
	for _, schema := range []string{
		`{"properties":[]}`, `{"items":true}`, `{"items":[{"type":"string"}]}`, `{"required":"x"}`,
		`{"required":[1]}`, `{"enum":"x"}`, `{"minimum":"1"}`, `{"maxItems":"3"}`,
		`{"additionalProperties":"no"}`, `{"type":7}`, `{"type":[1]}`,
		`{"properties":{"a":{"required":"x"}}}`,
	} {
		if _, err := aiprovider.ParseSchema([]byte(schema)); err == nil || !strings.Contains(err.Error(), "must be") {
			t.Errorf("ParseSchema(%s) = %v, want the keyword's type refused", schema, err)
		}
	}
	for _, schema := range []string{`{"type":["string","null"],"enum":["a",null]}`, `{"additionalProperties":{"type":"string"}}`, `{"const":{"a":1}}`} {
		if _, err := aiprovider.ParseSchema([]byte(schema)); err != nil {
			t.Errorf("ParseSchema(%s) = %v", schema, err)
		}
	}
}

// TestSchema_AnErrorDoesNotCarryTheAnswerWhole: a long value or key in an
// answer that breaks the schema is cut in the error that names it.
func TestSchema_AnErrorDoesNotCarryTheAnswerWhole(t *testing.T) {
	s, err := aiprovider.ParseSchema([]byte(`{"type":"object","additionalProperties":false,"properties":{"label":{"enum":["bug"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("v", 1<<20)
	for _, answer := range []string{`{"label":"` + long + `"}`, `{"` + long + `":1}`} {
		err := s.Validate([]byte(answer))
		if err == nil || len(err.Error()) > 1024 {
			t.Errorf("a %d byte answer's error = %d bytes", len(answer), len(fmt.Sprint(err)))
		}
	}
}
