// Package playbook: running a conversion end to end and assembling its
// report.
package playbook

import (
	"fmt"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
)

// run converts root, a parsed playbook, into runbooks and a report.
func (t *translator) run(root *yaml.Node) (Result, error) {
	if root.Kind != yaml.SequenceNode {
		return Result{}, fmt.Errorf("%s is not a playbook: a playbook is a list of plays", termsafe.EscapeLine(t.file))
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return Result{}, err
	}
	stem := strings.TrimSuffix(path.Base(t.file), path.Ext(t.file))
	runbooks := t.translatePlays(root, stem)
	result := Result{Report: Report{SchemaVersion: SchemaVersion, Playbook: termsafe.EscapeLine(t.file)}}
	for i, rb := range runbooks {
		out, err := t.emitRunbook(rb, i+1, stem, eval)
		if err != nil {
			return Result{}, err
		}
		result.Runbooks = append(result.Runbooks, out.runbook)
		result.Report.Runbooks = append(result.Report.Runbooks, out.result)
		for _, task := range out.tasks {
			result.Report.Tasks = append(result.Report.Tasks, *task)
		}
	}
	result.Report.Findings = t.findings
	result.Report.Resolutions = t.res.resolutions()
	t.count(&result.Report)
	return result, nil
}

// count fills the report's counts and its list of tasks that cannot take
// part in check mode.
func (t *translator) count(r *Report) {
	r.CannotCheck = []string{}
	for _, task := range r.Tasks {
		r.Counts.Tasks++
		switch task.Outcome {
		case OutcomeBlocked:
			r.Counts.Blocked++
		case OutcomeReview:
			r.Counts.Review++
			r.Counts.Converted++
		default:
			r.Counts.Converted++
		}
		switch task.Class {
		case ClassAsserted:
			r.Counts.Asserted++
		case ClassComputed:
			r.Counts.Computed++
		case ClassImperative:
			r.Counts.Imperative++
		case ClassObserve:
			r.Counts.Observe++
		}
		if task.CanCheck != nil && !*task.CanCheck {
			r.Counts.CannotCheck++
			r.CannotCheck = append(r.CannotCheck, task.Runbook+" "+task.NodeID)
		}
	}
}
