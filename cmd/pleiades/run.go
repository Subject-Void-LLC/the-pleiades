package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	serialtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serial"
	serialtcptransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serialtcp"
	sshtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/ssh"
	telnettransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/telnet"
	winrmtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/winrm"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialtcp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// runRunbook loads the inventory and a runbook, validates them, prints
// the resulting execution plan (the authored pretasks/tasks/posttasks
// tree and what each task targets), and then actually executes it with
// engine.Executor (Part 0 Phase W5).
//
// This is the Crawl-tier composition root's own adapter selection for the
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
//
// --mode check turns the run into a dry run (PLAN.md Section 34's check
// mode): every task that can say what it would change does so without
// changing anything, every task that cannot is named as unchecked, no
// journal is written, and the command ends non-zero if anything went
// unchecked. engine.WithMode carries the mode; see internal/engine's
// check.go for the rules it enforces.
// maxForks is the most devices one run works on at once, the same bound
// the runbook launch kind's forks field sets on the Controller.
const maxForks = 1000

func runRunbook(args []string) error {
	// splitPositional rather than fs.Arg(0), for the same reason
	// add-host and the forge subcommands use it: Go's flag package stops
	// parsing at the first non-flag argument, so `run site.yaml
	// --verbose` would silently treat --verbose as a second positional
	// and fail with a usage error naming neither the flag nor why. The
	// runbook path is the thing a person types first.
	runbook, rest, err := splitPositional(args, map[string]bool{"verbose": true, "v": true, "persist-connections": true})
	if err != nil {
		return fmt.Errorf("usage: pleiades run <runbook.yaml> [--mode execute|check] [--tags a,b] [--skip-tags c] [--forks 5] [--persist-connections=false] [--verbose] [--dir .]: %w", err)
	}

	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	modeFlag := fs.String("mode", string(collection.ModeExecute), "execute applies changes; check reports what each task would change and changes nothing")
	verbose := fs.Bool("verbose", false, "print each task's own output (stdout, exit status, diffs), not just whether it changed")
	// allowUnchecked names methods whose tasks may go unchecked without
	// making the check incomplete: they are still listed, so a pipeline
	// accepts exactly the gaps it named and a new one still stops it.
	allowUnchecked := map[string]bool{}
	fs.Func("allow-unchecked", "a method whose tasks may go unchecked without making the check incomplete (repeatable)", func(v string) error {
		if v == "" {
			return errors.New("needs a method name")
		}
		allowUnchecked[v] = true
		return nil
	})
	fs.BoolVar(verbose, "v", false, "shorthand for --verbose")
	persist := fs.Bool("persist-connections", true, "keep one SSH connection per device open between its tasks; =false logs in afresh for every task")
	forks := fs.Int("forks", engine.DefaultMaxConcurrency, "how many devices are worked on at once, 1 to 1000 (Ansible's forks, with the same default)")
	selection := tagFlags(fs)
	if err := fs.Parse(rest); err != nil {
		return err
	}

	// Parsed before anything is loaded, so a misspelled mode costs nothing
	// and, above all, is never treated as execute. ParseMode refuses any
	// value outside the closed set rather than defaulting.
	mode, err := collection.ParseMode(*modeFlag)
	if err != nil {
		return fmt.Errorf("--mode: %w", err)
	}
	if *forks < 1 || *forks > maxForks {
		return fmt.Errorf("--forks must be between 1 and %d, got %d", maxForks, *forks)
	}

	// External Collections register before anything reads the registry:
	// loadWorld and validate.Validate both look methods up, and a method
	// from an external program has to be known to them exactly as a
	// built-in one is.
	if _, err := loadExternalCollections(context.Background(), *dir); err != nil {
		return err
	}

	items, dag, err := loadWorld(*dir, runbook, *selection)
	if err != nil {
		return err
	}
	// A runbook carrying check_mode makes the whole run a check, whatever
	// --mode said: the key can only narrow (engine.TaskMode). Settled
	// here, before the journal is opened and the plan printed, so the
	// command behaves exactly as it would for --mode check.
	mode = engine.TaskMode(mode, dag, nil)
	checking := mode == collection.ModeCheck

	world := validate.WorldView{Items: items, DAG: dag, Mode: mode}
	report := validate.Validate(world)
	if report.HasErrors() {
		fmt.Print(report.String())
		return fmt.Errorf("validation failed, not executing")
	}

	// The run journal, one JSON Lines file per run under
	// <dir>/.pleiades/journal. It records what ran, against what, in what
	// order and with what outcome, and it holds no value that came back
	// from a device, so it needs no key and nothing masks it.
	//
	// Declared as the interface rather than as *journal.FileStore because
	// engine.WithJournal guards a nil interface and deliberately not a
	// typed nil: a *journal.FileStore variable holding nil would pass
	// that guard and panic at the first level barrier.
	//
	// Opened HERE, before a single line of output, and its failure ends
	// the command. A journal the operator cannot write is one that
	// silently records nothing, so it is deliberately fail-closed. That
	// makes where it sits part of the design rather than an accident: it
	// used to be constructed beside the executor, twenty lines after the
	// plan and the word "executing:" had already been printed, so a
	// read-only project directory produced a run that announced itself
	// and then abandoned the attempt. Nothing had actually been executed
	// either way, but the output said otherwise.
	//
	// A check opens no journal at all. It changes nothing, so it has
	// nothing to record, and the engine refuses to journal a check
	// regardless (engine.WithMode); not opening the store means a check
	// also works in a project directory this user cannot write, which is
	// a reasonable place to ask "what would this do" from.
	var sink engine.Journal
	if !checking {
		store, err := journal.NewFileStore(*dir)
		if err != nil {
			return fmt.Errorf("failed to open the run journal: %w", err)
		}
		sink = store
	}

	fmt.Printf("plan for %s (%d nodes, %d inventory hosts loaded):\n", runbook, len(dag.Nodes), len(items))
	if checking {
		fmt.Println("mode: check (tasks report what they would change; nothing on any device is changed)")
	}

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
	if described := describeSelection(dag.Selection); described != "" {
		fmt.Printf("selection: %s\n", termsafe.EscapeLine(described))
	}
	for _, section := range []struct {
		label string
		tasks []engine.Task
	}{{"pretasks", dag.PreTasks}, {"tasks", dag.Tasks}, {"posttasks", dag.PostTasks}} {
		if len(section.tasks) > 0 {
			fmt.Printf("%s:\n", section.label)
			printTaskList(dag, section.tasks, section.label, 1)
		}
	}

	fmt.Println()
	if checking {
		fmt.Println("checking:")
	} else {
		fmt.Println("executing:")
	}

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
		winrmtransport.New(winrmexec.Options{}),
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
	// sitting in .pleiades/credentials.yaml the whole time. The Walk tier
	// never had that gap, because the Controller attaches the credential
	// to the dispatch payload.
	credentials := credential.NewLazyFileStore(*dir)

	// A second, independent inventory.Repository from the same on-disk
	// inventory.yaml loadWorld already read (never the []InventoryItem
	// slice loadWorld returned: hopChainInventory needs GetByName and
	// GroupAncestry, which a plain slice cannot answer). Cheap to build a
	// second time: NewFileRepository wraps a path, it does not read the
	// file until asked. fileRepository.GroupAncestry always reports "no
	// hierarchy" (Crawl tier's hosts.yaml has no Group/Inventory nesting
	// to walk), so a bastion configured at Device level still resolves
	// end to end here; only a Group- or Inventory-level route needs
	// Walk tier's ent-backed Repository.
	inventoryPath := filepath.Join(*dir, inventory.DefaultInventoryFilename)
	inventoryRepo := inventory.NewFileRepository(inventoryPath, inventory.NewItemFactory())

	// One SSH connection per device, kept open between that device's
	// tasks and closed when the run ends, unless --persist-connections=false
	// (off for the whole run) or the device's own ladder turns it off
	// (engine.PersistFor). Off at either wins. A nil pool is every task
	// logging in afresh, which is what a run did before this existed.
	var pool *remoteexec.Pool
	if *persist {
		pool = remoteexec.NewPool(remoteexec.DefaultPoolIdle)
		defer func() { _ = pool.Close() }() // closing only ends connections; nothing is left to report on
	}

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
		engine.WithConnectionPool(pool, engine.PersistFor(inventoryRepo)),
	)

	executor := engine.NewExecutor(
		world,
		actionExecutor,
		locks,
		event.NewInProcessBus(),
		engine.NewInProcessWorkflowContext(),
		*forks,
		engine.WithJournal(sink),
		engine.WithMode(mode),
		// This command's user may run every loaded program for real, so a
		// check may run their checks too (the simulate-lock rule still
		// keeps them off a device being onboarded).
		engine.WithExternalChecks(true),
	)

	result, err := executor.Run(ctx, dag)
	if err != nil {
		return fmt.Errorf("execution aborted: %w", err)
	}

	// unchecked counts the tasks a check could not answer for. They are
	// named one by one below and counted again at the end, because a check
	// that silently passed over them would report a clean result for a
	// plan it never looked at.
	unchecked := 0
	var allowed []string
	for _, node := range result.Nodes {
		label := node.NodeID
		if node.Device != "" {
			label = fmt.Sprintf("%s [%s]", node.NodeID, node.Device)
		}
		switch {
		case node.Unchecked:
			note := ""
			if task := dag.Nodes[node.NodeID]; task != nil && allowUnchecked[task.FQCN] {
				allowed = append(allowed, label)
				note = " [allowed by --allow-unchecked]"
			} else {
				unchecked++
			}
			fmt.Printf("  %s: COULD NOT CHECK (%s)%s\n", label, termsafe.Escape(redact.Text(result.Secrets, node.SkipReason)), note)
			continue
		case node.Err != nil:
			// Masked through result.Secrets: a later task's failure can
			// echo a value an earlier register_mask/secret_mask task
			// marked secret (its own stdout accidentally repeating a
			// generated password, for example), and by the time this
			// prints, Run has already returned the complete secret set,
			// not just whatever publish's own best-effort, in-flight
			// masking knew about when that node's own event went out.
			fmt.Printf("  %s: FAILED: %v\n", label, termsafe.Escape(redact.Text(result.Secrets, node.Err.Error())))
		case node.Skipped:
			fmt.Printf("  %s: skipped (%s)\n", label, termsafe.Escape(node.SkipReason))
		case node.Changed && (checking || node.Checked):
			fmt.Printf("  %s: would change\n", label)
		case node.Changed:
			fmt.Printf("  %s: changed\n", label)
		case node.Checked && !checking:
			// check_mode on this task in a real run: it was asked, not run.
			fmt.Printf("  %s: ok (checked only)\n", label)
		default:
			fmt.Printf("  %s: ok\n", label)
		}
		// Always, verbose or not: a warning is something the person running
		// the work has to act on (sdk.StatWarnings).
		printWarnings(node.Stats, result.Secrets)
		if *verbose && node.Provider != nil {
			fmt.Printf("    provided by: %s (%s)\n", termsafe.EscapeLine(node.Provider.Program), node.Provider.Digest)
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
		if checking {
			return fmt.Errorf("check failed")
		}
		return fmt.Errorf("execution failed")
	}

	if checking {
		fmt.Println()
		// A check that could not cover every task ends non-zero. A dry run
		// used as a gate (a pipeline step before a real run) must not read
		// as a pass when part of the plan was never checked; the tasks were
		// named above, so the fix is visible without rerunning anything.
		if unchecked > 0 {
			return &incompleteError{msg: fmt.Sprintf("check incomplete: %d task(s) could not be checked, so this check does not cover them (nothing was changed)", unchecked)}
		}
		if len(allowed) > 0 {
			fmt.Printf("check complete: nothing was changed (not checked, as --allow-unchecked allows: %s)\n", strings.Join(allowed, ", "))
			return nil
		}
		fmt.Println("check complete: nothing was changed")
		return nil
	}

	fmt.Println()
	// Validation refuses check_mode on a task that cannot be checked, so
	// this is the backstop for a runbook that reached the engine another
	// way: a task its author wanted only checked was neither checked nor
	// run, and the run must not read as complete.
	if unchecked > 0 {
		return &incompleteError{msg: fmt.Sprintf("run incomplete: %d task(s) marked check_mode could not be checked, and were not run either", unchecked)}
	}
	fmt.Println("run complete")
	return nil
}

