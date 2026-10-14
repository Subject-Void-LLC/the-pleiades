# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-14. Pull request #55** (`feature/Part-XVIII-119c-Kubernetes`, 119c) is open against `main`; #54 merged
as `d3b43532`. Rules unchanged: the gate is GitHub Actions; before every push run `make pr-check` (LESSONS 271)
under `~/.local/bin/capped` (3 GB or more, since `govulncheck` peaks at 2.73 GB) with `GOMAXPROCS=4 GOFLAGS=-p=1
PR_CHECK_PARALLELISM=2`; one PR at a time; method-as-key runbooks; no AI credit anywhere; never touch
`/mnt/c/Users/raymo/coupon_clipper` or the live `groceries` namespace.

### PR #55

Commits: `d5011ea7` (119c), `faadf816` (klog direct in `go.mod`), `0d2b11aa` (each package's floor covered),
`2a18f24a` (the hardening below), `d0da3f84` (`pkg/awscloud` back over its floor). Run 38636093862 on `0d2b11aa`:
`ci` on all three systems and `containers` (1) to (4) green; `fast` red (k3s, 446 and 447); `fast-macos` red
(`pkg/tftpxfer`, below); `coverage` skipped behind `fast`.

- **k3s fixture** (`internal/testsupport/k3s.go`): its readiness exec raced k3s's cgroup v2 evacuation, so every
  controller enable failed with EBUSY and kubelet died (FAILURE_PATTERNS 446, reproduced with `--cpus=0.05`). The
  wait now reads "Starting k3s" first; a failed start leads with k3s's `failed to enable controller` lines.
- **Container-list guard** (`tools/internal/flakegate`'s `TestEveryContainerPackageIsListed`,
  `containerscan_test.go`): follows each test's calls through the module instead of reading direct imports (447).
  `pkg/kubeapi` is now in `DOCKER_DEPENDENT_PACKAGES`; `internal/engine` and `internal/forge/playbook` stay in the
  fast tier through `oneShotContainerPackages` (one-shot `docker run`, kept on macOS).
- **`make pr-check`** (Makefile, `tools/prscope`, `coverage-check -scoped`, `pr-check-vet`). Its first full run
  found `pkg/awscloud` at 94.9% against 95.8% (the EKS code's untested branches), fixed in `d0da3f84`.

### Next

1. Push `2a18f24a` and `d0da3f84` to #55 once `make pr-check` passes on them, and watch the run to green; the user
   merges. Then tick 119c's Release Gate in `.SPECIFICATION/IMPLEMENTATION.md`.
2. `fast-macos` (advisory): `pkg/tftpxfer`'s `TestLongestAcceptedFilenamesCrossARealServerWhole/largest_blocksize/
   a_space` failed once after 59.8 s (`read udp [::]:...: i/o timeout`) in the strict repeat. Not reproduced on
   Linux (40 runs of all 18 subtests); one lost datagram is retried at every step in pin/tftp v3.2.0, so six
   unanswered attempts points at a stalled server, cause unknown. The test's 5-second context bounds nothing
   (tftpxfer checks it only before a transfer, as documented).
3. Only when the user says: 119e, 119f, 119g, 119h, 119l, 119m; then 119d, 26, 119k; then 119i, 119j.
4. Open offers: the `winrm` CI diagnostic run; CodeQL and secret scanning (the user's settings);
   `pkg/containerengine` has no floor (53.7% in CI); `internal/archtest` and `internal/ent/migrate/gen` are in
   `DOCKER_DEPENDENT_PACKAGES` though neither starts a container.
5. `pkg/kubeapi` (80.5%) and `internal/inventory/devices/kubernetes` (90.5%) have no floors yet; record them.

### Files changed this round

`internal/testsupport/k3s{,_test}.go`, `tools/internal/flakegate/{repeatgate,containerscan}_test.go` and
`testdata/containerscan/`, `tools/prscope/`, `tools/coverage-check/`, `Makefile`, `coverage-floor.json`,
`pkg/awscloud/{eks,instance_internal}_test.go`, `CLAUDE.md`, `changelog/pr-check.added.md`, FAILURE_PATTERNS 446
and 447, LESSONS 271 to 273.
