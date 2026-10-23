Added `--extra-vars` and `-e` to `pleiades validate`, read exactly as `run` reads them, and both commands now
refuse a runbook whose task parameter reads `vars.<name>` that no `--extra-vars` gives, naming the variable and
the task, before any device is contacted rather than when the task renders. A read guarded by `| default(...)` is
optional and is not refused, and a task with a `when:` of its own is left to the run, since its condition may
skip it.
