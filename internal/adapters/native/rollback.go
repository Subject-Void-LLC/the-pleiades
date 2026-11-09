// Package native: running a rollback dispatch (Phase 40).
//
// A rollback dispatch names the runbook the undone job ran and carries
// this device's steps (wire.DispatchPayload.Rollback). The Controller
// planned and checked them; this Runner checks them again against its own
// copy of that runbook before running any, because the journal rows the
// plan was made from were published by Runners, and a row claiming a node
// ran a method it does not run must steer nothing. The steps then run as
// an ordinary runbook of their own, journaled as a rollback of the job.
//
// A recorded step is also held to the declarations it was recorded under:
// one whose undo was recorded under other declarations than this Runner's
// runs only when the rollback accepted that by name (allow_skew), and the
// Runner logs which declarations it accepted.
package native

import (
	"fmt"
	"log/slog"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/rollback"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// rollbackDAG returns the runbook a rollback dispatch runs, built from its
// steps once each one is held to ran, the runbook the undone job ran, and
// the executor option that journals it as that job's rollback, and a note
// for each step it runs under other declarations than it was recorded
// under, as the rollback accepted by name, which it also logs to logger
// (when one is given) and Execute publishes to the job's log. It refuses
// the whole dispatch on the first step that does not hold, before any
// runs.
func rollbackDAG(logger *slog.Logger, ran *engine.DAG, payload wire.DispatchPayload) (*engine.DAG, engine.ExecutorOption, []string, error) {
	rb := payload.Rollback
	if ran.Version != rb.DAGVersion {
		return nil, nil, nil, fmt.Errorf("this Runner's copy of runbook %q is not the version job %s ran, so it cannot check the rollback's steps against it", payload.RunbookID, rb.Of)
	}
	if len(rb.Steps) == 0 {
		return nil, nil, nil, fmt.Errorf("the rollback of job %s names no step for device %s", rb.Of, payload.DeviceName)
	}
	levels := make([][]rollback.Step, 0, len(rb.Steps))
	var notes []string
	for _, ws := range rb.Steps {
		s := stepFromWire(ws)
		if err := rollback.Verify(ran, s); err != nil {
			return nil, nil, nil, fmt.Errorf("the rollback of job %s does not hold against runbook %q: %w", rb.Of, payload.RunbookID, err)
		}
		// Said where it is known: the Controller cannot see this Runner's
		// declarations, so only here is it known what the name accepted. The
		// note quotes every group, version and digest it names.
		if note := rollback.SkewNote(s); s.SkewAccepted && note != "" {
			notes = append(notes, fmt.Sprintf("node %s: %s", rollback.Printable(s.Node), note))
			if logger != nil {
				logger.Warn("running a recorded undo under other declarations than it was recorded under, as the rollback accepted by name",
					slog.String("job", payload.JobID), slog.String("rollback_of", rb.Of), slog.String("device", payload.DeviceName),
					slog.String("node", s.Node), slog.String("skew", note))
			}
		}
		levels = append(levels, []rollback.Step{s})
	}
	source, undoes, err := rollback.Runbook("rollback-"+rb.Of, levels, false)
	if err != nil {
		return nil, nil, nil, err
	}
	eval, err := engine.NewCELEvaluator()
	if err != nil {
		return nil, nil, nil, err
	}
	dag, err := engine.NewBuilder(eval).BuildFromYAML(source)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("the rollback of job %s does not build: %w", rb.Of, err)
	}
	return dag, engine.WithRollback(rb.Of, undoes), notes, nil
}

// stepFromWire is the planner's step for ws, as the Controller planned it:
// every field the wire carries, the Collection identity and undo digest the
// journal recorded among them and whether the operator accepted its skew by
// name, for the step's checks on this Runner.
func stepFromWire(ws wire.RollbackStep) rollback.Step {
	return rollback.Step{Node: ws.Node, Index: ws.Index, Emitter: ws.Emitter, FQCN: ws.Method, Params: ws.Params, Name: ws.Name, Source: ws.Source,
		Collection: ws.Collection, UndoDigest: ws.UndoDigest, SkewAccepted: ws.SkewAccepted}
}
