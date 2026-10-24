---
name: docker-clean
description: Clean up Docker on a developer machine (Docker Desktop, often under WSL) without breaking a test run or throwing away the cached test images. Use when a container test fails on Docker itself (a published port that never answers, "error getting credentials", "no space left", a reaper that will not start), when docker system df shows tens of GB reclaimable, or when the user asks to clean, prune or reset Docker. Reports what it found and what it freed.
---

# Cleaning Docker here

A developer machine here usually runs Docker Desktop, often under WSL; `.IGNORE/agents-local.md`, when it exists,
names this one and its limits. The test suites (`make pr-check`, `go test -tags integration`) start hundreds of
containers a day through testcontainers, and Docker Desktop wears under that:
unused volumes and build cache pile up, published ports stop being forwarded, and the credential helper fails now and
then. Cleaning is safe only when nothing is using Docker, and only for what nothing uses.

## 1. Refuse while anything is using Docker

Check first, and stop if any of these is true; say what is running instead of cleaning:

- a gate or test run is live: `pgrep -af 'make pr-check|make push-gate|make ci|go test|testgate'`;
- containers are running: `docker ps --format '{{.Names}} {{.Image}} {{.Status}}'`. Testcontainers' own reaper
  (`testcontainers/ryuk`) running means a test process is alive or just ended; wait a minute and look again.

A prune while a test runs deletes the volumes and networks it is using, and the run fails in ways that look like
the code's fault.

## 2. Measure

```bash
docker system df
docker ps -a --format '{{.Names}} {{.Status}}' | head -50
docker network ls --format '{{.Name}} {{.Driver}}'
```

## 3. Clean what nothing uses, in this order

```bash
docker container prune -f        # stopped containers only
docker network prune -f          # networks no container uses (testcontainers leaves some)
docker volume prune -f           # anonymous volumes no container uses (named volumes are kept by default)
docker builder prune -f          # build cache; tagged images (pleiades/gns3:..., test images) keep their layers
docker image prune -f            # dangling, untagged images only
```

Then `docker system df` again, and report each line's before and after.

## 4. Never, unless the user asks for it by name

- `docker image prune -a`, `docker system prune -a`, or removing tagged images: the test images listed by
  `tools/testimages` are cached on purpose. Pulling them again costs an hour and Docker Hub's anonymous rate limit.
- `docker volume prune -a` or removing named volumes: a named volume can hold someone's data.
- Stopping or restarting Docker Desktop, or `wsl --shutdown`: it ends every container and can take WSL down.
- `pkill -f` anything.

## 5. Docker's own transient failures, and what to do instead of cleaning

- `error getting credentials - err: exit status 1`: Docker Desktop's credential helper failed, not the build or
  pull. For a public image, run the command with an empty config: `DOCKER_CONFIG=$(mktemp -d)` and
  `echo {} > $DOCKER_CONFIG/config.json`. Never put a registry token in that file.
- A published port that never answers ("sent no SSH greeting", "connection refused" on a mapped port): Docker
  Desktop's port forwarding under load (FAILURE_PATTERNS 575's family). Rerun the one test alone first; clean only if
  it fails alone too.
- `no space left on device` inside a container or a build: clean as above, then check
  `docker system df` again; Docker Desktop's disk lives in its own VM, so the host's `df` does not show it.

## 6. Report

Say what was running (or that nothing was), each `docker system df` line before and after, what was pruned, and
what was kept on purpose. If you refused, say why and what would have to end first.
