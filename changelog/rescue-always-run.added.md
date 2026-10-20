A block's `rescue:` and `always:` run. A `rescue:` runs on each device where a task of its block failed and, when it
runs to its end, answers the failure: the run goes on, and ends `rescued` rather than `complete`, with each answered
task reporting `rescued_by`. An `always:` runs on every device the block ran on, whatever happened. A rescue task reads
the failure it answers as `error` (`error.task`, `error.message`, `error.unreached` and the rest). `pleiades run
--fail-on-rescue` ends a rescued run with exit status 4, so a gate never passes through a fallback. In a check a
rescue task is reported `conditional_skip` and is not counted as a gap. Before this, both were accepted and never run:
a failed block ran no rescue, and an `always:` cleanup was skipped while the run ended complete. Every task inside a
block with `rescue:` or `always:` runs on the runbook's hosts; `pleiades validate` refuses `error` outside a rescue,
and a register only the block's own tasks write when a later task reads it without handling its absence.
