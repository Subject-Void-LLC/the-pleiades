package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	serialtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serial"
	serialtcptransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serialtcp"
	sshtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/ssh"
	telnettransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/telnet"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialtcp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
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
	// splitPositional rather than fs.Arg(0), for the same reason
	// add-host and the forge subcommands use it: Go's flag package stops
	// parsing at the first non-flag argument, so `run site.yaml
	// --verbose` would silently treat --verbose as a second positional
	// and fail with a usage error naming neither the flag nor why. The
	// runbook path is the thing a person types first.
	runbook, rest, err := splitPositional(args, map[string]bool{"verbose": true, "v": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades run <runbook.yaml> [--verbose] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	verbose := fs.Bool("verbose", false, "print each task's own output (stdout, exit status, diffs), not just whether it changed")
	fs.BoolVar(verbose, "v", false, "shorthand for --verbose")
	if err := fs.Parse(rest); err != nil {
		return err
	}

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

	// credential.NewLazyFileStore defers resolving the AES-256 master key
	// and reading .pleiades/credentials.yaml until a task actually needs a
	// credential, so a "noop"-only runbook never touches disk for it.
	// sshtransport.New's own defaults (Options{})
	// are conservative enough for a first real connection: a fail-closed
	// known_hosts check, bounded retry/backoff, and a per-target circuit
	// breaker (internal/transport/ssh's own doc comments).
	//
	// engine.NewDefaultTransportBindings is the one shared, Registry-backed
	// constructor cmd/runner's own native adapter composition also builds
	// from (Phase 16, Native Go Execution Adapter), so this codebase has
	// exactly one capability-keyed transport-binding table, not two
	// independently maintained copies.
	bindings := engine.NewDefaultTransportBindings(
		sshtransport.New(sshtransport.Options{}),
		serialtransport.New(serialexec.Options{}),
		serialtcptransport.New(serialtcp.Options{}, remoteexec.Options{}),
		telnettransport.New(telnetexec.Options{}, remoteexec.Options{}),
	).All()
	// The chain audit's fqcn-table finding (IMPLEMENTATION.md Phase W3):
	// this map and validate.CapabilityRule's table had drifted before
	// engine.ActionCapability unified them. This check is what stops a
	// future binding from drifting again instead of only sharing the
	// table's initial values.
	if err := engine.CheckActionCapabilityBindings(bindings); err != nil {
		return fmt.Errorf("transport bindings misconfigured: %w", err)
	}

	// One credential store, read by both halves of the chain below.
	//
	// It used to be constructed inline for the transport executor alone,
	// and the Collection executor got engine.NewDeviceRunbookContext,
	// which resolves nothing. That made every credential-needing
	// Collection method unusable from this CLI: net.ssh.ping failed with
	// "no usable authentication method" and net.catalyst.* with "no
	// username secret available", against a device whose credential was
	// sitting in .pleiades/credentials.yaml the whole time. The Crawl tier
	// never had that gap, because the Controller attaches the credential
	// to the dispatch payload.
	credentials := credential.NewLazyFileStore(*dir)

	// A second, independent inventory.Repository from the same on-disk
	// inventory.yaml loadWorld already read (never the []InventoryItem
	// slice loadWorld returned: hopChainInventory needs GetByName and
	// GroupAncestry, which a plain slice cannot answer). Cheap to build a
	// second time: NewFileRepository wraps a path, it does not read the
	// file until asked. fileRepository.GroupAncestry always reports "no
	// hierarchy" (Walk tier's hosts.yaml has no Group/Inventory nesting
	// to walk), so a bastion configured at Device level still resolves
	// end to end here; only a Group- or Inventory-level route needs
	// Crawl tier's ent-backed Repository.
	inventoryPath := filepath.Join(*dir, inventory.DefaultInventoryFilename)
	inventoryRepo := inventory.NewFileRepository(inventoryPath, inventory.NewItemFactory())

	// The executor chain, innermost fallback last: a registered Collection
	// method wins, then a transport-backed legacy fqcn, then the two engine
	// keywords. Ordering matters only in that the Collection registry is
	// consulted first, which is what makes the generated catalog reachable
	// at all; the two layers underneath it are namespaced-free fqcn values
	// the registry has never heard of, so they cannot collide.
	actionExecutor := engine.NewCollectionActionExecutor(
		engine.NewTransportActionExecutor(
			bindings,
			credentials,
			inventoryRepo,
			engine.NewBuiltinActionExecutor(),
		),
		engine.NewCredentialRunbookContext(credentials),
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
			fmt.Printf("  %s: FAILED: %v\n", label, redact.Text(result.Secrets, node.Err.Error()))
		case node.Skipped:
			fmt.Printf("  %s: skipped (%s)\n", label, node.SkipReason)
		case node.Changed:
			fmt.Printf("  %s: changed\n", label)
		default:
			fmt.Printf("  %s: ok\n", label)
		}
		if *verbose && node.Err == nil && !node.Skipped {
			printNodeStats(node.Stats, result.Secrets)
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
				value := redact.Text(secrets, fmt.Sprintf("%v", stats[k]))
				fmt.Printf("%s%s: %s\n", prefix, k, value)
			}
		}
	}
}

