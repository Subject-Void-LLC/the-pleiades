# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-14. Two branches.** `feature/Part-XVIII-Container-Platforms` is **pull request #54** (119a, 119b),
pushed, its last commit `7016137a`. `feature/Part-XVIII-119c-Kubernetes` (off `7016137a`) holds **119c,
committed (the branch's one commit on `7016137a`), not pushed**; the user chose "a separate PR after #54": once #54 merges, rebase it onto
`main`, push, and open its PR. Rules unchanged: the gate is GitHub Actions (no local
`make ci`/`push-gate` unless asked), method-as-key runbooks, two agents at most, heavy commands under
`~/.local/bin/capped` with `GOMAXPROCS=4`, no AI credit anywhere, never touch `/mnt/c/Users/raymo/coupon_clipper`
or the live `groceries` namespace (read-only checks of Docker Desktop's cluster only).

### PR #54

Three fixes this session, each pushed: the Podman fixture checks passwords without PAM (`7a6599e7`,
FAILURE_PATTERNS 426); every floored package covered by its own tests (`35a07941`, 427); the Linux server prober
built on every OS, with an archtest against file-name build constraints (`7016137a`, 428). Run 38604838943 on
`7016137a`: all three `ci` jobs green (macOS and Windows included); the test shards were still running at the time
of writing. The user merges when every check is green.

### 119c (16 of 18 ticked; open: Release Gate, commit message)

- `pkg/kubeapi` (client-go v0.37.1): sessions from a record and a credential only; discovery of every served
  version, bounded (128 MiB per discovery, 64 entries per rule), server error text bounded and escaped; EKS
  identity minting through `pkg/awscloud.EKSToken` (SigV4 checked against an independent computation).
- `kubernetes_cluster` (record, accessors, `ServedKinds`, `KubeEKS`, `ConnectionProperties`), onboarding with
  bastion routes (`sdk.Router`, `engine.RouteDialerFor`, `onboard.WithRoutes`), `node_containers`.
- Kubeconfig import: CLI `import-kubeconfig` and `POST /inventory/kubeconfig-imports` (both scopes; a replacement
  changes only the four connection properties, through the loaded record).
- Credentials: `kubernetes_bearer_token` (AWX's) and `pleiades_kubernetes_client_certificate` (ours, the catalog
  now admits a `pleiades_` prefix), a code target in `internal/credtype`; native path delivers to the matching
  cluster only, Ansible path gets AWX's `K8S_AUTH_*` (verification off only by `verify_ssl: false`, a stated
  deviation from AWX); Controller onboarding falls back to the one host-matched stored credential
  (`resolve.KubernetesSecrets`, its reason written in `resolve.go`).
- `validate`'s `KubernetesKindRule` over `collection.ParamFormatKubernetesManifest`: proven on records; its proof
  through the binary is 119g's, with the first apply method.
- Also: a refused SSH host key is no longer retried or counted against the breaker (`pkg/remoteexec`, 429).
- Tests: k3s at 1.35 and 1.37, token and certificate, through an sshd bastion (host key refused when unknown), an
  `exec` kubeconfig refused, a static binary run where an in-cluster Runner runs (its admin ServiceAccount never
  used), Docker Desktop opt-in (`PLEIADES_TEST_DOCKER_DESKTOP_KUBECONFIG`, passed read-only), fuzzing of import and
  discovery. `gosec` clean (three G101 false positives waived by line); `govulncheck` affects nothing (GO-2026-5932,
  x/crypto/openpgp, no fix, uncalled). Cross-OS vet clean. Docs: `docs/10` "Kubernetes clusters", `docs/03`'s
  credential type lists corrected, generated reference, changelog fragments.

### Next

1. Watch PR #54 to green; the user merges.
2. Once #54 merges, rebase that commit onto `main`, push, and open 119c's PR (its local suites passed:
   `cmd/pleiades` 399s, `cmd/controller` 147s, `cmd/runner` 300s, the container packages it touched, archtest).
3. Then 119e, 119f, 119g (its apply method declares the manifest format and proves the kind rule end to end),
   119h, 119l, 119m; then 119d, 26, 119k; then 119i, 119j.
4. Open offers: the `winrm` CI diagnostic run; CodeQL and secret scanning (repository settings, the user's).
5. `pkg/containerengine` measured 53.7% in CI with no floor recorded; raise it and record a floor.

### Files changed

PR #54 this session: `internal/testsupport/{dockerhost.go,testdata/podman-sshd/Dockerfile}`, eight packages' new
tests, `internal/inventory/onboard/probe_linux_server.go` (renamed), `internal/archtest/filename_constraint_test.go`.
119c: see `git status` on its branch (about 95 files): `pkg/{kubeapi,awscloud,capability,collection,sdk,wire,
remoteexec,inventory}`, `internal/{inventory,credtype,credstore/resolve,dispatch,engine,api,apispec,validate,
credential,lock,runner,testsupport,archtest,clispec,forge}`, `cmd/{pleiades,controller,runner}`, `docs/`,
`changelog/`, `CLAUDE.md`, `gosec-waivers.json`, `go.mod`, and this session's FAILURE_PATTERNS 426 to 443 and
LESSONS 263 to 269.
