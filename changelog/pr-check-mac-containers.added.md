When a Mac is configured, `make pr-check` runs 18 container packages (NATS, Postgres, sshd and telnetd fixtures) on the
Mac's Podman, beside the rest here, and records them as their own `containers-mac` part. `go run ./tools/maccheck
-containers` runs that half alone; it starts the Podman machine when it is stopped, removes every container before and
after, and stops a machine it started.
