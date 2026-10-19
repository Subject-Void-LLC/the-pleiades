`pleiades validate` warns when a runbook does not open with a header comment saying what it does: the `#`
lines before the first line of YAML, naming what it does, what it changes and how to undo it. The warning
is printed beside the findings (`[warning] header: ...`, and "1 with warnings" in a many-runbook summary)
and never fails validation. The sample runbook `pleiades init` writes, and every shipped example, has one.
