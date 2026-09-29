// Package native: running a rollback dispatch (Phase 40).
//
// A rollback dispatch names the runbook the undone job ran and carries
// this device's steps (wire.DispatchPayload.Rollback). The Controller
// planned and checked them; this Runner checks them again against its own
// copy of that runbook before running any, because the journal rows the
// plan was made from were published by Runners, and a row claiming a node
// ran a method it does not run must steer nothing. The steps then run as
// an ordinary runbook of their own, journaled as a rollback of the job.
package native

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// rollbackDAG returns the runbook a rollback dispatch runs, built from its
// steps once each one is held to ran, the runbook the undone job ran, and
// the executor option that journals it as that job's rollback. It refuses
// the whole dispatch on the first step that does not hold, before any
// runs.
func rollbackDAG(ran *engine.DAG, payload wire.DispatchPayload) (*engine.DAG, engine.ExecutorOption, error) {
	rb := payload.Rollback
	if ran.Version != rb.DAGVersion {
		return nil, nil, fmt.Errorf("this Runner's copy of runbook %q is not the version job %s ran, so it cannot check the rollback's steps against it", payload.RunbookID, rb.Of)
	}
	if len(rb.Steps) == 0 {
		return nil, nil, fmt.Errorf("the rollback of job %s names no step for device %s", rb.Of, payload.DeviceName)
	}
	levels := make([][]rollback.Step, 0, len(rb.Steps))
	for _, ws := range rb.Steps {
		s := rollback.Step{Node: ws.Node, Index: ws.Index, Emitter: ws.Emitter, FQCN: ws.Method, Params: ws.Params, Name: ws.Name, Source: ws.Source}
		if err := rollback.Verify(ran, s); err != nil {
			return nil, nil, fmt.Errorf("the rollback of job %s does not hold against runbook %q: %w", rb.Of, payload.RunbookID, err)
		}
		levels = append(levels, []rollback.Step{s})
	}
	source, undoes, err := rollback.Runbook("rollback-"+rb.Of, levels, false)
	if err != nil {
		return nil, nil, err
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return nil, nil, err
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML(source)
	if err != nil {
		return nil, nil, fmt.Errorf("the rollback of job %s does not build: %w", rb.Of, err)
	}
	return dag, engine.WithRollback(rb.Of, undoes), nil
}
