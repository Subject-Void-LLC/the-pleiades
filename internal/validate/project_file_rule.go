// Package validate: ProjectFileRule, which refuses a task naming a project
// file where the run has no project.
package validate

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ProjectFileRule refuses, where the run has no project directory
// (WorldView.NoProjectDirectory: a Walk-tier Runner), a task naming a
// project file: a parameter it gives, an entry of a structured parameter it
// gives (net.batfish.check's devices[].src, through collection.Param.Entries),
// or a project path it leaves to its default (net.yang.schema's dest). Which
// calls do is collection.ProjectFiles, the rule a Controller's fan-out holds a
// launched job to as well (internal/dispatch's RefuseProjectFiles), so the two
// tiers refuse the same calls in the same words. Before it, nothing refused
// one before run time: the Runner ran the runbook's earlier tasks and the
// method then answered sdk.ErrNoProjectFiles. On the command line, which has
// the project, it finds nothing.
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
		for _, f := range collection.ProjectFiles(desc.Manifest.Doc, task.Params) {
			findings = append(findings, Finding{RuleName: "project-file", Node: id, Message: f.Refusal(taskLabel(id, task))})
		}
	}
	return findings
}

func init() {
	Register(ProjectFileRule)
}
