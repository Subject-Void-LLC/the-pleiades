# Webserver lab: three real Ubuntu targets for capturing real output

This directory is a small, real Docker lab used to capture genuine `pleiades` terminal
output for `docs/`, instead of writing example transcripts by hand. It is documentation
infrastructure, not part of the shipped product, and not something a user runs.

```
examples/webserver_lab/
  docker/
    docker-compose.yml
    plain/              baseline linux_server: SSH only, nothing installed
    nginx-bootstrap/    standard "install and enable a webserver" pattern
    wordpress/          LAMP-stack CMS pattern: Apache + PHP + MariaDB + WordPress
  pleiades/
    inventory.yaml       the three lab hosts, added with the real `pleiades add-host`
    runbooks/
      baseline_check.yaml
      nginx_check.yaml
      wordpress_check.yaml
  captures/              real, captured `pleiades` output from real runs against these hosts
```

## Why three patterns, not one

A single "SSH server with a package installed" target proves the transport works, but
says nothing about what a runbook author actually writes for a real host. These three
cover the common shapes:

- **plain**: a freshly onboarded server before any provisioning, the state `add-host`
  and `validate` examples should show.
- **nginx-bootstrap**: the classic "install a package, enable the service, confirm it
  answers" pattern.
- **wordpress**: a stateful, multi-service host (a database plus a web server plus an
  application), where a check has to be specific to the real service, not a generic
  `systemctl is-active`. See the note below: one of this lab's own checks needed fixing
  for exactly that reason.

## Try it

```bash
cd examples/webserver_lab/docker
docker compose up -d --build

# Trust each container's SSH host key once, the same way a real operator
# would trust a real device's key on first connect. These lab containers
# bake in a fixed host key (docker/*/keys/) so this only needs doing once.
for port in 2221 2222 2223; do
  ssh-keyscan -t ed25519 -p "$port" 127.0.0.1 >> ~/.ssh/known_hosts
done

cd ../pleiades
pleiades add-credential plain --username svc-netauto --password pleiades-lab-2026
pleiades add-credential nginx-bootstrap --username svc-netauto --password pleiades-lab-2026
pleiades add-credential wordpress --username svc-netauto --password pleiades-lab-2026

pleiades validate runbooks/baseline_check.yaml
pleiades run runbooks/baseline_check.yaml
pleiades run runbooks/nginx_check.yaml
pleiades run runbooks/wordpress_check.yaml
```

`inventory.yaml` and the three hosts in it were produced by the real `pleiades add-host`
command, not hand-written; see `captures/` for what `pleiades run` actually printed
against each of the three real containers.

## A real finding, kept rather than smoothed over

`wordpress_check.yaml`'s MariaDB check originally read
`systemctl is-active mariadb || service mariadb status`, the same shape that works for
the nginx-bootstrap host's nginx check. Against the wordpress host it failed for real:

```
tasks[0] [...]: FAILED: task tasks[0] (name "Confirm MariaDB is active") failed: fqcn "ssh_exec" on device "wordpress": command exited 3
stdout:  * MariaDB is stopped.

stderr: bash: line 1: systemctl: command not found
cat: /run/mysqld/mysqld.pid: Permission denied
```

Not a Pleiades bug: these lab containers have no `systemctl` (no systemd as PID 1), and
the sysvinit `mariadb status` script's PID-file check fails under the non-root service
account these lab hosts authenticate as. The fixed runbook checks `mysqladmin ping`
instead, the same check the container's own startup script already uses. Kept here,
including the failed transcript in `captures/wordpress_run_failed_example.txt`, because
it is a genuine, unscripted example of what `pleiades run` prints on a real failure:
stdout and stderr both come through, masked through `credential.Mask` like everything
else the CLI prints.

## Rebuilding from scratch

The three SSH host keys under `docker/*/keys/` are fixed and checked in, generated once
for this lab and never rotated, so `ssh-keyscan` only needs running once even across a
`docker compose up --build`. They authenticate nothing outside these throwaway
containers.
