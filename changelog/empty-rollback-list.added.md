A task's `rollback: []`, an empty list, now says undoing the task means doing nothing: a command that only read, or
a model call that changed no device. `pleiades rollback` runs nothing for it and reports it as `nothing to undo` (and
in `--json` as `nothing_to_undo`), a block's `rollback_on_failure` runs no undo for it and goes on to its rescue, and
`pleiades validate`'s `reversible` and `rollback_on_failure` rules take it as the task's answer. Before, an empty list
was read as no list at all, so such a task was refused as one that could not be undone. A list taken out of the runbook
since the run is still not an empty one.
