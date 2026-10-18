// Test vectors for the canonical form, and every ambiguity it refuses.
package promptsign_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
)

// TestCanonical_Vectors: each input and its exact canonical form. These
// are the format's test vectors: a second implementation must produce the
// same bytes.
func TestCanonical_Vectors(t *testing.T) {
	for in, want := range map[string]string{
		`{"b":1,"a":2}`:                                             `{"a":2,"b":1}`,
		" {\n \"z\" : [ 1 , 2 ] ,\t\"a\":{}}\n":                     `{"a":{},"z":[1,2]}`,
		`{"n":[1.0,0.70,-0,1.5,-12,9007199254740991]}`:              `{"n":[1,0.7,0,1.5,-12,9007199254740991]}`,
		`{"s":"tab\there\nline \"q\" back\\slash \/ \u0001 é é 😀"}`: `{"s":"tab\there\nline \"q\" back\\slash / \u0001 é é 😀"}`,
		`{"é":1,"e":2,"Z":3,"a":4}`:                                 `{"Z":3,"a":4,"e":2,"é":1}`,
		`[true,false,null,"",{},[]]`:                                `[true,false,null,"",{},[]]`,
		`{"a":{"c":1,"b":{"z":0,"y":0}}}`:                           `{"a":{"b":{"y":0,"z":0},"c":1}}`,
	} {
		got, err := promptsign.Canonical([]byte(in))
		if err != nil {
			t.Errorf("Canonical(%s) = %v", in, err)
			continue
		}
		if string(got) != want {
			t.Errorf("Canonical(%s)\n got %s\nwant %s", in, got, want)
		}
	}
}

// TestCanonical_RefusesWhatTwoReadersCouldReadDifferently.
func TestCanonical_RefusesWhatTwoReadersCouldReadDifferently(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1,"a":2}`:                 `the key "a" appears twice`,
		`{"x":{"text":"a","text":"b"}}`: `the key "text" appears twice`,
		`{"s":"\ud800"}`:                "unpaired UTF-16 surrogate",
		`{"s":"\udc00"}`:                "unpaired UTF-16 surrogate",
		`{"s":"\ud800A"}`:               "unpaired UTF-16 surrogate",
		`{"n":1e3}`:                     "with an exponent",
		`{"n":9007199254740993}`:        "outside what every reader holds exactly",
		`{"n":01}`:                      "leading zero",
		`{"n":1.}`:                      "fraction needs a digit",
		`{} {}`:                         "text after the document",
		"{\"s\":\"\xff\"}":              "not UTF-8",
		"{\"s\":\"raw\x01\"}":           "must be escaped",
		`{"s":"\x"}`:                    `\x is not an escape`,
		strings.Repeat("[", 70) + strings.Repeat("]", 70): "nests deeper",
		`{"a" 1}`: "followed by a colon",
		`NaN`:     "does not begin a value",
	} {
		if _, err := promptsign.Canonical([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Canonical(%.40s) = %v, want %q", in, err, want)
		}
	}
}
