A block can undo its own changes when one of its tasks fails: `rollback_on_failure: true` undoes what the block's tasks
completed on that device, newest first, before its `rescue:` starts, so the rescue starts from where the block began.
Each undo is what `pleiades rollback` would run (the task's `rollback:` list, or the undo its method recorded) and is
journaled as a rollback of that task, so a later rollback of the run lists it as undone by the run itself rather than
repeating it; the run's report shows each undo as a task whose `undoes` names the task it undid. `pleiades validate`
refuses the key on a block holding a task it could not undo exactly. If an undo fails, the rescue does not run,
`always:` does, and the run fails with both failures. A block with no rescue still undoes, then its failure goes on as
before; a block that runs as one unit undoes on every device; a check never undoes.
