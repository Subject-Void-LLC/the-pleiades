# CI lessons

The rules drawn from failures in `make pr-check` and in GitHub Actions, each with the
[FAILURE_PATTERNS.md](FAILURE_PATTERNS.md) entries behind it. Read them before writing a test that starts a
container, opens a port, waits on timing, or touches the file system, and before opening a pull request.

The failures themselves are counted by `tools/failures`, in two local ledgers: one for `make pr-check` runs
(written by every run) and one for GitHub Actions runs (`go run ./tools/failures record-actions -pr N`).
`go run ./tools/failures report` maps the crossover. A test that fails only in Actions is a hole in the
pre-push gate, and the report says when pr-check did not test its package at that commit (the 631 shape). One
that fails only in `pr-check` is this machine's environment. One called contention run after run needs a look,
not another rerun. `go run ./tools/failures watch-pr -pr N` follows a pull request's checks and records its run
when they finish. No FAILURE_PATTERNS title carries a `[ci]` marker, so the ledgers, not the titles, say which
failures CI found.

Append a rule when a diagnosis teaches one; name the failure patterns it comes from. Never renumber.

## Before the pull request

1. **A change to a shared package's behavior needs its consumers' tests too.** `pr-check` runs the packages a
   branch changes (and a fixed census), not the packages that import them, so a consumer's assertion about the
   old behavior breaks only in CI. Until the scope selects reverse dependents, look for them yourself
   (go_symbol_references) and run them. FAILURE_PATTERNS 559, 631.
2. **`tests/e2e` runs only in CI.** A change that grows what the images build from (source, test data, a new
   top-level directory) is measured there against the build context's ceiling. Keep what no build reads out of
   the context rather than raising the ceiling. FAILURE_PATTERNS 630.
3. **A file-system assumption is a Windows bug until the Windows leg says otherwise.** Mode bits, "not found",
   symbolic links and path case all answer differently there; read the answer the way that system keeps it,
   and confirm absence with `Lstat`. A `Mode().Perm()` comparison gets its Windows answer when it is written.
   FAILURE_PATTERNS 544, 567, 615, 616, 628, 635, 636.
4. **A pass of `pr-check` is a pass of every leg at one commit on a clean tree.** A file left in the tree (even
   a moved, ignored one under a new name) costs the receipt; an unreachable machine costs the leg. A machine
   check makes room from its own trees and build cache before it refuses a full disk; what it refuses then is
   not its to remove. Clear the cause and run again rather than pushing around it. `make pr-check LEGS=bsd` runs
   one part again and keeps the rest; every record is stamped with its commit and tree, so the receipt still needs
   them all at one commit on a clean tree. LESSONS_LEARNED 271.
14. **A new leg runs once for real, through the command `pr-check` will use, before `pr-check` counts on it.** A
    leg's stand-in tests prove its parts; only the real machine shows which binary, OS build and tool version they
    meet. A leg drives its machine with a build its own run made, named by the check's own identity, never a
    literal tool name in shared code. FAILURE_PATTERNS 526, 675, 676.
16. **An edit to a file that holds a waived gosec finding runs `make gosec` before the commit.** A waiver is keyed by
    file and line, so lines added above the waived call move the finding, and the gate stops on an unwaived finding
    and a stale waiver. Review the waiver's reason again when you move it. FAILURE_PATTERNS 685.
17. **A lab VM kept only for checks is cleaned before every run, not refused.** Runs killed when a gate is
    interrupted leave Go's work and test folders behind, and that is the normal case. The check clears the login's
    Go build cache and those folders before each run (this refines 4 for such machines); a machine someone else
    uses is still left alone. FAILURE_PATTERNS 537, 688.

## Tests that do not fail by luck

5. **Never release a port and use it again.** Another process takes it in between; hold the listener, or ask
   Docker for the mapping. `internal/archtest` refuses the released-port shape. FAILURE_PATTERNS 123, 478, 601,
   607, 622.
6. **A timing assertion needs a margin no scheduler stall on the slowest leg reaches:** seconds, not a small
   ratio of milliseconds. The 2 GB Windows VM and a loaded CI runner stall goroutines for hundreds of
   milliseconds. FAILURE_PATTERNS 632.
