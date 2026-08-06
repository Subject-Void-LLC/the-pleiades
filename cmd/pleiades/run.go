package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/credential"
	"github.com/SubjectVoidLLC/the-pleiades/internal/engine"
	"github.com/SubjectVoidLLC/the-pleiades/internal/event"
	"github.com/SubjectVoidLLC/the-pleiades/internal/lock"
	sshtransport "github.com/SubjectVoidLLC/the-pleiades/internal/transport/ssh"
	"github.com/SubjectVoidLLC/the-pleiades/internal/validate"
)

// runRunbook loads the inventory and a runbook, validates them, prints
// the resulting execution plan (the authored pretasks/tasks/posttasks
// tree and what each task targets), and then actually executes it with
// engine.Executor (Part 0 Phase W5).
//
// This is the Walk-tier composition root's own adapter selection for the
// Release Gate Phase W4 left open (HANDOFF_DOCUMENT.md's Phase W4
// session): lock.NewInProcessManager and event.NewInProcessBus are wired
// in here, giving both ports their first real production caller anywhere
// in this codebase. engine.NewTransportActionExecutor is Phase W6's real
// transport-backed ActionExecutor: "ssh_exec" dispatches over a real SSH
// connection (internal/transport/ssh), authenticated via whatever
// add-credential (addcredential.go) stored for the target device, and
// every other fqcn (starting with "noop") still falls through to
// engine.NewBuiltinActionExecutor unchanged, exactly the seam action.go's
// own doc comment named this phase as filling.
func runRunbook(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: pleiades run <runbook.yaml>")
	}
	runbook := fs.Arg(0)

	items, dag, err := loadWorld(*dir, runbook)
	if err != nil {
		return err
	}

	world := validate.WorldView{Items: items, DAG: dag}
	report := validate.Validate(world)
	if report.HasErrors() {
		fmt.Print(report.String())
		return fmt.Errorf("validation failed, not executing")
	}

	fmt.Printf("plan for %s (%d nodes, %d inventory hosts loaded):\n", runbook, len(dag.Nodes), len(items))

	// ServiceEffecting and blast radius are runbook-level, native-only
	// summary facts (see engine.Metadata's doc comment), so they are
	// surfaced once up front rather than per task.
	fmt.Printf("service-effecting: %t\n", dag.Metadata.ServiceEffecting)

	blastRadius := validate.CalculateBlastRadius(world)
	if len(blastRadius.Tiers) > 0 {
		fmt.Printf("blast radius: %d devices, tiers: %v\n", blastRadius.DeviceCount, blastRadius.Tiers)
	} else {
		fmt.Printf("blast radius: %d devices\n", blastRadius.DeviceCount)
	}
	fmt.Println()

	// The interesting structure (block/rescue/always nesting) lives in
	// the retained authored tree, not in the synthesized happy-path
	// Adjacency chain, so the plan is printed by walking that tree
	// directly rather than via engine.TopologicalOrder. Each of the three
	// sections is skipped entirely when empty, matching Ansible's own
	// convention that pretasks/posttasks are optional and tasks alone can
	// carry a whole play.
	if len(dag.PreTasks) > 0 {
		fmt.Println("pretasks:")
		printTaskList(dag.PreTasks, 1)
	}
	if len(dag.Tasks) > 0 {
		fmt.Println("tasks:")
		printTaskList(dag.Tasks, 1)
	}
	if len(dag.PostTasks) > 0 {
		fmt.Println("posttasks:")
		printTaskList(dag.PostTasks, 1)
	}

	fmt.Println()
	fmt.Println("executing:")

	// A CLI subcommand has no cancellation surface of its own yet, so a
	// fresh background context is used here rather than threading one
	// through every subcommand's flag parsing, mirroring loadWorld's own
	// precedent (load.go).
	ctx := context.Background()
	locks := lock.NewInProcessManager()
	defer locks.Close()

	// newLazyCredentialStore (lazy_credential_store.go) defers resolving
	// the AES-256 master key and reading .pleiades/credentials.yaml until
	// a task actually needs a credential, so a "noop"-only runbook never
	// touches disk for it. sshtransport.New's own defaults (Options{})
	// are conservative enough for a first real connection: a fail-closed
	// known_hosts check, bounded retry/backoff, and a per-target circuit
	// breaker (internal/transport/ssh's own doc comments).
	bindings := map[string]engine.TransportBinding{
		"ssh_exec": {
			Capability: engine.ActionCapability["ssh_exec"],
			Transport:  sshtransport.New(sshtransport.Options{}),
			Target:     engine.SSHTarget,
		},
	}
	// The chain audit's fqcn-table finding (IMPLEMENTATION.md Phase W3):
	// this map and validate.CapabilityRule's table had drifted before
	// engine.ActionCapability unified them. This check is what stops a
	// future binding from drifting again instead of only sharing the
	// table's initial values.
	if err := engine.CheckActionCapabilityBindings(bindings); err != nil {
		return fmt.Errorf("transport bindings misconfigured: %w", err)
	}

	// The executor chain, innermost fallback last: a registered Collection
	// method wins, then a transport-backed legacy fqcn, then the two engine
	// keywords. Ordering matters only in that the Collection registry is
	// consulted first, which is what makes the generated catalog reachable
	// at all; the two layers underneath it are namespaced-free fqcn values
	// the registry has never heard of, so they cannot collide.
	//
	// Credentials are resolved per device by the transport layer below.
	// A Collection method receives them through its RunbookContext, which
	// is empty here because no method in the catalog needs a device secret
	// yet: net.catalyst.* authenticates to a controller, and wiring that
	// through is the next thing this chain grows.
	actionExecutor := engine.NewCollectionActionExecutor(
		engine.NewTransportActionExecutor(
			bindings,
			newLazyCredentialStore(*dir),
			engine.NewBuiltinActionExecutor(),
		),
		engine.NewDeviceRunbookContext,
	)

	executor := engine.NewExecutor(
		world,
		actionExecutor,
		locks,
		event.NewInProcessBus(),
		engine.NewInProcessWorkflowContext(),
		0,
	)

	result, err := executor.Run(ctx, dag)
	if err != nil {
		return fmt.Errorf("execution aborted: %w", err)
	}

	for _, node := range result.Nodes {
		label := node.NodeID
		if node.Device != "" {
			label = fmt.Sprintf("%s [%s]", node.NodeID, node.Device)
		}
		switch {
		case node.Err != nil:
			// Masked through result.Secrets: a later task's failure can
			// echo a value an earlier register_mask/secret_mask task
			// marked secret (its own stdout accidentally repeating a
			// generated password, for example), and by the time this
			// prints, Run has already returned the complete secret set,
			// not just whatever publish's own best-effort, in-flight
			// masking knew about when that node's own event went out.
			fmt.Printf("  %s: FAILED: %v\n", label, credential.Mask(result.Secrets, node.Err.Error()))
		case node.Skipped:
			fmt.Printf("  %s: skipped (%s)\n", label, node.SkipReason)
		case node.Changed:
			fmt.Printf("  %s: changed\n", label)
		default:
			fmt.Printf("  %s: ok\n", label)
		}
	}

	if len(result.Metadata) > 0 {
		fmt.Println()
		fmt.Println("metadata:")
		printMetadata(result.Metadata, result.Secrets)
	}

	if result.HasErrors() {
		return fmt.Errorf("execution failed")
	}

	fmt.Println()
	fmt.Println("run complete")
	return nil
}

