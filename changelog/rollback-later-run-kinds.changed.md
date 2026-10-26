A rollback refused over a later run now says which kind of run it is, with its own advice: one with changes of its own to
undo (roll it back first), one whose changes recorded no undo (read it, then step over it), or one whose runbook says none
of its changes needs undoing (`rollback: []`, now recorded in the journal). `--json` gives each problem's `kind`. A refusal
whose undo needs a runbook edited since the run opens with the `--runbook <file>` hint. In `pleiades rollback --mode
check`, a step on a device an earlier step of the same rollback changes first is provisional: a failed check of it is
reported unchecked with the reason, not failed.
