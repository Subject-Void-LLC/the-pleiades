A runbook may use YAML anchors, aliases and merge keys (`<<: *common`): they are expanded before the runbook is read,
where a runbook with an alias used to fail with "got a YAML node of kind 16". Two merge keys in one mapping are refused,
since YAML readers disagree on which one wins, and an anchor kept under a top-level key of its own is told where to go.
