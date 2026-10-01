# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branches `feature/Phase-118-Verification-For-Others` and `chore/dependency-updates`, 2026-10-01.** Phase 118
is in pull request #48, which has had three GitHub runs. Every failure they showed is fixed on the Phase 118
branch (`1a35d0a6` to `c8dfd3cd`) and merged into the dependency branch (`a482629d`), whose gate covers both
tips; this handoff is committed on the dependency branch. `gh` is not authenticated here: the user reads the
runs on a phone and pastes the logs, and the job summaries (`testgate` writes them on every outcome since
`e260f040`, masked) are what make that enough. Rules unchanged (method-as-key runbooks, two agents at most,
heavy commands under `~/.local/bin/capped`, the lab provisioned by The Pleiades only), plus one from this
session: no LocalStack in the hosted jobs.

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

1. **Push both branches** once `make push-gate` passes on the dependency tip (the receipt covers Phase 118's
   tip as an ancestor), then read the fourth run of #48. Phase 118 is 14 of 15; its Release Gate closes when
   that run passes, and so does Phase 106b's hosted-CI item.
2. **After the PR, plan with the user** (their words: "we get the pr done then we plan for it"): separating a
   push gate from a release gate, so the hour-long gate runs for a release tag and not for every push. Raise
   with it: the coverage re-measure gives a stalled container no second chance where the test pass does; the
   three non-paging views (`credential-types`, `credentials`, `projects`); a `govulncheck` guard that fails on
   a vulnerable package this module imports (offered under Phase 106b, not built); the Node 20 deprecation on
   the pinned actions; the setup-go cache tar warning.
3. **Dependency branch:** grpc held at 1.83.2 (413); GO-2026-5932 (`x/crypto/openpgp`) is a module-level
   notice with no fix and no importer. Its PR goes against the Phase 118 branch; the bodies for all three PRs
   were sent to the user on 2026-10-01.
4. Carried: 117a's env-gated ServiceNow gate; Phase 110's strict `make ci`; Phase 113's Pattern Entry Gate;
   Phases 12 and 70 have no Implements line; `fakeRepository.GetGroup` in the UI harness ignores `After` and
   `Limit`; the Ansible gate's sshd keeps its log wait by design.

### Decisions for the user

1. Open the dependency branch's PR against the Phase 118 branch once both are pushed.
2. A read-only `DOCKERHUB_TOKEN`/`DOCKERHUB_USERNAME` stays optional. LocalStack is not run in CI.
3. Once macOS `fast` and `winrm` pass on GitHub, make them required.
4. Whether the three non-paging views should page.

### Files changed (since the second run)

`internal/api/dispatcher_{testutil,release}_test.go`; `tests/e2e/{previous_build_test.go,
previous_build_ref_test.go,mesh_wss_release_gate_test.go}`; `tools/testgate/{options,main,gate_live_test}.go`
and the `Makefile` (`-repeat`); `internal/loader/*_test.go`; `internal/catalog/http/request_device_test.go`;
`internal/catalog/facts/gather_check_test.go`; `internal/inventory/onboard/probe_ssh_test.go`;
`pkg/remotefile/predict_test.go`; `internal/testsupport/require.go`; `.github/workflows/ci.yml`;
CONTRIBUTING.md; CLAUDE.md; FAILURE_PATTERNS 418 to 421. Local only: the roadmap's Phase 118 Release Gate note.