// printNodeStats prints one successful node's own output under `run
// --verbose`: every key the action recorded, sorted, masked through this
// run's complete secret set.
//
// It exists because "ok" and "changed" are the whole of what a
// successful run used to say. A task that read a device's OS version, a
// file's mode or a service's state produced that answer, put it in
// Stats, and had it printed nowhere; the only way to see it from the CLI
// was to make the task exit non-zero, because the failure path prints
// the error and a Collection method's error carries its output. Several
// example runbooks and release gates were written that way, and a test
// that has to break something to observe it is testing the wrong thing.
//
// Not on by default. A run over a large inventory would otherwise print
// every device's whole stdout between the plan and the summary, which
// buries the one line saying whether the run succeeded.
//
// A multi-line value is printed under its key rather than beside it,
// because the common case here is exactly that: a captured stdout with
// newlines in it, which as a single line is unreadable and as %q is
// worse.
func printNodeStats(stats map[string]interface{}, secrets []string) {
	keys := make([]string, 0, len(stats))
	for k := range stats {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		value := redact.Text(secrets, fmt.Sprintf("%v", stats[k]))
		if value == "" {
			continue
		}
		if !strings.Contains(value, "\n") {
			fmt.Printf("    %s: %s\n", k, value)
			continue
		}
		fmt.Printf("    %s:\n", k)
		for _, line := range strings.Split(strings.TrimRight(value, "\n"), "\n") {
			fmt.Printf("      %s\n", line)
		}
	}
}

// printTaskList prints tasks in order, indented two spaces per depth.
// Each task prints its Name, or its FQCN if Name is empty, so a task
// always has something readable printed even though Name has no
// uniqueness requirement (see engine.Task's doc comment). A block task
// recurses one level deeper under a "block:" label, and prints Rescue and
// Always, if present, as further labeled sub-sections at that same depth,
// mirroring how Ansible authors read a block/rescue/always task. A
// parallel task recurses the same way under a "parallel:" label; its
// children have no rescue/always of their own (Task.Parallel stays
// Block-only for that, see dag.go).
func printTaskList(tasks []engine.Task, depth int) {
	indent := strings.Repeat("  ", depth)
	for _, task := range tasks {
		label := task.Name
		if label == "" {
			label = task.FQCN
		}
		fmt.Printf("%s%s\n", indent, label)

		childIndent := strings.Repeat("  ", depth+1)
		switch task.Kind() {
		case engine.TaskKindBlock:
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
		case engine.TaskKindParallel:
			fmt.Printf("%sparallel:\n", childIndent)
			printTaskList(task.Parallel, depth+2)
		}
	}
}
