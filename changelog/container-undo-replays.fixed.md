A `container.*` task's recorded undo replays again. Every container undo names the engine it ran on, a parameter
`container.remove` and its kin declare through a shared documentation fragment, and the run journal read only a
method's own parameters, so it counted the engine as undeclared and marked each such undo incomplete:
`pleiades rollback` refused to replay `container.run`'s removal of the container it created, and a block's
`rollback_on_failure` refused it too. The journal now reads a method's parameters as `pleiades validate` and the
rollback planner do, its own and its fragments' alike.
