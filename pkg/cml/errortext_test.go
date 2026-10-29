// Tests of how a controller's error answer is put into an error: CML's
// indented JSON gives its description, not the first line "{".
package cml

import (
	"strings"
	"testing"
)

func TestStatusError_SaysWhatTheControllerSaid(t *testing.T) {
	for body, want := range map[string]string{
		"{\n  \"code\": 400,\n  \"description\": \"Not enough resources to start node CORE2\"\n}": `"Not enough resources to start node CORE2"`,
		"{\n  \"detail\": \"lab is locked\"\n}":                                                   `"lab is locked"`,
		"{\n  \"code\": 400\n}":                                                                   `"{ \"code\": 400 }"`,
		"plain text\nover two lines":                                                              `"plain text over two lines"`,
		"":                                                                                        `""`,
	} {
		got := (&StatusError{Method: "PUT", Path: "/x", Status: 400, Body: body}).Error()
		if !strings.HasSuffix(got, ": 400: "+want) {
			t.Errorf("body %q: error %q, want it to end with %s", body, got, want)
		}
	}
	long := "{\"description\": \"" + strings.Repeat("a", 400) + "\"}"
	if got := (&StatusError{Status: 400, Body: long}).Error(); !strings.Contains(got, "...") || len(got) > 360 {
		t.Errorf("a long description was not bounded: %d bytes", len(got))
	}
	if got := (&StatusError{Status: 400, Body: "{\"description\": \"bad\\u001b[2Jthing\"}"}).Error(); strings.ContainsRune(got, 0x1b) {
		t.Errorf("a control character reached the error: %q", got)
	}
}
