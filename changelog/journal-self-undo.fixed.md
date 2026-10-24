Fixed `pleiades journal` hiding that a run undid its own changes: `journal list` now says `undid its own change in
the run` (and `undone_in_run` in `--json`) for a run whose block's `rollback_on_failure` ran, and `journal show`
names the task each undo step undid instead of printing an undo of the undo, which no rollback runs.
