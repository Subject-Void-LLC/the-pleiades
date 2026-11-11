// Package dispatch: pinning the compiled runbook a job was prepared from (its
// version and its nodes), which the Controller's facts and journal consumers
// resolve each node's method from (cmd/controller's node resolver).
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ent"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ent/job"
)

// PinGraph records graph on jobID once, under the caller's fence. See
// JobStore.PinGraph.
func (s *entJobStore) PinGraph(ctx context.Context, jobID string, fence int64, graph *NodeGraph) error {
	if graph == nil || graph.Version == "" || graph.Scheme == "" || graph.Digest == "" {
		return nil
	}
	encoded, err := json.Marshal(graph)
	if err != nil {
		return fmt.Errorf("failed to encode the runbook graph of job %s: %w", jobID, err)
	}
	// One conditional write: the fence must still be the caller's, and the
	// job must hold no graph yet, so a graph once pinned is never replaced.
	affected, err := s.client.Job.Update().
		Where(
			job.JobIDEQ(jobID),
			job.FenceEQ(fence),
			job.Or(job.DagVersionIsNil(), job.DagVersionEQ("")),
		).
		SetDagVersion(graph.Version).
		SetNodeGraph(encoded).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to pin the runbook graph of job %s: %w", jobID, err)
	}
	if affected > 0 {
		return nil
	}
	// Nothing matched: the job is gone, another claim holds it, or it was
	// pinned already.
	row, err := s.client.Job.Query().Where(job.JobIDEQ(jobID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("job %s: %w", jobID, ErrJobNotFound)
		}
		return fmt.Errorf("failed to read job %s after pinning its runbook graph: %w", jobID, err)
	}
	if row.Fence != fence {
		return fmt.Errorf("job %s: %w", jobID, ErrFenced)
	}
	return nil
}

// graphChanged returns, for a job that pinned a graph, the reason every
// device it has not dispatched yet is failed when prepared is another graph
// of its runbook (another method on a node, another node, or another
// rollback: list: NodeGraph.Same, the comparison the Runner makes too): one job runs one graph, so devices are
// never dispatched against a runbook edited after the job began. Empty when
// the job pinned no graph, when prepared is the graph it pinned, and for a
// rollback job, which runs its plan's steps held to the version it undoes
// (wire.Rollback.DAGVersion) and pins no graph of its own.
func graphChanged(j *Job, prepared PreparedDefinition) string {
	if j.RollbackOf != "" || j.DAGVersion == "" {
		return ""
	}
	if j.Graph.Same(prepared.Graph) {
		return ""
	}
	return fmt.Sprintf("runbook %q changed after this job started, so its remaining devices were not run against the new version", j.RunbookID)
}
