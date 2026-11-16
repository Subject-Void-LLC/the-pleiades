// Package gnmi_test: net.gnmi.config's answer, from a call alone, to whether
// it records the undo its manifest declares.
package gnmi_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestConfig_UndoCallAnswersFromTheDeletes refuses a call naming a delete,
// which records no undo, and a delete written as a template, and leaves an
// update or a replace, which records one for a create, to the run.
func TestConfig_UndoCallAnswersFromTheDeletes(t *testing.T) {
	d, _ := collection.Lookup("net.gnmi.config")
	if d.UndoCall == nil {
		t.Fatal("net.gnmi.config has no UndoCall")
	}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"update":       {update("/a", 1), ""},
		"no deletes":   {map[string]any{"delete": []any{}}, ""},
		"a delete":     {map[string]any{"delete": []any{"/a"}}, "a delete records no undo"},
		"a template":   {map[string]any{"delete": "{{ vars.gone }}"}, "delete is a template"},
		"not template": {map[string]any{"delete": "/a"}, ""},
	} {
		if got := d.UndoCall(tc.params); (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: UndoCall = %q, want %q", name, got, tc.want)
		}
	}
}
