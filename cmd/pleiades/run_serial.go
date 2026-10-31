// A runbook's serial: on the CLI: the runbook's hosts are split into batches
// (engine.Batches) and the whole DAG runs over one batch at a time, as one run
// that the journal, the report and a rollback all see whole.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// batchResolver answers targets as inner does, limited, among the runbook's
// own hosts, to the batch running now. A device outside the hosts (a lab
// server, a Batfish service a task names) is not batched and resolves as
// always, since serial: says how many of the hosts change at a time.
type batchResolver struct {
	inner   validate.WorldView
	hosts   map[inventory.DeviceID]bool
	current map[inventory.DeviceID]bool
}

// Resolve implements engine.TargetResolver.
func (b *batchResolver) Resolve(target string) []inventory.InventoryItem {
	var out []inventory.InventoryItem
	for _, item := range b.inner.Resolve(target) {
		if !b.hosts[item.ID()] || b.current[item.ID()] {
			out = append(out, item)
		}
	}
	return out
}

// serialPlan is a run's batches and the resolver that runs one at a time, or
// nil for a runbook without serial:.
type serialPlan struct {
	batches  [][]inventory.InventoryItem
	resolver *batchResolver
}

// planSerial splits world's hosts of dag into dag.Serial's batches and returns
// world with its resolver limited to the batch running. A runbook without
// serial: returns world unchanged and no plan.
func planSerial(world validate.WorldView, dag *engine.DAG) (validate.WorldView, *serialPlan) {
	if len(dag.Serial) == 0 {
		return world, nil
	}
	all, _ := engine.ResolveTargets(world, dag.Hosts)
	b := &batchResolver{inner: world, hosts: map[inventory.DeviceID]bool{}}
	for _, item := range all {
		b.hosts[item.ID()] = true
	}
	limited := world
	limited.Resolver = b
	return limited, &serialPlan{batches: engine.Batches(all, dag.Serial), resolver: b}
}

// runBatches runs dag over each batch in turn as one run (RunAs, one run id,
// one numbering), stopping after the first batch with a failure, and returns
// the whole run's result, the error that aborted it, and the warning naming
// the devices of the batches a failure kept from running.
func runBatches(ctx context.Context, executor *engine.Executor, dag *engine.DAG, plan *serialPlan, announce bool) (engine.RunResult, error, *clireport.RunWarning) {
	var result engine.RunResult
	for i, batch := range plan.batches {
		plan.resolver.current = map[inventory.DeviceID]bool{}
		names := make([]string, len(batch))
		for j, item := range batch {
			plan.resolver.current[item.ID()] = true
			names[j] = item.Name()
		}
		if announce {
			fmt.Printf("serial: batch %d of %d: %s\n", i+1, len(plan.batches), strings.Join(names, ", "))
		}
		res, err := executor.RunAs(ctx, dag, result.RunID, result.Sequence)
		if i == 0 {
			result = res
		} else {
			result.Absorb(res)
		}
		if err != nil {
			return result, err, nil
		}
		if res.HasErrors() && i < len(plan.batches)-1 {
			var left []string
			for _, later := range plan.batches[i+1:] {
				for _, item := range later {
					left = append(left, item.Name())
				}
			}
			return result, nil, &clireport.RunWarning{
				Kind: clireport.WarnSerialStopped, When: "end", Devices: left,
				Message: fmt.Sprintf("serial: batch %d of %d failed, so the rollout stopped there: %d device(s) in later batches did not run: %s",
					i+1, len(plan.batches), len(left), strings.Join(left, ", ")),
			}
		}
	}
	return result, nil, nil
}
