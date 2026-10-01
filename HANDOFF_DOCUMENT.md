# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `chore/dependency-updates`, 2026-10-01. Phase 118 is on `main`.** Pull request #48's fourth run
passed every job, and #48 merged, but into the Phase 75 branch: #47 had merged that branch into `main` and
left it in place, so #48 kept it as its base (the repository does not delete a merged branch, and GitHub moves
a stacked PR to `main` only when its base branch is deleted). #49 then carried it from that branch to `main`
(merge commit `59a8f58b`, every job green again). The dependency branch is the last of the stack; it carries
Phase 118 by merge and shows only its own 14 files against `main`. `gh` is now logged in here as
SubjectVoidLLC (version 2.4.0; pass `-R Subject-Void-LLC/the-pleiades`, since the remote's SSH alias is not
recognized, and change a PR body with `gh api -X PATCH`, since `gh pr edit` fails on a retired Projects
query). Rules unchanged (method-as-key runbooks, two agents at most, heavy commands under
`~/.local/bin/capped`, the lab provisioned by The Pleiades only, no LocalStack in the hosted jobs), plus one
made explicit today in `.AGENTS/AGENTS.md`: no AI credit anywhere, including a PR description.

### What the three runs found, and what fixed it

- **Run 1** (FAILURE_PATTERNS 416, 417): every containers shard pulled `65532:65532` as an image;
  `upload-artifact` skips the hidden `.coverage/`; macOS was an advisory leg of the `fast` matrix that
  `coverage` needed; `ubuntu-latest` becomes Ubuntu 26 on 2026-10-19; a failed `testgate` run wrote no
  summary, and the summary carried test output unmasked.
- **Run 2** (418): the upgrade gates asked for `main` by name in a checkout that has one commit and no
  branches (`1a35d0a6`); `test-repeat` was a bare `go test` outside `testgate`, so its failure was unreadable
  (`3070a8f1`, `testgate -repeat`).
- **Run 3** (419, 420, 421): Ubuntu `fast` failed `TestDispatcher_ReleaseGate` in `test-repeat`, because the
  gate's own 5 ms poll loaded every task row through the one SQLite connection the Worker needed (now 250 ms,
  3.2 s on four cores, budget 120 s; `1ec9bd39`). Shard 2's mesh WSS gate read Docker 28's port map, which
  lists the image's EXPOSEd 4222 with no bindings, as a published port (a port with no binding is skipped;
  `5e959438`). macOS `fast` failed 43 tests that assumed Linux (`b947d97a`: the loader requires `landlock`
  wherever `Load` is called, the http tests pin their authority as `tls_ca_pem` because macOS ignores
  SSL_CERT_FILE, facts and onboard require `linux`, remotefile's directory setgid case is marked Linux's, and
  the userns message off Linux says the kernel has none). LocalStack is out of the hosted jobs at the user's
  direction (`c8dfd3cd`).
- **Verified here:** the touched packages under `-race`, the dispatcher gate three times over on four cores,
  the mesh gate against real containers (44 s), `GOOS=darwin go vet` of every touched package, actionlint.
  **Not verifiable here:** the macOS fixes themselves; the fourth run is their test.

### Open

1. **The dependency PR into `main`**, opened after this handoff's gate and push. grpc stays at 1.83.2 (413);
   GO-2026-5932 (`x/crypto/openpgp`) is a module-level notice with no fix and no importer.
2. **Plan the push-gate / release-gate split with the user**, now that the PR is done (their words: "we get
   the pr done then we plan for it"). Proposed and not yet approved: a fast push gate without the container
   packages and the ratchet, the PR jobs as they are, and a strict `make release-gate` (everything `make ci`
   runs plus `image-scan`, a fuzz budget, the upgrade gates and a fresh `govulncheck`) whose receipt the
   pre-push hook requires on the exact commit of any `v*` tag, run again by a release workflow on the tag.
   Raise with it: the coverage re-measure gives a stalled container no second chance where the test pass
   does; a `govulncheck` guard that fails on a vulnerable imported package (Phase 106b); the Node 20
   deprecation on the pinned actions; the setup-go cache tar warning.
3. **Branch cleanup**, planned and not run (the user asked for the plan only): four merged remote branches,
   eleven merged local ones plus a fast-forward of local `main`, the Phase 75 branch now that #49 merged, and a
   superseded worktree another session left in its scratch directory (`wt117a`, 36 uncommitted files from
   2026-09-29, everything in it older than `main`).
4. Carried: 117a's env-gated ServiceNow gate; Phase 110's strict `make ci`; Phase 113's Pattern Entry Gate;
   Phases 12 and 70 have no Implements line; `fakeRepository.GetGroup` in the UI harness ignores `After` and
   `Limit`; the Ansible gate's sshd keeps its log wait by design; three views do not page
   (`credential-types`, `credentials`, `projects`).

### Decisions for the user

1. Require the PR checks on `main`. Its rulesets block deletion and force-pushes and require a pull request,
   but require no status check; `fast-macos` has now passed twice, which was the condition for it.
2. Turn on "Automatically delete head branches", so a stacked PR follows its base to `main`.
3. Two commits already on `main` carry a `Co-Authored-By: Claude` trailer, the newest `8e477beb`
   (2026-09-23). Removing them means rewriting `main`, which its ruleset refuses; left as they are.
4. Whether the three non-paging views should page.

### Files changed (since the second run)

`internal/api/dispatcher_{testutil,release}_test.go`; `tests/e2e/{previous_build_test.go,
previous_build_ref_test.go,mesh_wss_release_gate_test.go}`; `tools/testgate/{options,main,gate_live_test}.go`
and the `Makefile` (`-repeat`); `internal/loader/*_test.go`; `internal/catalog/http/request_device_test.go`;
`internal/catalog/facts/gather_check_test.go`; `internal/inventory/onboard/probe_ssh_test.go`;
`pkg/remotefile/predict_test.go`; `internal/testsupport/require.go`; `.github/workflows/ci.yml`;
CONTRIBUTING.md; CLAUDE.md; FAILURE_PATTERNS 418 to 421. Local only: the roadmap's Phase 118 Release Gate note.