// printMetadata prints result.Metadata (populated only from "set_metadata"
// tasks, see internal/engine's ActionResult.IsMetadata), sorted by
// register name, then device ID, then key, for deterministic output.
// Every value is masked through credential.Mask using result.Secrets
// before printing: a set_metadata task can echo back a value an earlier
// register_mask/secret_mask task marked secret just as easily as any other
// task's output can.
func printMetadata(metadata map[string]interface{}, secrets []string) {
	registers := make([]string, 0, len(metadata))
	for name := range metadata {
		registers = append(registers, name)
	}
	sort.Strings(registers)

	for _, name := range registers {
		fmt.Printf("  %s:\n", name)

		byDevice, ok := metadata[name].(map[string]interface{})
		if !ok {
			continue
		}
		deviceIDs := make([]string, 0, len(byDevice))
		for id := range byDevice {
			deviceIDs = append(deviceIDs, id)
		}
		sort.Strings(deviceIDs)

		for _, deviceID := range deviceIDs {
			stats, ok := byDevice[deviceID].(map[string]interface{})
			if !ok {
				continue
			}
			keys := make([]string, 0, len(stats))
			for k := range stats {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			prefix := "    "
			if deviceID != "" {
				prefix = fmt.Sprintf("    [%s] ", deviceID)
			}
			for _, k := range keys {
				value := credential.Mask(secrets, fmt.Sprintf("%v", stats[k]))
				fmt.Printf("%s%s: %s\n", prefix, k, value)
			}
		}
	}
}

// printTaskList prints tasks in order, indented two spaces per depth.
// Each task prints its Name, or its FQCN if Name is empty, so a task
// always has something readable printed even though Name has no
// uniqueness requirement (see engine.Task's doc comment). A task with a
// non-empty Block recurses one level deeper under a "block:" label, and
// prints Rescue and Always, if present, as further labeled sub-sections
// at that same depth, mirroring how Ansible authors read a block/rescue/
// always task.
func printTaskList(tasks []engine.Task, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, task := range tasks {
		label := task.Name
		if label == "" {
			label = task.FQCN
		}
		fmt.Printf("%s%s\n", indent, label)

		if len(task.Block) > 0 {
			childIndent := strings.Repeat("  ", depth+1)
			fmt.Printf("%sblock:\n", childIndent)
			printTaskList(task.Block, depth+2)

			if len(task.Rescue) > 0 {
				fmt.Printf("%srescue:\n", childIndent)
				printTaskList(task.Rescue, depth+2)
			}
			if len(task.Always) > 0 {
				fmt.Printf("%salways:\n", childIndent)
				printTaskList(task.Always, depth+2)
			}
		}
	}
}
