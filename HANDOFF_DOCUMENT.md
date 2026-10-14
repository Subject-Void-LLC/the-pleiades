# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Part-XVIII-Container-Platforms` off `main` (`6b5ff333`), 2026-10-13. Nothing committed
or pushed** (the user has not asked; offer per-phase commits, 119a first). Building Part XVIII (Docker,
Podman, Kubernetes) in full, at the user's direction: "full plan docker/k8s/podman as planned", "full
functions - do not half ass", Podman "adjacent to docker so dont skip it", and "make it work with how that
project does things. do not actually build in that project". The acceptance target is the user's
`/mnt/c/Users/raymo/coupon_clipper` (`k8s/deploy.sh`), **read only, never built, applied or changed**, and its
live `groceries` namespace on Docker Desktop's Kubernetes is never touched: every test uses throwaway
fixtures. Docker Desktop and its Kubernetes are integrated into WSL, so the local paths are first class
(this machine's `/var/run/docker.sock`; the `docker-desktop` kube context, client certificate; node container
`desktop-control-plane`, `kindest/node:v1.36.1`, hidden from `docker ps` but reachable by `docker inspect`, its
machine ID equal to the node's `machineID`). Rules unchanged (method-as-key runbooks, two agents at most,
heavy commands under `~/.local/bin/capped`, no AI credit). Memory is tight (about 2.5 to 3 GB free, other
Claude sessions running): run live fixture tests one package at a time.

### The roadmap (gitignored, local only)

Part XVIII at the end of `.SPECIFICATION/IMPLEMENTATION.md`: 119a to 119m. Fast track in **v0.3.0**: 119a,
119b, 119c, 119e, 119f (Docker and Podman together), 119g, 119h, 119l (local device for the project's
scripts), 119m (the coupon_clipper-shaped deploy, end to end); v0.5.0: 119d, Phase 26, 119k; v0.7.0: 119i,
119j. The preamble maps every step of `deploy.sh` to a method and phase. Backups of each roadmap revision
are in this session's scratchpad only. Read status with `python3 .SPECIFICATION/implementation.py --json
--phase 119a` (and so on).

### Done and verified

- **119a (12 of 14; open: Release Gate, commit message).** `container.docker.exec` reaches the engine the
  device advertises; `docker_endpoint` is refused (finding S1); `container.docker.run` sends env on stdin
  (finding S2).
- **119b (12 of 13; open: Release Gate, commit message).** Evidence on every item in the roadmap. The port
  (`pkg/containerengine`): Docker's own Go client behind one guard, four carriers chosen by one function
  that sessions and `pleiades validate` share (`usableCarriers`, `Route`), identity pinning, an account check
  (`id -un` against the recorded `login_user`), an Engine API floor of 1.41 (measured: Docker 29.9.0 1.56,
  Podman 5.8.7 1.44, Podman 4.9.4 1.41), bounded answers, `ValidName`. `pkg/dockerexec` folded in and deleted
  (`Session.Exec`). Registry credentials end to end. `validate` prints `[route]` lines. Stress: 50 devices x
  10 sessions at once, 0.6 s, 50 pooled logins. Docs: `docs/10`, "Container engines: Docker and Podman".

### Findings this session (each fixed, each worth a FAILURE_PATTERNS or LESSONS entry at the doc ritual)

1. OpenSSH applies `AllowTcpForwarding` to Unix-socket channels and answers every refusal "connect failed".
2. The guard judged a decoded path when a target held an invalid character (fuzz); now canonical only.
3. Closing an SSH exec channel does not stop the remote process: the Podman service outlived `Close`.
4. A test asserted on `ps` in an image with no `ps` and passed vacuously.
5. `container_host` parsed its TLS settings but never exposed them, so a pinned CA was ignored.
6. `trust-host --first-connect` wrote a fixture's key into the real `~/.ssh/known_hosts`; tests use
   `PLEIADES_KNOWN_HOSTS`.
7. **The moby client dials a hijacked request (exec, attach) itself whenever its transport has a TLS config,
   which cloning a default transport gives it, so exec skipped the guard.** HTTP/1-only transport in
   `newAPI`; `TestSession_HijackedRequestsPassTheGuard` fails without it.
8. **Over `engine_stdio` no login was ever pooled:** `Conn.Start` tainted it, and the HTTP transport closes a
   finished request's connection on its own goroutine, after the session released the login. Now a process
   seen to exit (`Wait`) is counted out, and the carrier ends its open processes before release.
9. `testsupport.EngineHostOptions.SSHDConfig` REPLACES the default `AllowTcpForwarding local`; a test that
   only meant to add `MaxStartups` silently turned forwarding off.
10. Two vacuous checks caught by controls: busybox `find` rejects `-size -64M` (the error was swallowed),
    and `-xdev` skipped dind's `/var/lib/docker` volume. A planted canary now proves the disk search finds.
11. The Walk child masks the credential's username like every credential value, so an account refusal
    shows it as `********` (kept: never show secrets).
12. Go 1.26.9 and x/net v0.60.0 (PR #52) and the gate moving to GitHub Actions (PR #53) both merged to
    main; WSL crashed once from a 4G cap during a gopls reload (memory note updated).

### Next

1. Commit 119a and 119b and open a PR to main: under the Actions policy its checks are the Release Gate.
   main is 9 commits ahead (#52, #53); this branch holds the same Go bump uncommitted, identical lines.
2. Then 119c (Kubernetes client, `kubernetes_cluster`, kubeconfig import of `docker-desktop`), 119e, 119f,
   119g, 119h, 119l, 119m; then 119d, 26, 119k; then 119i, 119j.
3. The `winrm` CI job's 401 needs one diagnostic run (proposal in the session's notes); waiting on the user.

### Files changed

See `git status` on the branch (about 90 files): `pkg/{capability,containerengine,remoteexec,
wire,sdk,external}`, `internal/{inventory,catalog/container,credential,credtype,dispatch,engine,adapters/native,
testsupport,forge/catalogdata,clispec,api}`, `cmd/pleiades`, `docs/`, `changelog/`, `Makefile`, `go.mod`.
