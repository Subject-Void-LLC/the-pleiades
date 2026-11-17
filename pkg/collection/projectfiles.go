// Which of a call's parameters name a file in the project the run belongs to,
// and the refusal a run with no project makes of one. It is the one rule
// internal/validate's project file rule holds a Runner's runbook to and a
// Controller's fan-out holds a launched job to (internal/dispatch), so the
// two cannot disagree about which calls need the command line.
package collection

import (
	"fmt"
	"slices"
	"strings"
)

// ProjectFile is one place a call names a file in the project directory.
type ProjectFile struct {
	// Place is the parameter, or an entry field within one, as a task writes
	// it: dest, devices[0].src.
	Place string
	// Default is the parameter's default when the call leaves it out
	// (net.yang.schema's dest), empty when the call gives it.
	Default string
}

// IsProjectFileFormat reports whether a parameter of format f names a file
// in the project: one the run reads or writes (ParamFormatProjectPath), or a
// template the engine reads and renders (ParamFormatTemplateFile).
func IsProjectFileFormat(f ParamFormat) bool {
	return f == ParamFormatProjectPath || f == ParamFormatTemplateFile
}

// ProjectFiles returns every place a call with params, held to doc, names a
// file in the project, in doc's parameter order: a project-file parameter it
// gives (present and not null, whatever its value: a template names a file
// as surely as a literal does), an entry field it gives at any depth of a
// structured parameter (Param.Entries; net.batfish.check's devices[].src),
// sorted within that parameter, and a project-file parameter it leaves to a
// non-empty Default.
func ProjectFiles(doc Doc, params map[string]any) []ProjectFile {
	var out []ProjectFile
	for _, p := range doc.Params {
		value, given := params[p.Name]
		if !given || value == nil {
			if IsProjectFileFormat(p.Format) && p.Default != "" {
				out = append(out, ProjectFile{Place: p.Name, Default: p.Default})
			}
			continue
		}
		if IsProjectFileFormat(p.Format) {
			out = append(out, ProjectFile{Place: p.Name})
		}
		for _, place := range projectFileEntries(p.Name, p.Entries, value) {
			out = append(out, ProjectFile{Place: place})
		}
	}
	return out
}

// Refusal is the sentence a run with no project directory (a Walk-tier
// Runner, and the Controller that dispatches to one) refuses f by, task
// naming the task.
func (f ProjectFile) Refusal(task string) string {
	const why = "a file in the project, and this run has no project directory (a Walk-tier Runner is handed the runbook, not the project)"
	if f.Default != "" {
		return fmt.Sprintf("task %s leaves %s to its default, %s, %s: run it from the command line", task, f.Place, f.Default, why)
	}
	return fmt.Sprintf("task %s gives %s, %s: give the content in the runbook instead, or run it from the command line", task, f.Place, why)
}

// projectFileEntries returns the place of every entry field under place
// that names a project file and is given in value: each map of a list of
// maps, or the map itself, at any depth fields declares, sorted.
func projectFileEntries(place string, fields []Param, value any) []string {
	if len(fields) == 0 {
		return nil
	}
	var maps []map[string]any
	var at []string
	switch v := value.(type) {
	case map[string]any:
		maps, at = []map[string]any{v}, []string{place}
	case []any:
		for i, item := range v {
			if m, ok := item.(map[string]any); ok {
				maps = append(maps, m)
				at = append(at, fmt.Sprintf("%s[%d]", place, i))
			}
		}
	}
	var places []string
	for i, m := range maps {
		for _, f := range fields {
			fv, given := m[f.Name]
			if !given || fv == nil {
				continue
			}
			here := at[i] + "." + f.Name
			if IsProjectFileFormat(f.Format) {
				places = append(places, here)
			}
			places = append(places, projectFileEntries(here, f.Entries, fv)...)
		}
	}
	slices.SortFunc(places, strings.Compare)
	return places
}
