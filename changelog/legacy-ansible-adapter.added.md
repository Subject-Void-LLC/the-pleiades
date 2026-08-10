An unconverted Ansible playbook dispatched through the Runner mesh now really runs: a
fresh ephemeral container executes the playbook against the one device the dispatch
names, and its output is translated into the same job log events a native runbook run
produces.

Two limits apply. The playbook runs against exactly one device per dispatch, not a whole
play's own host list. Events are parsed from the completed run's captured output, not
streamed live task by task; a later phase replaces this with real-time streaming from a
callback plugin.
