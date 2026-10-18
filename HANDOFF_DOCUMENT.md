# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-15. Phase 1 of the owner's Tier A plan is built, on six stacked local branches, none pushed.** The
plan's rules on top of the usual ones still hold:
- no push or PR unless asked;
- never edit coverage-floor.json, gosec-waivers.json or flaky-packages.json to make a failure disappear;
- never write the owner's application's name anywhere (its paths and namespace included).

Phase 0 is merged (#56, #57, #58). The owner approved the first design note's shape and asked for the rest
without asking for each.

### The branches, each stacked on the one before, each with a worktree

| Branch | Worktree | Head | Adds |
|---|---|---|---|
| `feature/tier-a-phase1-gate` | the main checkout | `ddd35560` | `gate.run`, `gate.list` (`pkg/gate`, reused by `tools/internal/flakegate`) |
| `feature/tier-a-phase1-build` | `../auto-roboto-build` | `a885e02a` | `build.digests.check`, `build.digests.refresh`, `build.lock.verify` |
| `feature/tier-a-phase1-db` | `../auto-roboto-db` | `009a7430` | the six `db.pg.*` methods, `pkg/pgclient`, `pkg/sqllint`, the `database` credential kind |
| `feature/tier-a-phase1-scan` | `../auto-roboto-scan` | `ceecf026` | `scan.vuln`, `scan.sbom`, `scan.secrets` |
| `feature/tier-a-phase1-ai` | `../auto-roboto-ai` | `04e544dd` | the eleven `ai.*` methods (below) |
| `feature/tier-a-phase1-edge` | `../auto-roboto-edge` | top of the stack | `edge.tunnel`, `edge.access`, `edge.dns`, then `file.json`, `file.yaml`, `http.check`, `win.schtask`, and this handoff |

The counts on the top branch: 136 registered, 133 implemented (the same three declared), 23 namespaces, and check
mode for 123. Each collection has:
- a design note in `docs/design/`;
- a sample under `examples/`;
- a changelog entry;
- a release gate through the real binary.

**ai.\*:**
- `ai.chat` and `ai.route` (route calls the use's providers in fallback order);
- `ai.provider.list` and `ai.provider.probe`;
- `ai.model.list`, `load`, `unload`, `pull` and `remove`, for an Ollama host with the declared single-resident
  guard;
- `ai.bench.run`;
- `ai.prompt.verify`.

Its pieces are `pkg/aiprovider` (four kinds), `pkg/aitask`, `pkg/promptsign`, the `ai_provider` device, and the CLI
`pleiades ai prompt keygen|sign` and `pleiades ai review list|show|approve|deny`.

**Human review (asked for mid-session).** Review rules hold an answer for a person:
- a person at the terminal decides at once;
- with nobody there, the answer is kept in `ai/review` and the task fails `held`;
- running again reuses the decision with no second call;
- a Runner fails closed.

The engine's pause and resume is declared, not built (`internal/engine/pause.go`, `Executor.Resume` returns
`ErrPauseNotBuilt`): Phase 27's seam, at the owner's request.

**Proof, live versus fixture:**
- **Live:**
  - `db.pg.*` against throwaway Postgres, and Kubernetes check mode against k3s (Phase 0 and the db branch).
  - `scan.*` against this machine's Docker with the pinned scanners.
  - `file.json`, `file.yaml` and `gate.*` against a real in-process SSH server.
  - `http.check` against real HTTP servers.
- **Fixture only:**
  - The `ai.*` providers: a real HTTP server speaking Ollama's and the hosted kinds' documented wire formats. No
    hosted provider was called.
  - `edge.*`: a real HTTPS server answering as Cloudflare's API documents it. No real account was read.
  - `win.schtask`: its WinRM call replaced by a recorder, and its script parsed by Windows PowerShell 5.1's parser,
    with nothing run on a Windows host.

### Gates

`make ci-remote` passed on every branch:

| Branch | Result |
|---|---|
| gate | pass |
| build | pass, 122 s |
| db | pass, 193 s |
| scan | pass, 237 s |
| ai | pass, 174 s, after a gosec G703 fix with no waiver (FAILURE_PATTERNS 475) |
| edge | pass, 241 s |

`make pr-check` passed on the gate branch (receipt written). Its run on the top branch, after these records, is
recorded in `.IGNORE/timings.md` and the session's report. The tests of each package a commit touched passed before
that commit: under `-race`, except `cmd/pleiades`'s release gates, which ran without it.

### Next

1. Offer the pull requests one at a time, bottom of the stack first (gate), each only when the one before has
   merged green: the owner's one-PR-at-a-time rule. Rebase each branch onto main after its predecessor merges.
2. Open items for the owner:
   - moving `file.copy` onto `remotefile.Replace` is a contract change;
   - FAILURE_PATTERNS 470 and 471 are open (a fixture's mapped port never answering);
   - the Controller's trusted keys and approvals are Phase 27's.

### Files changed (Phase 1, across the six branches)

`pkg/{gate,pgclient,sqllint,scanignore,osvreport,sbomcheck,promptsign,aiprovider,aitask,cfapi,docedit}`,
`pkg/sdk/{database,ai,review}.go`, `pkg/capability` (AIProviderCapable, CloudflareAPICapable),
`internal/catalog/{gate,build,db/pg,scan,ai,edge,win}`, `internal/catalog/file/{json,yaml,structured}.go`,
`internal/catalog/http/check.go`, `internal/inventory/devices/{ai,cloudflare}` and their onboarding probes,
`internal/engine/{ai_project,review,pause}.go`, `internal/aireview`, `internal/validate/residency_rule.go`,
`internal/prompt/person.go`, `cmd/pleiades/ai*.go` and the release gates, `docs/design/*.md`, `docs/10`,
`examples/{gate,build,db,scan,ai,edge,config}_lab`, `changelog/`, the records files.
