// Tests of ProjectFiles, the one rule for which calls name a file in the
// project, and of the refusal a run with no project makes of each.
package collection

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// projectDoc is a method with every kind of parameter ProjectFiles reads: a
// project path given, one with a default, a template file, a structured
// parameter whose entries name files at two depths, and one naming nothing.
func projectDoc() Doc {
	return Doc{Params: []Param{
		{Name: "src", Format: ParamFormatProjectPath},
		{Name: "dest", Default: "yang/<device>", Format: ParamFormatProjectPath},
		{Name: "template", Format: ParamFormatTemplateFile},
		{Name: "devices", Entries: []Param{
			{Name: "src", Format: ParamFormatProjectPath},
			{Name: "inner", Entries: []Param{{Name: "template", Format: ParamFormatTemplateFile}}},
			{Name: "name"},
		}},
		{Name: "name"},
		{Name: "empty_default", Format: ParamFormatProjectPath},
	}}
}

// TestProjectFiles_NamesEveryPlaceACallNamesAFile reads each place in the
// doc's order: given, entries sorted within their parameter at any depth (a
// list of maps or one map), a null or absent entry naming nothing, and a
// parameter left to a non-empty default.
func TestProjectFiles_NamesEveryPlaceACallNamesAFile(t *testing.T) {
	got := ProjectFiles(projectDoc(), map[string]any{
		"src":      "configs/r1.conf",
		"template": "{{ vars.t }}",
		"devices": []any{
			map[string]any{"src": "configs/b.conf", "inner": map[string]any{"template": "t.j2"}, "name": nil},
			"not a map",
			map[string]any{"src": "configs/a.conf"},
		},
		"name": "r1",
	})
	want := []ProjectFile{
		{Place: "src"},
		{Place: "dest", Default: "yang/<device>"},
		{Place: "template"},
		{Place: "devices[0].inner.template"},
		{Place: "devices[0].src"},
		{Place: "devices[2].src"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectFiles = %+v\nwant %+v", got, want)
	}
	if got := ProjectFiles(projectDoc(), map[string]any{"src": nil, "dest": "yang/r1", "devices": map[string]any{"src": "x"}}); !reflect.DeepEqual(got,
		[]ProjectFile{{Place: "dest"}, {Place: "devices.src"}}) {
		t.Errorf("a null src, a given dest, one map of devices: %+v", got)
	}
	if got := ProjectFiles(Doc{Params: []Param{{Name: "name"}}}, map[string]any{"name": "x"}); got != nil {
		t.Errorf("a method naming no project file: %+v", got)
	}
}

// TestProjectFile_Refusal words both refusals, each naming the task and the
// place, the defaulted one its default too.
func TestProjectFile_Refusal(t *testing.T) {
	given := ProjectFile{Place: "src"}.Refusal(`tasks[0] (name "copy")`)
	left := ProjectFile{Place: "dest", Default: "yang/<device>"}.Refusal("tasks[1]")
	for got, want := range map[string][]string{
		given: {`task tasks[0] (name "copy") gives src, a file in the project`, "no project directory", "give the content in the runbook instead"},
		left:  {"task tasks[1] leaves dest to its default, yang/<device>, a file in the project", "no project directory", "run it from the command line"},
	} {
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%q lacks %q", got, w)
			}
		}
	}
	if !IsProjectFileFormat(ParamFormatProjectPath) || !IsProjectFileFormat(ParamFormatTemplateFile) || IsProjectFileFormat(ParamFormatCommand) {
		t.Error("IsProjectFileFormat reads the wrong formats")
	}
	if got := projectFileEntries("x", nil, map[string]any{"src": "y"}); !slices.Equal(got, nil) {
		t.Errorf("entries of a parameter declaring none: %v", got)
	}
}
