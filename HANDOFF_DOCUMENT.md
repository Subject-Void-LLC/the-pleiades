# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-15. Branch `feature/tier-a-collections`.** `origin` holds Phase 0 up to `a710f808` (pushed 2026-10-14, at
the owner's request); everything after it is local. The owner's goal is a pasted Tier A plan: **Phase 0**, a
verification sweep of the git/forge, container-engine and Kubernetes code, then **stop for the owner's reaction**;
**Phase 1** (gate.*, build.*, db.pg.*, scan.*, ai.*, edge.* and small methods) starts only after that, with a
design note in docs/ shown first. **Phase 0 is done, its seven listed contract changes are decided and built, and
Phase 1 has not started.** Rules from that goal on top of the usual ones: no push or PR unless asked; never edit
coverage-floor.json, gosec-waivers.json or flaky-packages.json to make a failure disappear; never write the
owner's application's name anywhere (its paths and namespace included).

### The seven contract changes (the owner asked for edge cases and pro/con, then the choice built)

Each decided by the owner's rule (the secure option with the most functionality), each its own commit with its
test watched failing on the old code:

1. `0e1d51db` `engine_local` is refused unless the process is the CLI (`containerengine.AllowLocalCarrier`, called
   only by `cmd/pleiades`): a Runner reaching its own host's socket would act on whatever engine the Runner's
   machine runs. `882a637c` made it the one switch for that fact (onboarding had its own, and `make pr-check`
   caught the two disagreeing in a live onboarding test; FAILURE_PATTERNS 464).
2. `16c6a6f5` every docker CLI call has a deadline (30 s queries, 2 min start, 90 s stop, 60 s remove), and a pull
   its own (`pull_timeout`, default 900 s); a deadline's error says the call may or may not have happened.
3. `f6a29649` (breaking) every SSH command's output is bounded by default (`remoteexec.DefaultOutputLimit`,
   64 MiB per stream); past it the command fails, never truncates silently.
4. `1b6a3a88` (breaking) `container.docker.run` joins no network unless `network:` names one (ports with none are
   refused), mounts read-only unless a volume says `rw`, and runs the image ID it resolved (pulling when absent),
   keeping the asked reference as a label. `8e5675c1` split `docker.go` first.
5. `c421317e` (breaking) `run` starts a stopped container (a paused or dead one is refused, naming the fix), and
   `stop` now records an undo (`run` with `start: true`).
6. `4cbcf6f7` a Kubernetes session bounds each answer at 64 MiB, a watch, a followed log and an upgraded
   connection excepted (a total like discovery's would end long watches).
7. `be8a50f5` a cluster's token, client key, the AWS key an EKS token is minted from, and each minted token are
   masked by value in client-go's log on all three tiers (`kubeapi.LogTo(w, redact.Shared().Literals())`); a
   rotated minted token is forgotten, a static one never is.

Plus `0bef18a4`: the gencatalog race. Five tests wrote generated packages into the module tree (a repeat of
FAILURE_PATTERNS 53 and 55); they now build through `go -overlay` (`internal/testsupport/gooverlay`), proven by
running them with the tree read-only.

### Gates

- Phase 0 (`a710f808`): `make ci-remote` and `make pr-check` passed; pushed.
- This round: `make ci-remote` passed at `0bef18a4` (129 s; its first run failed gosec on the new helper's two
  G204s, fixed in that commit as described there). `make pr-check`'s first run (at `f47b2b22`, 1707 s) failed
  `TestKubernetesOnboard_NodesAndRoutes` in isolation, fixed by `882a637c`; its rerun is on the commit carrying
  this handoff, with the result in `.IGNORE/timings.md` and the session's report. Each decision's touched packages passed under
  `-race` on its own, the live k3s session tests and the real-Docker check test included.

### Still listed, not built

- `kubeapi.bound` and `containerengine.truncate` could move onto `remoteexec.Excerpt`.
- The Ansible path's `K8S_AUTH_API_KEY` environment variable is a deliberate AWX-parity exception, masked.
- A minted EKS token's last value stays in the masker for the process's life (bounded by sessions, not refreshes).
- `go vet` does not see generated packages in the forge and scaffold release gates (it needs a real directory).

### Next

1. The owner's reaction to the sweep and the seven decisions; a push of the commits after `a710f808` needs their
   word and a fresh `make pr-check` receipt. Then Phase 1: write the `gate.*` design note in docs/ and show it
   before building.
2. The missing families (119e, 119f, 119g, 119h; Part XIX) are the owner's to schedule.
3. Carried: require PR checks on `main`; branch cleanup; PR #55 and `chore/pr-check-receipt` still waiting on merge.

### Files changed

`pkg/containerengine/local_policy*.go`, `pkg/containerengine/session.go`, `internal/catalog/container/docker/`
(split into `engine.go`, `validate.go`, `runcreate.go`, `image.go`, `start.go`, `deadlines.go`, with tests),
`pkg/remoteexec/{conn,bounded}.go`, `pkg/kubeapi/{bounded,session,logging,config,device}.go` and tests,
`cmd/{pleiades,runner,controller}/main.go`, `cmd/pleiades/{kubeapi_logging,container_docker_run_release_gate,e2e}_test.go`,
`internal/testsupport/gooverlay/`, `internal/archtest/testonly_test.go`, the forge and scaffold release gates,
`tools/gencatalog/main_test.go`, `internal/forge/catalogdata`, `docs/` (reference regenerated, docs/10),
`changelog/` (7 fragments), the records files.
