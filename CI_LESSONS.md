# CI lessons

The rules drawn from failures in `make pr-check` and in GitHub Actions, each with the
[FAILURE_PATTERNS.md](FAILURE_PATTERNS.md) entries behind it. Read them before writing a test that starts a
container, opens a port, waits on timing, or touches the file system, and before opening a pull request.

The failures themselves are counted by `tools/failures`, in two local ledgers: one for `make pr-check` runs
(written by every run) and one for GitHub Actions runs (`go run ./tools/failures record-actions -pr N`).
`go run ./tools/failures report` maps the crossover. A test that fails only in Actions is a hole in the
pre-push gate. One that fails only in `pr-check` is this machine's environment. One called contention run
after run needs a look, not another rerun.

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
   and confirm absence with `Lstat`. FAILURE_PATTERNS 544, 567, 615, 616, 628, 635.
4. **A pass of `pr-check` is a pass of every leg at one commit on a clean tree.** A file left in the tree (even
   a moved, ignored one under a new name) costs the receipt; an unreachable machine costs the leg. A machine
   check makes room from its own trees and build cache before it refuses a full disk; what it refuses then is
   not its to remove. Clear the cause and run again rather than pushing around it. LESSONS_LEARNED 271.

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
