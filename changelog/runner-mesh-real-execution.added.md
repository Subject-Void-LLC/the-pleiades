A job dispatched to the Controller and picked up by a `runner` now executes against the
real device it names, over SSH, instead of returning a simulated result. Collection
methods run in a per-task child process, so the credential a method needs is passed on
standard input rather than through a command line or an environment variable.

Two limits apply. The Controller attaches a device's credential to the dispatch message,
so a secret is present in the message broker's storage until that message ages out; set
your broker retention accordingly. Credential storage is still the same encrypted local
file the CLI uses, with no rotation, Vault, or PFX support yet.

Added `net.ssh.ping`, which runs a harmless command against any SSH-reachable device and
reports the reply, useful for confirming a device is genuinely reachable end to end.
