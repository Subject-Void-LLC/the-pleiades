Added `exec.command`, the first collection method that changes anything on a device. It
runs one command over SSH with no shell interpreting it, so a semicolon or a dollar sign
in an argument is text rather than syntax, and reports the exit status, stdout and stderr.
Set `creates` or `removes` to make a task idempotent: the command does not run at all when
the path they name says the work is already done, and the run reports no change.

Running a collection method from the `pleiades` CLI now resolves the target device's stored
credential. Before this, methods that needed one (`net.ssh.ping` and every `net.catalyst.*`
method) failed with an authentication error from `pleiades run` even when the credential was
already stored.
