# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/survey-builder`, 61 commits, off `main` at `227fc9e`.** Everything below is
committed and the tree is clean.

### What shipped

**The survey builder (item C), with a `file` question type.** Add, edit, remove and reorder
questions from the template's Survey section. The file type carries a two-gate policy for program
content: the deployment must set `PLEIADES_SURVEY_FILE_ALLOW_PROGRAM_CONTENT` AND the question
itself must be marked as accepting it. Neither gate opens anything alone, the deployment variable
is a live kill switch, and a refusal says what the rule is and what it is not, because a refusal
that only says "refused" invites the reading that whatever got through is safe. Content is
classified by allowlist (`internal/launch/fileanswer.go`): not valid UTF-8, a NUL byte or a byte
order mark is binary and refused outright; a `#!` prefix is program text; everything else is inert.

**The Tasks tab and the per-job download chooser (item E).** The run journal had migrations in both
dialects, a subscriber the Controller refuses to start without, and an index declared for exactly
this query, and nothing had ever read it. The structural caveat is on the page rather than buried:
only the native adapter journals, so a playbook job has no entries and its detail lives in the log
stream instead. That asymmetry is why Download is a chooser rather than a fixed pair of links.

**The isolation-rerun gate.** `tools/testgate` re-runs a failure alone and lets that decide, and
`tools/coverage-check` now runs the same pass so the two cannot reach opposite verdicts in one
run, which they did. It found a real defect on its second run.

**The gate is now a process and the hook is a receipt check.** See the blocker below for why.

**Twenty-eight adversarial review findings**, across two commits. The worst was silent data loss:
editing any survey question on a deployment that refuses program content cleared
`allow_program_content`, because an absent checkbox and an unchecked one are indistinguishable in
a submission. `view.Values.Declares` now answers what `Get` and `Bool` cannot, which is whether the
form drew the control at all.

### The blocker that ate the back half, now fixed

`git push` failed five times with exit 141, no output, and a gate that printed "all checks passed".
Git opens its connection to the remote BEFORE running `pre-push`, so a twenty minute gate inside
that hook leaves git writing to a socket the remote dropped long ago. FAILURE_PATTERNS #229 has the
detail and the two cheap diagnostics that should have found it on attempt two. `make push-gate` and
`make ci` now end by writing `.git/pleiades-gate.json` through `tools/gatereceipt`, and the hook
reads it back in about a second.

### Next step

`make up`, and Phase 83 behind it. The design is done and verified rather than argued: `${VAR:-}`
resolves empty and succeeds, `.env` auto-loads, shell environment outranks `.env` (which is what
lets the compose release gate pass secrets without writing into a developer's checkout), and
`${VAR:?}` really does break `ps` and `down` and would have broken `tools/breakglass`. All four
were run against a real daemon.

The crux is that `docker-compose.yml` ships a usable `MASTER_ENCRYPTION_KEY` and a `JWT_SECRET`
under a comment calling them throwaway defaults, so a `make up` that wrapped today's file would
make the published-key path the product's front door. The wizard is what makes the target honest,
not decoration on it.

### Known and recorded, not fixed

- Every device creatable through the API or the UI lacks a `host` property, so the dispatcher skips
  it. FAILURE_PATTERNS #232. A seeded demo fleet would have hidden this.
- `jobs.extra_vars` is stored in plaintext; survey answers never reach the log masker; a chunked
  POST body is dropped. All pre-existing and recorded in earlier sessions.
- LESSONS_LEARNED #189, which is about a diagnostic that must be incapable of printing a secret,
  RECURRED this session: an auth token was interpolated into a command line and printed by a
  process listing. The rule was already written down. Writing it down was not enough.
