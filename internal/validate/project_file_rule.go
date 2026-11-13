// Package validate: ProjectFileRule, which refuses a task naming a project
// file where the run has no project.
package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// projectFileFormats are the parameter formats that name a file in the
// project directory: one the run reads or writes (ParamFormatProjectPath)
// and a template the engine reads and renders (ParamFormatTemplateFile).
var projectFileFormats = []collection.ParamFormat{collection.ParamFormatProjectPath, collection.ParamFormatTemplateFile}

// ProjectFileRule refuses, where the run has no project directory
// (WorldView.NoProjectDirectory: a Walk-tier Runner), a task giving a
// parameter that names a project file, or a structured parameter's entry
// that does (net.batfish.check's devices[].src, through
// collection.Param.Entries). Before it, nothing refused one before run
// time: the Runner ran the runbook's earlier tasks and the method then
// answered sdk.ErrNoProjectFiles. On the command line, which has the
// project, it finds nothing.
func ProjectFileRule(world WorldView) []Finding {
	if !world.NoProjectDirectory {
		return nil
	}
	var findings []Finding
	for id, task := range world.DAG.Nodes {
		desc, ok := collection.Lookup(task.FQCN)
		if !ok {
			continue
		}
		for _, p := range desc.Manifest.Doc.Params {
			value, given := task.Params[p.Name]
			if !given || value == nil {
				continue
			}
			var places []string
			if slices.Contains(projectFileFormats, p.Format) {
				places = append(places, p.Name)
			}
			places = append(places, projectFileEntries(p.Name, p.Entries, value)...)
			for _, place := range places {
				findings = append(findings, Finding{
					RuleName: "project-file",
					Node:     id,
					Message: fmt.Sprintf("task %s gives %s, a file in the project, and this run has no project directory "+
						"(a Walk-tier Runner is handed the runbook, not the project): give the content in the runbook instead, "+
						"or run it from the command line", taskLabel(id, task), place),
				})
			}
		}
	}
	return findings
}

// projectFileEntries returns the place of every entry field under place
// that names a project file and is given in value: each map of a list of
// maps, or the map itself, at any depth fields declares.
func projectFileEntries(place string, fields []collection.Param, value any) []string {
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
			if slices.Contains(projectFileFormats, f.Format) {
				places = append(places, here)
			}
			places = append(places, projectFileEntries(here, f.Entries, fv)...)
		}
	}
	slices.SortFunc(places, strings.Compare)
	return places
}

func init() {
	Register(ProjectFileRule)
}
