Added `pleiades.builtin.project.write`, which writes text a run produced, such as a router's registered
`running_config`, to a file in the project (`content: "{{ result.r3.running_config }}"`), so a running
configuration becomes a candidate file without copying it out of a `--json` report. The path is held to the
rules every project file is (relative, inside the project, never through a symbolic link, never into
`.pleiades`), the file is replaced atomically, owner-only unless `mode` says otherwise, and the task reports
`changed` only when the bytes or mode differ; a check predicts it. It runs on the command line only. A masked
secret in what it writes is warned about, and `net.cli.config` and `net.netconf.config` now refuse content
holding a `pleiades-masked-` placeholder before reaching the device, as `net.frr.config` already did.