7. **A goroutine choosing between "the context ended" and "the call is over" can pick either when both are
   ready.** Stop the watcher before the call returns (`context.AfterFunc` and its stop, or a stop that waits),
   and when one such watcher is fixed, sweep every watcher of that shape. A race like this fails once in CI and
   never in hundreds of local runs; reproduce it by delaying the goroutine (one CPU), not by loading the
   machine. FAILURE_PATTERNS 352, 633.
15. **A test of a protection has a control that shows the hazard without it, and every new case is seen to
    fail.** A fake that never answers can let a case pass by accident; an edit made by a script asserts its anchor
    matched before it writes. FAILURE_PATTERNS 436, 671, 679.
18. **A test asks for the capability it needs, not for a sign of it.** Skip on `docker info` failing and `docker
    compose version` failing, not on a missing `docker` binary; read Go's race detector platforms, not
    `CGO_ENABLED`. Installing a tool on a check machine changes which tests run there, and every proxy check is
    then wrong in a new way. FAILURE_PATTERNS 502, 683, 684.
19. **A test comparing two timings takes each side's fastest of interleaved rounds.** An average of calls in a row
    puts one stall on one side. A sweep for this class searches by shape (two measured durations compared), not by
    the names the last fix touched. FAILURE_PATTERNS 422, 687.
20. **A test that runs make clears what a parent make exports.** `MAKEFLAGS` and its kin reach the child through
    `go test` when the gate itself was started by make, and change what the child prints and does. FAILURE_PATTERNS
    686.

## Docker Desktop on this machine

8. **Each Docker step a fixture leans on has its own transient failure under load**: the credential helper
   during a build or pull, port publishing, a published port forwarded through the wrong network, a container
   slow to report ready. Retry each on its own named error and nothing else; a failure that passes alone is
   contention only until it repeats. FAILURE_PATTERNS 61, 575, 601, 622, 629.
9. **Clean Docker only when nothing is using it**, and never the cached test images or named volumes (the
   docker-clean skill and the docker-cleaner agent hold the procedure).

## Reading a failure

10. **Read why it failed before calling it contention**: the first failure's own output, not the verdict line.
    The same test re-run alone can pass for a real race (633). Ask whether it has failed this way before
    (the ci-triager agent matches FAILURE_PATTERNS; `tools/failures report` counts recurrences).
11. **A required check that fails because another job did (`coverage` after a shard) has no cause of its own.**
    Fix the job it waits on.
12. **A fixture git ignores exists only in the tree that wrote it.** The machine legs test the committed tree, so
    they find it before CI does; `internal/archtest`'s `TestNoTestdataFileIsIgnored` refuses one under any
    `testdata` directory. Name a captured log `.txt`, or add an exception. FAILURE_PATTERNS 401, 634.
13. **A failure whose record holds no cause is unexplained, not contention.** The run record keeps 400 lines of a
    failure's output because a test that logs its own teardown buries its assertion far from the end. When the
    record still holds only teardown, run the test alone with its full output before naming a cause, and record
    what that run showed. FAILURE_PATTERNS 414, 668.
21. **When dozens of tests in one package fail with ordinary-looking errors, read the package's first failure.** A
    shared fixture built once (behind a `sync.Once`) reports its cause only in the first test, and every later test
    fails on what the fixture left out. FAILURE_PATTERNS 682.
22. **A build failure with no compiler output is unexplained, not real and not contention.** Record what the
    machine had free when it happened, then build the package alone on the same tree before naming a cause.
    FAILURE_PATTERNS 537, 690.
23. **A container test that leans on the host runs on a native engine before the pull request.** Docker Desktop is a VM;
    CI's runners are not. Host networking, the Docker socket, namespaces, PAM and cgroups behave differently, and
    pr-check does not run container packages by default. After `make pr-check` and `git push`, `make
    branch-containers` runs the branch's changed container packages on the runners (a dispatch of CI's
    branch-containers job, receipt in `.git/pleiades-branch-ci.json`); open the pull request only when it passes. It
    covers the container packages the branch changes, not the ones that only import a changed package (FAILURE_PATTERNS
    631 is still open), and nothing reads the receipt yet: the rule is remembered, not enforced. A change that narrows what pr-check selects names what it still runs, and keeps `PR_CHECK_ALWAYS`. FAILURE_PATTERNS 426, 446, 538, 755, 756; LESSONS 365.
