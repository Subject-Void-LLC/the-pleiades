// This file holds repeatedPatchField to its contract on bodies the strict
// decode in front of it never lets through, so its early returns are known
// to report no repeat rather than a false one.
package api

import (
	"reflect"
	"testing"
)

// TestRepeatedPatchField_ReportsNoRepeatForAnUnreadableBody checks the scan
// gives up quietly on bytes that are not one JSON object, and still finds a
// repeat in one that is.
func TestRepeatedPatchField_ReportsNoRepeatForAnUnreadableBody(t *testing.T) {
	type fields projectPatchDTO
	shape := reflect.TypeOf(fields{})

	for name, body := range map[string]string{
		"empty":               ``,
		"a key with no name":  `{5:1}`,
		"a key with no value": `{"name":`,
	} {
		if field, repeated := repeatedPatchField([]byte(body), shape); repeated {
			t.Errorf("%s: reported %q repeated", name, field)
		}
	}
	if field, repeated := repeatedPatchField([]byte(`{"name":"a","Name":"b"}`), shape); !repeated || field != "name" {
		t.Errorf("a repeated name gave %q, %v", field, repeated)
	}
}
