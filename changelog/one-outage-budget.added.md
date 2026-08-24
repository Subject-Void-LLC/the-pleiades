One setting now decides how long your deployment can lose its network and still
pick up where it left off: `PLEIADES_MAX_OUTAGE`, or `mesh.maxOutageSeconds` in the
Helm chart, defaulting to thirty minutes. Message retention, duplicate suppression
and the runner's memory of completed work are all derived from it instead of being
three unrelated numbers.

Raising it is safe. Lowering it shortens retention, so the controller refuses to
delete messages to achieve that unless you set
`PLEIADES_MAX_OUTAGE_ALLOW_DISCARD=true`, and it tells you what would be lost.

A dispatch that the controller could not confirm, and therefore reissued, is no
longer executed twice.
