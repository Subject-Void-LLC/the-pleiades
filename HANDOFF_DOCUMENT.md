# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Production-Packaging`. Directive: plan and build Phase 20, Production Packaging.
Phase 20a is BUILT and `make ci` passes in full. Nothing is committed, per the standing instruction.
Phase 79's handoff moved to `HANDOFF_ARCHIVE.md`.**

### The phase opened by correcting its own map, and had to

Four Phase 20 items described a repository that no longer exists, so the Architecture Mismatch
protocol applied before any code. `cmd/controller` and `cmd/runner` both exist and compile, so
"entrypoints that do not exist" was false; both Dockerfiles already built package paths; Phase 19
had deleted the separate UI service, so there was no third image to write; and the compose NATS
healthcheck had been edited since the item was written. All four are struck and restated in place.

### Two findings that changed the plan

- **`CGO_ENABLED=0` compiles clean and breaks SQLite at run time.** The roadmap asked for a
  "distroless or scratch base", which requires a static binary, and `mattn/go-sqlite3` is a cgo
  driver that degrades to a stub rather than a compile error. The committed Alpine image was
  ALREADY broken this way: `golang:1.26-alpine` sets `CGO_ENABLED=0` and ships no C compiler, so the
  shipped controller died in its first migration on its own default DSN. `FAILURE_PATTERNS` #122.
  Resolved with `gcr.io/distroless/base-debian12:nonroot` and a bookworm builder, cgo kept on.
- **The Ansible legacy path needs a Docker daemon**, because `internal/adapters/legacy` starts a
  sibling container rather than shelling out. A pod has none, and the two ways to give it one are a
  node escape or a privileged sidecar. This is now the Pattern Entry Gate's Sidecar rejection with a
  real instance behind it, and the chart must refuse to render a runner with a playbook dir set.

### What shipped in 20a

Both images hardened (distroless, non-root, `-trimpath -ldflags="-s -w"`, digest-pinned, OCI
provenance labels fed by build args). A `.dockerignore` that denies by default, with a test asserting
the shape, the forbidden paths, and that every allowance is one a Dockerfile needs; the build context
went from 410 MB to 13 MB. `docker-compose.yml` gained named volumes, a real controller healthcheck,
`start_period`/`start_interval` tuning, explicit image names, and a NATS probe that actually detects
a JetStream-less broker. A `healthcheck` subcommand on `cmd/controller`, because the distroless image
contains exactly one executable and a healthcheck that cannot run is the same as none.

Measured: warm start 9.978 s to ~4.1 s, cold build 144 s to 127 s, context 410 MB to 13 MB.

### Three defects the adversarial passes found, all reproduced

- **Every `docker compose down` destroyed the control plane.** No `volumes:` key existed at all.
  Reproduced: bootstrap a user, `down` without `-v`, `up`, and the volume id had changed with zero
  users. JetStream had the same gap, taking the scheduler lease bucket.
- **`docker compose up -d --wait` exited 0 printing "Healthy" for a dead controller**, then `ps` hid
  the row. This refuted the justification the file itself carried for shipping no healthcheck.
- **`USER nonroot:nonroot` makes every `runAsNonRoot: true` pod fail** with
  `CreateContainerConfigError`. Compose cannot express `runAsNonRoot`, so 20a's own gate was
  structurally blind to it; found by really installing into kind. Fixed to `USER 65532:65532`.

### A claim this project had on record was false, and the truth is worse

`HANDOFF_DOCUMENT.md` and Phase 79's Release Gate both said a real browser refuses the `__Host-`
session cookie on the documented compose path. **False for `http://localhost`**, which browsers treat
as a potentially-trustworthy origin; a real Chromium completed the whole documented flow. What is
true is narrower in reach and worse in kind: on any non-loopback origin, and Chromium keys that on
the host STRING so a hostname resolving to 127.0.0.1 still counts as remote, the cookie is refused,
CSRF then fails, and the page says "Those credentials were not accepted" although the password was
never checked. Every real deployment hits this, as a misleading wrong-password error. Corrected in
both places. TLS is still required; the recorded reason was wrong and understated it.

### Governance: a deadline nobody could pay, and my own corrections of it were wrong twice

`gosec-waivers.json`'s header demanded "zero remaining waivers" before Phase 20 and attributed that
to AGENTS.md. **AGENTS.md never said it**: its only rule is "gosec and govulncheck must be validated
prior to production packaging (Phase 20)". The invented bar came from Phase 0's pre-existing-findings
policy and was copied into the header; both are struck now. Of twelve waivers only three are Phase
20's, seven pointed at **Phase 39, which is closed**, and two are test-only. **Phase 82** was
appended to Part IX to own the seven and decide the two. `LESSONS_LEARNED` #112.

Read this part as a warning: the first correction repeated the failure #112 records (said eight, not
seven; 3+8+2 is 13 against 12), and the second still left stale text in two Phase 20 items and in
#112 itself. All three rounds were caught by adversarial passes that recomputed every number from
source, never by review. Also uncounted until now: inline `#nosec` suppresses **55** further
findings, so this file's twelve are about 18% of the project's suppressions.

### Also fixed

A pre-existing red test blocking every gate, `internal/catalog/net/ssh`'s
`TestPing_DialFailureIsReported`, which assumed a just-closed port refuses connections; on WSL2 the
connect succeeds and fails later in the handshake. Not caused by this work (the package was
byte-identical to HEAD) but `LESSONS_LEARNED` #110 forbids pushing past a red gate regardless of
fault. `FAILURE_PATTERNS` #123. Pin drift this phase introduced was also closed: `make ui-dev` was
running a different NATS image and flags than compose under a comment claiming they matched.

### Next

- **20b: TLS.** The deletion surface is bounded and surveyed (3 files, 2 setters, 1 reader). The
  deletion and TLS must land TOGETHER: removing the insecure path without TLS breaks every
  non-loopback origin. No X.509 serving-cert generation exists to reuse, so a helper goes in
  `internal/testsupport` beside `BuildAnsibleRunnerImage` so uidev and the harness cannot drift.
  Editing `auth.go` will shift the three `G710` lines, which are now Phase 82's entries.
- **20c: the Helm chart**, still unmodified `helm create` output. Verified constraints in hand: pin
  by TAG not digest (digests do not resolve against side-loaded images), do not default
  `image.repository` to an unpublished registry path, liveness and readiness must not share a path,
  and a kind-based gate needs both kind and the node image pinned.
- **Open decisions**: `/readyz` is unauthenticated, unrate-limited and runs a real query per request
  with no `MaxOpenConns` bound, so a caller can flip a healthy controller out of rotation today. The
  write probe was tested and REJECTED (it does not detect the failure it was proposed for, and ent
  opens `BEGIN READ WRITE` which defeats its headline claim). Single-flight collapse is the fix and
  is now a Phase 20 item. A registry path and release tag are still unowned.
