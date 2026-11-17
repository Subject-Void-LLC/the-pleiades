// Tests of the project-file rule's edges: a task naming a method this
// process does not hold, and a project path left to its default. Which calls
// name a project file is collection.ProjectFiles, tested in pkg/collection.
package validate

import (
	"strings"
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

// TestProjectFileRule_ADefaultedProjectPathNamesAProjectFile: a task leaving
// a project path to its default writes into the project as surely as one
// giving it, so a run with no project refuses both; a parameter with no
// default, left out, names nothing; and the command line refuses neither.
func TestProjectFileRule_ADefaultedProjectPathNamesAProjectFile(t *testing.T) {
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{Name: "validatetest.defaulted_dest", Manifest: collection.Manifest{
		Status: collection.StatusDeclared,
		Doc: collection.Doc{Params: []collection.Param{
			{Name: "dest", Type: "string", Default: "yang/<device>", Format: collection.ParamFormatProjectPath, Description: "x"},
			{Name: "src", Type: "string", Format: collection.ParamFormatProjectPath, Description: "x"},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	dag := &engine.DAG{Nodes: map[string]*engine.Task{
		"left":  {FQCN: "validatetest.defaulted_dest", Params: map[string]any{}},
		"given": {FQCN: "validatetest.defaulted_dest", Params: map[string]any{"dest": "yang/r1"}},
	}}
	got := ProjectFileRule(WorldView{DAG: dag, NoProjectDirectory: true})
	byNode := map[string]string{}
	for _, f := range got {
		byNode[f.Node] = f.Message
	}
	if len(got) != 2 || !strings.Contains(byNode["left"], "leaves dest to its default, yang/<device>") || !strings.Contains(byNode["given"], "gives dest") {
		t.Errorf("findings = %+v, want one for the defaulted dest and one for the given one, none for src", got)
	}
	if got := ProjectFileRule(WorldView{DAG: dag}); len(got) != 0 {
		t.Errorf("the command line found %+v, want nothing", got)
	}
}
