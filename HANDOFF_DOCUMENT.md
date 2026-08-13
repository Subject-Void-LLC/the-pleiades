# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Brutalist-UI-Scaffold`. Directive: get the front end to visual/structural
completion first, then build the APIs behind it, and update the plan documents so nothing found
along the way is lost. Everything below is uncommitted, held per standing instruction.**

**Front end: B1, B2 and B3 of `.SPECIFICATION/AWX_PARITY_ROADMAP.md`'s Tranche B, all built and
tested, no shortcuts.** B1 (typed execution fields with per-field prompt checkboxes, replacing the
old union multi-select) needed a real framework addition: `view.Descriptor.FieldsFor` and
`Descriptor.ResolveFormFields`, threaded through `internal/ui/web/resources.go`'s render and bind
paths, mirroring `RecordAction.FieldsFor`'s existing per-record pattern. New file
`internal/ui/resources/templates/defaults.go`. Also renamed the `tags` launch field to `job_tags`
(roadmap Section 1.2's prep step, needed for a lossless AWX import later) and fixed a real,
independently-found bug while in the code: `allow_simultaneous`'s edit-form prefill used `yesNo()`
("yes"/"no") where the checkbox template only renders `checked` for the literal string `"true"`, so
a `true`-valued template silently flipped to `false` on an untouched save (`FAILURE_PATTERNS.md`
#115). B2 (Activity + Last Ran columns) needed a new `dispatch.JobStore.RecentForTemplates`, batching
`ListForTemplate` across a whole list page in one query rather than one per row, since
`Projector[T].Row` has no per-page context to draw on; the Activity badge reads `FailedCount` rather
than trusting `State` alone (see the severed-link finding below). B3 (Labels) registered as the
eighth declared view, same shape as the other seven. Verified by the pre-existing
`editform_conformance_test.go` plus new tests: `internal/ui/resources/templates_defaults_test.go`
(4 tests), `internal/dispatch/worker_targeting_test.go`'s
`TestJobStore_RecentForTemplatesBatchesAcrossManyTemplates`.

**Backend: found two severed links reading the dispatch path end to end, closed the first one's
first hop.** `.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b has the full writeup with file:line
evidence for both; `FAILURE_PATTERNS.md` #116-117 and `LESSONS_LEARNED.md` #105 record them as
findings. In short: `launch.Template.Resolve` has always correctly computed `Resolved.Fields` and
`Resolved.ExtraVars`, and `internal/api/dispatcher.go`'s `LaunchTemplate` read them out of `resolved`
and never referenced them again — every execution field B1's new UI lets an author set was inert.
Closed this session's first hop: `dispatch.Job` gained `Fields`/`ExtraVars` columns (ent schema +
migrations `sqlite/0011` and `postgres/0008`), and `LaunchTemplate` now stamps them, tested end to
end against a real store. **Still open and NOT attempted**: the wire (`pkg/wire.DispatchPayload` has
no field for this yet) and both adapters (`internal/adapters/legacy/adapter.go`'s argv is still
hardcoded — no `--limit`/`--tags`/`--forks`/etc; the native adapter's extra-vars injection point was
not audited). Separately, confirmed but not touched: a job's `state`/tallies describe fan-out
publish outcomes, not per-device execution outcomes, and the Runner already reliably publishes real
per-device results (`internal/runner/wal.go`'s `ResultEntry`, via `topology.ResultSubject`) that
nothing on the Controller side has ever subscribed to — `ResultWAL`'s own doc comment says as much.
Both were sized and left for a dedicated design-then-build pass rather than rushed: they cross a wire
contract with a literal shape assertion and a state-machine design question (what happens if a Runner
never reports back), and attempting either under the time remaining in an already-long session was
judged the likeliest way to reproduce the exact "passed its own tests, still wrong" pattern this
project has been burned by three times.

**Next step.** Run `make ci` (serially; do not run it concurrently with further edits,
`FAILURE_PATTERNS.md` #104). Then the commit message. After that, in the order
`.SPECIFICATION/AWX_PARITY_ROADMAP.md` Section 3b lays out: the wire extension is the smallest next
piece, then the legacy adapter's argv (highest value, since its whole configuration surface is a
command line), then the native adapter, then read PLAN.md Sections 16-17 before starting the
Controller-side result subscriber. B2's own remaining scope (the expandable row summary; a real
multi-badge activity strip, which needs `internal/ui/render/views.templ`'s list-cell rendering
extended to support more than one badge per cell) is written up at the end of Section 3b's session
update, not silently dropped.

**Files changed this session:** `internal/ui/view/{view.go,pagemodels.go}` (FieldsFor seam),
`internal/ui/web/resources.go` (threaded through render/bind), `internal/launch/kinds/playbook/
playbook.go` (+tests) (tags→job_tags), `internal/ui/resources/templates/{templates.go,defaults.go
(new)}`, `internal/ui/resources/{templates_defaults_test.go (new),harness_test.go}`,
`internal/dispatch/{job.go,ent_store.go,ent_store_test.go,worker_targeting_test.go}`
(RecentForTemplates), `internal/ui/resources/labels/labels.go` (new) + `registrars.go`,
`internal/launch/template.go` (RecentJobs/JobSummary), `internal/ent/schema/job.go` +
regenerated `internal/ent/*` + `internal/ent/migrate/migrations/{sqlite/0011,postgres/0008}`
(Job.Fields/ExtraVars), `internal/api/dispatcher.go` (+`dispatcher_template_test.go`) (stamps them),
`tests/parity/fields_job_template.go` + regenerated `GAPS.md`, `.SPECIFICATION/AWX_PARITY_ROADMAP.md`
(Section 3b, new), `FAILURE_PATTERNS`/`FAILURE_PATTERNS_ARCHIVE` (#115-117),
`LESSONS_LEARNED`/`LESSONS_LEARNED_ARCHIVE` (#105).

---

Full session-by-session history (every `## Previous session: ...` and `## Files changed in the ... session` entry) lives in [`HANDOFF_ARCHIVE.md`](HANDOFF_ARCHIVE.md), kept out of this file so it stays cheap to read every session. Read the archive only when you need a specific past session's detail.

When Current Status above is superseded, move the outgoing text into `HANDOFF_ARCHIVE.md` as a new `## Previous session: ...` entry at the top of that file (before its current first entry), then overwrite Current Status here. Never delete a past entry.
