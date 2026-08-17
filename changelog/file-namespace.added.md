Five `file.*` methods are implemented: `file.directory`, `file.touch`, `file.permissions`,
`file.remove` and `file.symlink`. Each reads the device's state before acting and changes only
what differs, so a second run against a converged device reports no change and sends no command
for the part that was already right. Each also records what it found and what it left, under an
Ansible-shaped `diff` stat, and declares what would undo it.

Parameter names are Ansible's throughout. Two deliberate divergences, both documented on the
method's own reference page. `file.remove` refuses to delete a non-empty directory unless the
task sets `recurse`, where `ansible.builtin.file` with `state: absent` recurses without asking,
because a recursive delete should be readable in the runbook that asks for it. And a mode must be
a quoted octal string, since an unquoted `0755` is the number 493 by the time a module sees it,
and a symbolic mode like `u+rwx` cannot be compared against what the device reports, so it would
report changed on every run.

`file.remove` declares itself irreversible rather than naming an inverse. Nothing journals the
content it deletes, so nothing can put it back, and an inverse that recreated an empty file where
a full one had been would be worse than none. It still records the full prior state so a rollback
can say precisely what it cannot restore.
