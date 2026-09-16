# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Two things are live at once.** `feature/job-cancel` is finished and parked, and the work
list it came off is being continued. Read both halves before touching either.

### Parked: `feature/job-cancel`, 17 commits, off `main` at `227fc9e`, NOT pushed

Item I, job cancel, built end to end, plus the `running` job state and the result
aggregation that state had to stand on. `make push-gate` was run to completion and both
test phases pass. The only red is two AWS coverage floors that fall because
`LOCALSTACK_AUTH_TOKEN` is unset, and neither package is touched by the branch.

The full writeup is the top entry of `HANDOFF_ARCHIVE.md`: the two corrections to the
plan's own sizing, the subscription bug the branch's own test found before it shipped, and
the gate blind spot that `tools/coverage-check` cannot tell a skipped test from an untested
package. Five things were left as decisions rather than improvised, and they are what a
return to this branch is for:

1. A dogfood pass, which the suite cannot substitute for (LESSONS_LEARNED #174).
2. The legacy container adapter's cancellation is unverified. The native SSH path is proven.
3. A job can now get stuck in `running`, and nothing reaps it. The one gap this work
   introduces rather than inherits.
4. Nothing consumes dead letters, which is FAILURE_PATTERNS #217's shape a second time.
5. A job whose every device failed still ends `completed`. Inherited rather than
   introduced, and decidable now that per-device results exist.

The branch is a clean fast-forward from `main` and awaits a push and PR decision.

### The list this is working through, and where each item actually stands

This list has lived only in conversation until now, which is why an item's stated size has
twice turned out to be wrong in a way nothing recorded. Each row below carries what was
first claimed and what the code says today, with the anchor that settles it.

| Item | What it is | First sized | Verified state |
|---|---|---|---|
| B | Section write path, row half | S | Header half shipped. No row half exists at all: `view.Row` carries `ID`, `Cells` and `Refs`, and nothing that acts on one. |
| C | Survey builder | S add / M edit | Model is complete and persisted. UI is read-only. The edit half needs B, which the first note had backwards. |
| E | Tasks tab and Download | M | `internal/journal` is real and no API endpoint exposes it. Download has no route shape to reuse. |
| F | Users: password reset, team display | M | `internal/apispec` declares no password endpoint of any kind. No team-member port. |
| G | Inventory Sources | M | New entity plus both dialects' migrations. D's runner and history pattern is reusable. |
| H | Execution envs, instance groups, max hosts | L | Both UI resources exist at `view.StatusDeclared`. The heartbeat is a file, not a registration. |

**B, in detail, because it is next.** `view.Section` already carries `Actions []string`: a
section names one of its parent record's `RecordAction`s and renders a button that acts on
the record, which is how "Add input" reaches a credential type
(`internal/ui/resources/credentialtypes/inputs.go`). There is no equivalent for a row of
that section, so an input can be added to a credential type and then never removed, and the
same is true of an injector and of a survey question. Removing needs no form at all and is
the shippable half. Editing in place needs a `RecordAction` whose form can be prefilled from
an existing row, which is a `view` package change rather than a resource one, and is the
real blocker the original note was pointing at.

**C's dependency is the opposite of what was recorded.** The first note said the survey
builder "needs A's endpoint and relation pattern, not B". That is true of adding a question
and false of editing one: `launch.Survey` holds an ordered list of questions, so editing,
reordering and deleting one are all row operations and all wait on B.

**E grew rather than shrank.** The journal half is a query against a real package. The
Download half has nowhere to live: the only per-record route beyond the resource's own is
`/{resource}/{id}/logs`, fixed once for every resource that declares a `StreamSpec`
(`internal/ui/view/view.go`). A download is a second such route or an API endpoint, and
which one it should be is a decision rather than a detail.

### Next step

Item B, the section write path's row half, on its own branch off `main`.
