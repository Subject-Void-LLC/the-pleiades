# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-14. Branch `feature/tier-a-collections`, local, not pushed**, cut from `chore/pr-check-receipt`'s tip
(itself unpushed, off PR #55). The owner's goal is a pasted Tier A plan: **Phase 0**, a verification sweep of the
git/forge, container-engine and Kubernetes code against a standard (device model, reversibility, check mode,
secrets, injection, output, context, plan coverage, docs and counts), fixing every gap as its own commit, then
**stop for the owner's reaction**; **Phase 1** (gate.*, build.*, db.pg.*, scan.*, ai.*, edge.* and small methods)
starts only after that, with a design note in docs/ shown first. **Phase 0 is done and Phase 1 has not started.**
Rules from that goal on top of the usual ones: no push or PR unless asked; never edit coverage-floor.json,
gosec-waivers.json or flaky-packages.json to make a failure disappear; never write the owner's application's name
anywhere (its paths and namespace included).

### Phase 0, what the sweep found

- **Git and forges: nothing exists.** No `git.*`/`scm.*` method or client. Part XIX (120a to 120k) plans it; it was
  written this session (`.SPECIFICATION/IMPLEMENTATION.md`, gitignored).
- **Kubernetes: the foundation, no methods.** `pkg/kubeapi`, the cluster device, credentials, import and onboarding
  exist; no `k8s.*` method (119g/119h, all open).
- **Container engine: four methods.** `container.docker.exec` met the standard; `run`, `stop` and `remove` had real
  defects, now fixed.

### Commits this session (on `25296e33`)

`eff5b82d` option injection in the docker CLI methods (an image of `--privileged` ran a privileged container);
`921047c8` a failed start removes what it created, undo pinned to docker's own id; `43f7ad70` bounded, escaped
docker output (`remoteexec.RunWithStdinBounded`, `remoteexec.Excerpt`); `a1c248f7` the CLI methods act only on the
pinned engine; `26c89fe2` registry accounts masked on both tiers (`sdk.SecretRegistrar`); `93dc18e3` a release gate
planting docker run's secret; `42ec10bf` bounded Podman cleanup, tests off the local daemon; `cab66aef` a lent
Kubernetes credential stays inside its organization (security finding, the owner chose to fix); `ca204d1f` a
refused server address is never echoed; `a0d6c4fb` Kubernetes guards and tests; `f5512ff5` a token as a project
URL's user is a credential, withheld (security finding, pulled forward from 120a); `a7b36716` README and start-here
counts, tested; `85c12698` FAILURE_PATTERNS 450 to 462, LESSONS 275 to 277; `e6029671` the k3s fixture waits on its
mapped port; `180f6e85` a line-keyed gosec waiver moved with its code (same finding, reason extended); `a710f808`
coverage of the new failure paths. Each fix's test was watched failing on the old code.

### Gates

- `make ci-remote`: passed at `180f6e85` (first run failed only on the moved waiver).
- `make pr-check`: passed at `a710f808` (31 container-free, 13 container packages; 42 measured, none below floor);
  receipt `.git/pleiades-pr-check.json`. The first run failed coverage only, fixed by `a710f808`. This handoff's
  commit sits after the receipt, so a push needs `make pr-check` again.
- Live, on this machine: real Docker 29.8, the DinD and Podman fixtures, k3s (both minors), the CLI release gates
  and the Walk-tier exec; all pass, one k3s port race fixed in the fixture. GitHub, GitLab and Bitbucket were not
  reached (nothing exists to test).

### Listed for the owner, not fixed (contract changes)

1. Refuse `engine_local` on a Runner (CLI-only until 119l).
2. Per-method default timeouts for the docker methods (a long pull could start failing).
3. A cap on every SSH method's output (`remoteexec.Conn.Run` is still unbounded; only the docker methods use the
   bounded form).
4. `container.docker.run` defaults of no network, read-only mounts and digest pinning (planned for 119e's
   `container.run` instead).
5. `container.docker.run` leaves a stopped container stopped (its documented contract; 119e's `container.run` starts it).
6. Kubernetes session clients get per-response bounds with 119g's first method (a shared total would end watches).
7. A cluster token is pattern-masked in client-go's log, not registered as a literal.
8. `kubeapi.bound` and `containerengine.truncate` could move onto `remoteexec.Excerpt`.
9. The Ansible path's `K8S_AUTH_API_KEY` environment variable is a deliberate AWX-parity exception, masked.

### Next

1. The owner's reaction to the sweep table. Then Phase 1: write the `gate.*` design note in docs/ (method list,
   capabilities, check and reversibility table, failure modes) and show it before building.
2. The missing families (119e, 119f, 119g, 119h; Part XIX) are the owner's to schedule. Dogfooding this repo's own
   git flow (branch, commit, push, PR, checks, merge) needs 120a, 120c and 120f with only the GitHub adapter;
   `pleiades adhoc ... exec.command cmd="git ..."` works today on a host with git, with that host's own
   credentials and no check or undo, and MCP adds a caller, not methods.
3. Carried: require PR checks on `main`; branch cleanup; PR #55 and `chore/pr-check-receipt` still waiting on merge.

### Files changed

`internal/catalog/container/docker/`, `pkg/containerengine/`, `pkg/remoteexec/{bounded,excerpt,conn}.go`,
`pkg/sdk/secrets.go`, `internal/engine/{runbook_context,collection_action}.go`, `internal/adapters/native/`,
`internal/credstore/resolve/`, `internal/inventory/{ent_device_organization.go,onboard/}`, `internal/dispatch/`,
`internal/api/` (kubeconfig import, projects), `internal/apispec/`, `pkg/kubeapi/`, `internal/project/`,
`internal/ui/resources/projects/`, `internal/archtest/kubernetes_test.go`, `internal/testsupport/k3s*.go`,
`cmd/controller/kubernetes_borrow*.go`, `cmd/pleiades/` (two tests), `tools/gendocs/statuscounts_test.go`,
`docs/`, `README.md`, `changelog/` (9 fragments), `gosec-waivers.json` (one line moved), the records files.
