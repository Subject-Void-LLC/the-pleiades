`exec.shell` is implemented: it runs a command line through a real shell on the target, so a
pipe, a redirect, a glob, a variable expansion or a chain of commands behaves the way it would
if you typed it. It shares `chdir`, `creates`, `removes` and `stdin` with `exec.command`, because
Ansible's shell module is the same module with one flag set and the same argument spec. Choose
the shell with `executable`, which otherwise comes from the device or falls back to `/bin/sh`.

Every implemented method now also declares what would undo it, as a `collection.Inverse` on its
manifest, and the generated reference page for each shows it under "Undoing this". Registration
refuses an implemented method that declares nothing, one that claims to be irreversible without
saying why, or one whose named inverse is not a registered method. **Nothing performs a rollback
yet.** This is recorded now rather than later because a real inverse needs the prior state, and
only the forward run is in a position to record it.
