// Package restconf_test: net.restconf.config's answer, from a call alone, to
// whether it records the undo its manifest declares.
package restconf_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestConfig_UndoCallAnswersFromTheMethod refuses a delete and a patch, which
// never record one, and a method written as a template, and leaves a post or
// a put, which record one for a create, to the run.
func TestConfig_UndoCallAnswersFromTheMethod(t *testing.T) {
	d, _ := collection.Lookup("net.restconf.config")
	if d.UndoCall == nil {
		t.Fatal("net.restconf.config has no UndoCall")
	}
	for method, want := range map[any]string{
		nil:                 "",
		"post":              "",
		"PUT":               "",
		"Delete":            "a delete records no undo",
		"patch":             "a patch merges",
		"{{ vars.method }}": "method is a template",
		7:                   "",
	} {
		params := map[string]any{"path": "ietf-netconf-acm:nacm"}
		if method != nil {
			params["method"] = method
		}
		if got := d.UndoCall(params); (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("method %v: UndoCall = %q, want %q", method, got, want)
		}
	}
}
