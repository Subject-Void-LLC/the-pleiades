// Tests of the project-file rule's edges: a task naming a method this
// process does not hold, and a structured parameter given as one map rather
// than a list of maps.
package validate

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestProjectFileRule_PassesOverAMethodItDoesNotHold: a task whose method no
// Collection registered is not this rule's to judge (validation's unknown
// method rule refuses it), so it finds nothing rather than failing.
func TestProjectFileRule_PassesOverAMethodItDoesNotHold(t *testing.T) {
	dag := &engine.DAG{Nodes: map[string]*engine.Task{
		"t": {FQCN: "acme.not_registered", Params: map[string]any{"src": "configs/r1.conf"}},
	}}
	if got := ProjectFileRule(WorldView{DAG: dag, NoProjectDirectory: true}); len(got) != 0 {
		t.Errorf("findings = %+v, want none for a method nobody registered", got)
	}
}

// TestProjectFileEntries_ReadsOneMapAsWellAsAList: a structured parameter
// given as a single map has its entries read at the parameter's own place,
// at any depth, and a field given as null names nothing.
func TestProjectFileEntries_ReadsOneMapAsWellAsAList(t *testing.T) {
	fields := []collection.Param{
		{Name: "src", Format: collection.ParamFormatProjectPath},
		{Name: "inner", Entries: []collection.Param{{Name: "template", Format: collection.ParamFormatTemplateFile}}},
		{Name: "name"},
	}
	value := map[string]any{
		"src":   "configs/r1.conf",
		"inner": map[string]any{"template": "templates/r1.j2"},
		"name":  nil,
	}
	got := projectFileEntries("device", fields, value)
	want := []string{"device.inner.template", "device.src"}
	if !slices.Equal(got, want) {
		t.Errorf("projectFileEntries = %v, want %v", got, want)
	}
}
