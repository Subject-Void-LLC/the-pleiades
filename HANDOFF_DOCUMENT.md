# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Phase-118-Verification-For-Others`, 2026-09-30.** Phase 118 was pushed at `c1a673f1`
after `make push-gate` passed with no tolerated failure (1 h 5 min). The user then said "knock out open items";
that work (`02d764b5` to `9455e4a9`) passed `make push-gate` with no tolerated failure (1 h 4 min) and is
pushed. This handoff is committed on top and travels with the dependency branch's gate. A second branch,
`chore/dependency-updates`, is stacked on this one in the worktree `../auto-roboto-deps`. `gh` is not
authenticated here, so no PR could be opened or CI read. Rules unchanged (method-as-key runbooks, two agents at
most, heavy commands under `~/.local/bin/capped`, the lab provisioned by The Pleiades only).

### What was done

- **Before the push:** the PR's `coverage` job would have failed on every run, because the `ec2` and `s3`
  floors were recorded with LocalStack and CI has no token. LocalStack tests now stop through
  `testsupport.LocalStackToken` (`Require("localstack")`), `testgate` records each package's missing
  requirements beside its coverage, and `coverage-check -measured` names such a floor as unchecked rather than
  failing it (FAILURE_PATTERNS 406). The view reachability test listed 8 of 22 views and now reads them from
  source (407). The Vault gate waited on a log line before Docker forwarded its port (408). 33 packages got
  floors or exclusions.
- **Open items, after the push:** `TestCheckCmdEnvReads`, cited by a Phase 75 item but never written, now
  exists (409); `tools/doctor` is tested against described machines (410); every machine-dependent skip in
  the six packages left without floors goes through `Require`, and all six have floors; `testsupport.ForGreeting`
  (and `SSHGreeting`) reads a server's first line through the mapped port, and thirteen log-only container waits
  use it or an equivalent; the tracker's stale citations are fixed (0 items cite a missing test or file).
- **Found by seeding the conformance fixtures (a subagent):** schedule pages were ordered by name and resumed
  by id, so following Next skipped schedules in the view and `GET /schedules` (411, fixed, and a deleted cursor
  is now a 400); the Access list linked every row to its team and none to its grant, and the drill-down test
  skipped it (412, fixed, with a registration check and a test that now fails instead of skipping).
- **Dependencies (`chore/dependency-updates`):** 21 direct modules bumped (x/crypto 0.57, grpc 1.84,
  testcontainers 0.44, moby api 1.56, aws sdk, nats.go 1.54 and others); build and `make vet` passed.
  `cel-go` 0.32 moved its module path to `cel.dev/cel-go`: imports rewritten and `go.mod` tidied, not yet built.
  Nothing on that branch is committed. GO-2026-5932 (`x/crypto/openpgp`) stays as a module-level notice: nothing
  imports it and it has no fix; go-git already uses the ProtonMail fork.

### Open

1. **`chore/dependency-updates`:** build the cel move, run the tests, commit, rebase onto the Phase 118 tip
   (it was cut at `cbb9362b`), gate, push both branches, PR against the Phase 118 branch. See the final
   message for how far this got.
2. **The first real CI run** needs a pull request, which closes Phase 118's Release Gate item.
3. Three views' readers ignore paging (`credential-types`, `credentials`, `projects`): a design decision, and the
   paging conformance test now says so in its skip. `fakeRepository.GetGroup` in the UI harness ignores
   `After` and `Limit`.
4. About the test containers: the Ansible gate's sshd publishes no port and keeps its log wait by design.
5. Carried: 117a's env-gated ServiceNow gate; Phase 110's strict `make ci`; Phase 113's Pattern Entry Gate;
   Phases 12 and 70 have no Implements line.

### Decisions for the user

1. Open the PRs: Phase 75 (pushed), Phase 118, then the dependency branch stacked on 118.
2. Optional secrets: `LOCALSTACK_AUTH_TOKEN` (CI then requires LocalStack; check LocalStack's terms for CI use)
   and a read-only `DOCKERHUB_TOKEN`/`DOCKERHUB_USERNAME`.
3. Once macOS `fast` and `winrm` pass on GitHub, make them required.
4. Whether the three non-paging views should page.

### Files changed (since the Phase 118 push)

`pkg/winrmexec` tests; `tools/doctor/{main.go,checks_test.go}`; `Require` in `internal/{backup,setup,loader}`
and `tools/testimages` tests; `internal/testsupport/{greeting.go,natsready.go,sshd.go}`; the container waits in
`internal/transport/ssh`, `internal/catalog/file`, `pkg/netconf`, `cmd/runner`, `cmd/pleiades`;
`internal/schedule/ent_store.go`, `internal/api/schedules.go`; `internal/ui/resources/grants/grants.go`,
`internal/ui/view/view.go`, the conformance suite and its new `seed_more_test.go`; `coverage-floor.json`;
CONTRIBUTING.md; two changelog fragments; FAILURE_PATTERNS 409 to 412. Local only: the roadmap's stale citations.
