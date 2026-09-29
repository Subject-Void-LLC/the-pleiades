// The one run pipeline `run` and `adhoc` share: load, validate, open the
// journal, execute, and report. adhoc differs only in where its runbook
// comes from, so it has no execution path of its own.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/journal"
	"github.com/Subject-Void-LLC/the-pleiades/internal/lock"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	serialtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serial"
	serialtcptransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/serialtcp"
	sshtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/ssh"
	telnettransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/telnet"
	winrmtransport "github.com/Subject-Void-LLC/the-pleiades/internal/transport/winrm"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/serialtcp"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/telnetexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// runOptions are the settings run and adhoc share.
type runOptions struct {
	// dir is the project directory.
	dir string
	// mode is what --mode asked for; check_mode in the runbook can narrow it.
	mode collection.Mode
	// forks is how many devices are worked on at once.
	forks int
	// persist keeps one SSH connection per device between its tasks.
	persist bool
	// allowUnchecked names methods whose unchecked tasks do not make a
	// check incomplete.
	allowUnchecked map[string]bool
	// verbose prints each task's own output in the text view.
	verbose bool
	// asJSON prints the report as one JSON document instead of text.
	asJSON bool

	// rollbackOf, set only by `pleiades rollback`, names the run this run
	// undoes; undoes says which node and step of it each of this run's
	// nodes undoes (engine.WithRollback). Empty for an ordinary run.
	rollbackOf string
	undoes     map[string]engine.Undo

	// holdsRunLock says the caller already holds the project's run lock,
	// exclusive, as a rollback does; carryOut then takes none itself,
	// since a second lock on the same file from one process would wait on
	// itself.
	holdsRunLock bool

	// rollback is a rollback's plan, reported with the run, and whose
	// unaccepted unknowns make a run that otherwise completed incomplete.
	rollback *rollbackReport
}

// runSource is the runbook a run carries out.
type runSource struct {
	// label is what the report names it by.
	label string
	// path is a runbook file, or "" when yaml holds the runbook.
	path string
	// yaml is a runbook built in memory, for adhoc.
	yaml []byte
	// selection is --tags and --skip-tags, applied to a file's runbook.
	selection engine.TagFilter
}

// runPipeline carries out src and prints its report, as text or as JSON,
// returning the error the command ends with.
//
// With --json, standard output holds exactly one JSON document whatever
// happens: a runbook that does not load, or a run the executor aborts,
// still ends with a report whose outcome says so, so a program reading it
// never has to tell a failure from an empty answer.
func runPipeline(opts runOptions, src runSource) error {
	rep, err := carryOut(opts, src)
	if !opts.asJSON {
		return err
	}
	if rep == nil {
		rep = &runReport{Runbook: src.label, Mode: opts.mode}
	}
	if err != nil && rep.Outcome.Status == "" {
		rep.Outcome = runOutcome{Status: "error", Message: err.Error(), ExitCode: 1}
	}
	if writeErr := writeJSON(os.Stdout, rep); writeErr != nil {
		return writeErr
	}
	return err
}

