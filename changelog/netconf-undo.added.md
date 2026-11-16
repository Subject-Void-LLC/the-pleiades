`net.netconf.config` records the undo of what it creates: a removal built only from the task's own content (its names and
keys), recorded before the edit is sent so a lost answer still leaves it for `pleiades rollback`. A change to what was
there records no undo, since undoing it would keep configuration read from the device in the run journal; the run says
why, and a rollback refuses that task unless it is left or its `rollback:` list says what to do.