// printMetadata prints result.Metadata (populated only from "set_metadata"
// tasks, see internal/engine's ActionResult.IsMetadata), sorted by
// register name, then device ID, then key, for deterministic output.
// Every value is masked through redact.Text using result.Secrets
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
				value := termsafe.EscapeLine(redact.Text(secrets, fmt.Sprintf("%v", stats[k])))
				fmt.Printf("%s%s: %s\n", prefix, termsafe.EscapeLine(k), value)
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
		// A value is a device's or a program's output, so anything in it a
		// terminal would act on is shown escaped (termsafe); lines stay
		// lines.
		value := termsafe.Escape(redact.Text(secrets, fmt.Sprintf("%v", stats[k])))
		name := termsafe.EscapeLine(k)
		if value == "" {
			continue
		}
		if !strings.Contains(value, "\n") {
			fmt.Printf("    %s: %s\n", name, value)
			continue
		}
		fmt.Printf("    %s:\n", name)
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
func printTaskList(dag *engine.DAG, tasks []engine.Task, prefix string, depth int) {
	indent := strings.Repeat("  ", depth)
	for i := range tasks {
		task := &tasks[i]
		id := fmt.Sprintf("%s[%d]", prefix, i)
		label := task.Name
		if label == "" {
			label = task.FQCN
		}
		// A task name is runbook text, and a runbook may be converted from
		// someone else's playbook, so it is escaped before a terminal sees
		// it (internal/termsafe).
		note := ""
		if dag.Nodes[id] == nil {
			note = "  (not selected)"
		}
		fmt.Printf("%s%s%s\n", indent, termsafe.EscapeLine(label), note)

		childIndent := strings.Repeat("  ", depth+1)
		switch task.Kind() {
		case engine.TaskKindBlock:
			fmt.Printf("%sblock:\n", childIndent)
			printTaskList(dag, task.Block, id+".block", depth+2)

			if len(task.Rescue) > 0 {
				fmt.Printf("%srescue:\n", childIndent)
				printTaskList(dag, task.Rescue, id+".rescue", depth+2)
			}
			if len(task.Always) > 0 {
				fmt.Printf("%salways:\n", childIndent)
				printTaskList(dag, task.Always, id+".always", depth+2)
			}
		case engine.TaskKindParallel:
			fmt.Printf("%sparallel:\n", childIndent)
			printTaskList(dag, task.Parallel, id+".parallel", depth+2)
		}
	}
}

// exitIncomplete is the status of a check that failed nothing but left
// tasks unchecked, and of a real run whose check_mode tasks could not be
// checked. It is neither success (0), since part of the plan was never
// looked at, nor failure (1), so a pipeline can tell the two apart and
// accept one without hiding the other.
const exitIncomplete = 3

// incompleteError is the error an incomplete run ends with, carrying
// exitIncomplete.
type incompleteError struct{ msg string }

// Error implements error.
func (e *incompleteError) Error() string { return e.msg }

// ExitCode implements exitCoder.
func (e *incompleteError) ExitCode() int { return exitIncomplete }

// printWarnings prints a node's sdk.StatWarnings, one line each, escaped
// and masked like every other value a method reports.
func printWarnings(stats map[string]interface{}, secrets []string) {
	warnings, _ := stats[sdk.StatWarnings].([]string)
	if list, ok := stats[sdk.StatWarnings].([]any); ok {
		for _, w := range list {
			warnings = append(warnings, fmt.Sprint(w))
		}
	}
	for _, w := range warnings {
		fmt.Printf("    WARNING: %s\n", termsafe.EscapeLine(redact.Text(secrets, w)))
	}
}
