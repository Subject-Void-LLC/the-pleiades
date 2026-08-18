`file.touch` refuses a mode, owner or group it cannot apply instead of silently ignoring
it. An unquoted `mode: 0600` is a number by the time YAML is done with it, and the task
used to drop it and report success; it now fails with the runbook's mistake named.