// carryOut is runPipeline's work: everything but printing the JSON. It
// prints the text view as it goes, since a long run's plan is worth seeing
// before its results.
func carryOut(opts runOptions, src runSource) (*runReport, error) {
	// External Collections register before anything reads the registry:
	// loading the runbook and validate.Validate both look methods up, and a
	// method from an external program has to be known to them exactly as a
	// built-in one is.
	if _, err := loadExternalCollections(context.Background(), opts.dir); err != nil {
		return nil, err
	}
	items, dag, err := loadSource(opts.dir, src)
	if err != nil {
		return nil, err
	}
	// A runbook carrying check_mode makes the whole run a check, whatever
	// --mode said: the key can only narrow (engine.TaskMode). Settled
	// here, before the journal is opened and the plan printed, so the
	// command behaves exactly as it would for --mode check.
	mode := engine.TaskMode(opts.mode, dag, nil)
	checking := mode == collection.ModeCheck

	world := validate.WorldView{Items: items, DAG: dag, Mode: mode}
	rep := planReport(src.label, items, dag, mode, world)
	if report := validate.Validate(world); report.HasErrors() {
		rep.Validation = validationIssues(report)
		rep.Outcome = runOutcome{Status: "invalid", Message: "validation failed, not executing", ExitCode: 1}
		if !opts.asJSON {
			fmt.Print(report.String())
		}
		return rep, errors.New(rep.Outcome.Message)
	}

	// The project's run lock, shared: runs do not exclude each other, but
	// a rollback, which reads every journal to decide what to undo, is
	// excluded while any run is changing devices (internal/journal's
	// runlock.go). A check changes nothing and writes no journal, so it
	// takes none, and still works in a project directory this user cannot
	// write.
	if !checking && !opts.holdsRunLock {
		release, err := journal.LockRuns(opts.dir, false)
		switch {
		case errors.Is(err, journal.ErrRunsBusy):
			return rep, err
		case err != nil:
			// The lock lives beside the journal, so a project the run
			// cannot journal into fails here first; say so.
			return rep, fmt.Errorf("failed to open the run journal: %w", err)
		}
		defer func() { _ = release() }()
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
	// silently records nothing, so it is deliberately fail-closed. It used
	// to be constructed beside the executor, after the plan and the word
	// "executing:" had already been printed, so a read-only project
	// directory produced a run that announced itself and then abandoned
	// the attempt.
	//
	// A check opens no journal at all. It changes nothing, so it has
	// nothing to record, and the engine refuses to journal a check
	// regardless (engine.WithMode); not opening the store means a check
	// also works in a project directory this user cannot write, which is
	// a reasonable place to ask "what would this do" from.
	var sink engine.Journal
	var store *journal.FileStore
	if !checking {
		store, err = journal.NewFileStore(opts.dir)
		if err != nil {
			return rep, fmt.Errorf("failed to open the run journal: %w", err)
		}
		sink = store
	}

	if !opts.asJSON {
		printPlan(rep)
	}

	executor, closeExecutor, err := newRunExecutor(opts, world, sink, mode)
	if err != nil {
		return rep, err
	}
	defer closeExecutor()

	// A CLI subcommand has no cancellation surface of its own yet, so a
	// fresh background context is used here rather than threading one
	// through every subcommand's flag parsing, mirroring loadWorld's own
	// precedent (load.go).
	result, err := executor.Run(context.Background(), dag)
	sealRun(store, rep, result.RunID)
	if err != nil {
		rep.Outcome = runOutcome{Status: "error", Message: "execution aborted: " + redact.Text(result.Secrets, err.Error()), ExitCode: 1}
		return rep, fmt.Errorf("execution aborted: %w", err)
	}

	rep.Tasks = taskReports(dag, &result, items, opts.allowUnchecked)
	if len(result.Metadata) > 0 {
		rep.Metadata, _ = redact.Value(result.Secrets, result.Metadata).(map[string]any)
	}
	rep.Outcome = outcomeOf(rep, result.HasErrors())
	if opts.rollback != nil {
		rep.Rollback = opts.rollback
		rep.Outcome = rollbackOutcome(rep.Outcome, opts.rollback)
	}
	if !opts.asJSON {
		printResults(rep, opts.verbose)
	}
	return rep, outcomeError(rep.Outcome)
}

// sealRun marks runID's journal complete and records where it is on rep.
// It runs as soon as Executor.Run returns, on the aborted path too, since
// an aborted run still journaled every level it ran; only a process that
// died mid-level leaves no seal, which is what a rollback needs to tell
// the two apart (internal/journal's seal.go).
//
// A seal that cannot be written does not change the run's outcome: the
// devices were already changed, and saying the run failed would be false.
// It is printed on stderr instead, and the run then reads as cut off to a
// later rollback, which is the safe direction to be wrong in.
func sealRun(store *journal.FileStore, rep *runReport, runID string) {
	if store == nil || runID == "" {
		return
	}
	rep.RunID = runID
	if path, err := store.PathFor(runID); err == nil {
		// Absolute, so a path in a --json report still names the file
		// wherever the reader runs from; --dir is usually ".".
		if abs, absErr := filepath.Abs(path); absErr == nil {
			path = abs
		}
		rep.Journal = path
	}
	if err := store.Seal(runID, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v; a rollback will treat this run as cut off\n", err)
	}
}

// loadSource loads the inventory and compiles src's runbook.
func loadSource(dir string, src runSource) ([]pkginventory.InventoryItem, *engine.DAG, error) {
	if src.path != "" {
		return loadWorld(dir, src.path, src.selection)
	}
	return loadWorldYAML(dir, src.label, src.yaml)
}

// outcomeOf is how a finished run ended, counting what a check left
// uncovered. A task an --allow-unchecked method left unchecked is named
// rather than counted, so a pipeline accepts exactly the gaps it named.
func outcomeOf(rep *runReport, failed bool) runOutcome {
	checking := rep.Mode == collection.ModeCheck
	var out runOutcome
	for _, t := range rep.Tasks {
		if t.Status != statusUnchecked {
			continue
		}
		if t.AllowedUnchecked {
			out.AllowedUnchecked = append(out.AllowedUnchecked, taskLabel(t))
		} else {
			out.Unchecked++
		}
	}
	switch {
	case failed && checking:
		out.Status, out.Message, out.ExitCode = "failed", "check failed", 1
	case failed:
		out.Status, out.Message, out.ExitCode = "failed", "execution failed", 1
	case checking && out.Unchecked > 0:
		// A check that could not cover every task ends non-zero. A dry run
		// used as a gate (a pipeline step before a real run) must not read
		// as a pass when part of the plan was never checked.
		out.Status, out.ExitCode = "incomplete", exitIncomplete
		out.Message = fmt.Sprintf("check incomplete: %d task(s) could not be checked, so this check does not cover them (nothing was changed)", out.Unchecked)
	case checking && len(out.AllowedUnchecked) > 0:
		out.Status = "complete"
		out.Message = fmt.Sprintf("check complete: nothing was changed (not checked, as --allow-unchecked allows: %s)", strings.Join(out.AllowedUnchecked, ", "))
	case checking:
		out.Status, out.Message = "complete", "check complete: nothing was changed"
	case out.Unchecked > 0:
		// Validation refuses check_mode on a task that cannot be checked, so
		// this is the backstop for a runbook that reached the engine another
		// way: a task its author wanted only checked was neither checked nor
		// run, and the run must not read as complete.
		out.Status, out.ExitCode = "incomplete", exitIncomplete
		out.Message = fmt.Sprintf("run incomplete: %d task(s) marked check_mode could not be checked, and were not run either", out.Unchecked)
	default:
		out.Status, out.Message = "complete", "run complete"
	}
	return out
}

// outcomeError is the error the command ends with for outcome: none for a
// complete run, exitIncomplete's for an incomplete one, else a plain one.
func outcomeError(outcome runOutcome) error {
	switch outcome.Status {
	case "complete":
		return nil
	case "incomplete":
		return &incompleteError{msg: outcome.Message}
	}
	return errors.New(outcome.Message)
}

// newRunExecutor builds the executor a run uses, and a function that
// closes what it opened (the lock manager and the connection pool).
func newRunExecutor(opts runOptions, world validate.WorldView, sink engine.Journal, mode collection.Mode) (*engine.Executor, func(), error) {
	locks := lock.NewInProcessManager()

	// engine.NewDefaultTransportBindings is the one shared, Registry-backed
	// constructor cmd/runner's own native adapter composition also builds
	// from (Phase 16, Native Go Execution Adapter), so this codebase has
	// exactly one capability-keyed transport-binding table, not two
	// independently maintained copies. sshtransport.New's own defaults are
	// conservative enough for a first real connection: a fail-closed
	// known_hosts check, bounded retry and backoff, and a per-target
	// circuit breaker.
	bindings := engine.NewDefaultTransportBindings(
		sshtransport.New(sshtransport.Options{}),
		serialtransport.New(serialexec.Options{}),
		serialtcptransport.New(serialtcp.Options{}, remoteexec.Options{}),
		telnettransport.New(telnetexec.Options{}, remoteexec.Options{}),
		winrmtransport.New(winrmexec.Options{}),
	).All()
	// This check is what stops a future binding from drifting away from
	// validate.CapabilityRule's table (the chain audit's fqcn-table
	// finding, IMPLEMENTATION.md Phase W3).
	if err := engine.CheckActionCapabilityBindings(bindings); err != nil {
		_ = locks.Close() // an in-process manager has nothing to flush, and its Close never fails
		return nil, nil, fmt.Errorf("transport bindings misconfigured: %w", err)
	}

	// One credential store, read by both halves of the chain below. It
	// defers resolving the master key and reading
	// .pleiades/credentials.yaml until a task needs a credential, so a
	// "noop"-only runbook never touches disk for it. It used to be built
	// for the transport executor alone, which left every
	// credential-needing Collection method unusable from this CLI.
	credentials := credential.NewLazyFileStore(opts.dir)

	// A second, independent inventory.Repository from the same on-disk
	// inventory.yaml the runbook was loaded against: the transport
	// executor's hop chain needs GetByName and GroupAncestry, which a
	// plain slice cannot answer. NewFileRepository wraps a path and reads
	// nothing until asked, so it is cheap to build twice.
	inventoryRepo := inventory.NewFileRepository(filepath.Join(opts.dir, inventory.DefaultInventoryFilename), inventory.NewItemFactory())

	// One SSH connection per device, kept open between that device's
	// tasks and closed when the run ends, unless --persist-connections=false
	// (off for the whole run) or the device's own ladder turns it off
	// (engine.PersistFor). Off at either wins. A nil pool is every task
	// logging in afresh.
	var pool *remoteexec.Pool
	if opts.persist {
		pool = remoteexec.NewPool(remoteexec.DefaultPoolIdle)
	}
	closeAll := func() {
		if pool != nil {
			_ = pool.Close() // closing only ends connections; nothing is left to report on
		}
		_ = locks.Close() // an in-process manager has nothing to flush, and its Close never fails
	}

	// The executor chain, innermost fallback last: a registered Collection
	// method wins, then a transport-backed legacy fqcn, then the two engine
	// keywords. The Collection registry is consulted first, which is what
	// makes the generated catalog reachable at all; the two layers
	// underneath are namespace-free names the registry has never heard of,
	// so they cannot collide.
	actionExecutor := engine.NewCollectionActionExecutor(
		engine.NewTransportActionExecutor(bindings, credentials, inventoryRepo, engine.NewBuiltinActionExecutor()),
		engine.NewCredentialRunbookContext(credentials),
		engine.WithConnectionPool(pool, engine.PersistFor(inventoryRepo)),
		// A method creating a machine (virt.vbox.vm.clone) seeds it with a
		// device's login from the same vault: the public key and a hash of
		// the password for a machine reached over SSH, and the password
		// itself only for one reached over WinRM, whose answer file can
		// hold nothing else.
		engine.WithLoginSeeder(engine.NewCredentialLoginSeeder(credentials, inventoryRepo)),
	)

	executor := engine.NewExecutor(
		world,
		actionExecutor,
		locks,
		event.NewInProcessBus(),
		engine.NewInProcessWorkflowContext(),
		opts.forks,
		engine.WithJournal(sink),
		engine.WithMode(mode),
		// Marks each entry of a rollback with what it undoes; a no-op for an
		// ordinary run, whose rollbackOf is empty.
		engine.WithRollback(opts.rollbackOf, opts.undoes),
		// This command's user may run every loaded program for real, so a
		// check may run their checks too (the simulate-lock rule still
		// keeps them off a device being onboarded).
		engine.WithExternalChecks(true),
	)
	return executor, closeAll, nil
}
