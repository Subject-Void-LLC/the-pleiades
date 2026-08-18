Added `pleiades run --verbose`, which prints each task's own output (stdout, exit status,
diffs) instead of only whether it changed. Previously a successful task's output was
printed nowhere, and making a task fail was the only way to read a device's answer back.
