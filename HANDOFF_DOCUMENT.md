# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/launch-fields-and-push-gate`. Directive: plan and build Phase 22, Credential Types
and the Injector Engine, aiming for AWX parity. Everything below is uncommitted, held per standing
instruction (a commit message is prepared, no commit was made).**

**Stage 22a's primitives half is COMPLETE and every CI gate is green.** The phase was planned in
three staged commits (22a primitives plus data model plus API, 22b the injector engine plus both
adapters plus the release gate, 22c managed types plus UI plus docs). What is built is the first
part of 22a: the two PLAN.md Section 25 Build Once Contracts this phase owes, their wiring, and the
structural guards that keep them singular. The credential data model, the injector engine and the
API surface are NOT built yet.

### Built and verified

1. **The map was fixed before the code**, per AGENTS.md's Architecture Mismatch protocol. PLAN.md
   Section 25's "Template renderer" row said "Build by Phase 28"; Phase 22's own checklist, Phase
   28's own checklist and AWX_PARITY_ROADMAP.md's A2 section all said Phase 22 builds it and 28
   consumes it. This is the fourth correction of that exact shape on that one table. Corrected, with
   the reasoning in `LESSONS_LEARNED_ARCHIVE.md` #107.

2. **`internal/render`** (new, 97.2%): the one Jinja-compatible renderer, behind an `Engine`/
   `Template` port, with a hand-written strict subset of Jinja2's expression grammar. The governing
   rule is strict-undefined: a referenced name absent from the variables is an error, never the empty
   string, because an injector rendering to `""` still sets the environment variable and the
   authentication failure downstream gets attributed to the wrong thing. `Template.Names` is what
   moves that failure from launch time to save time. Closed seven-filter set, refusal of `{% %}` and
   `{# #}` rather than passing them through as text, compile-and-cache mirroring
   `internal/engine/cel.go` including its compile-outside-the-lock convergence. Fuzzed for 45s over
   4.4M executions with the security property asserted (with no `default` filter in play, removing
   any supplied name must produce `ErrUndefined` and an empty string). Measured against Python
   Jinja2 3.1.6 on the same machine: 498x faster to compile, 70x faster to render.

3. **`internal/redact`** (new, 95.7%): the shared masking ruleset as data plus the one engine that
   applies it. Three channels: by VALUE (`Literals`, the relocated substring scrub), by KEY (an
   attribute named `password` is secret whatever its value is), by SHAPE (PEM blocks, JWTs, bearer
   tokens, AWS key ids, URL userinfo). `credential.Mask` was DELETED rather than left as a delegate,
   and its algorithm relocated verbatim with its hard-won asterisk-edge exception intact; the call
   sites were found with `gopls references`, which turned up three that a grep-shaped list had
   missed. `engine.minMaskableSecretLength` and `launch.RedactedMarker` also folded in.

4. **The ordering constraint is now executable, not advisory.** The specification required the
   ruleset be applied through `slog.HandlerOptions.ReplaceAttr` rather than a wrapping
   `slog.Handler`. `internal/redact/wrapper_control_test.go` builds the rejected design in good
   faith and demonstrates that it leaks attributes added with `Logger.With` while catching direct
   ones, which is what makes the wrapper a trap rather than an obvious mistake. Per
   `LESSONS_LEARNED.md` #95, the guard was shown to fail before being trusted to pass.

5. **All four composition roots wired**, `slog` options and `log.SetOutput` both. `cmd/runner` was
   taking `slog.Default()`, the unconfigured process default, in the binary that holds credentials
   most directly.

6. **Structural guards in `internal/archtest`**: `TestExactlyOneRendererImplementation` (plus a
   stale-allowlist companion), `TestEverySlogHandlerCarriesTheMaskingRuleset` (AST inspection of
   every `cmd/` handler construction), `TestEveryCommandUsingTheLogPackageMasksItsOutput`, and
   standard-library-only dependency guards on both new packages. Both logging guards were verified
   to fail on the exact regressions they exist to catch.

7. **`rules.json` ships into the legacy runner image** at `/opt/pleiades/redact-rules.json` for
   Phase 25's Python callback bridge, with `TestRulesetHasExactlyOneCopy` forbidding a second copy.

### Two findings worth reading before continuing

- **`FAILURE_PATTERNS.md` #118**: the masking control's first correct version cost 26x the unmasked
  baseline per log line, and 424 microseconds per line with a thousand live secrets. Fixed with a
  data-driven prefilter, a cached sorted snapshot and a zero-allocation pre-pass, down to 3.4
  microseconds. The prefilter is itself a silent-failure surface, so each pattern rule carries
  `samples` in the same data file and three tests hold the prefilter and the pattern against each
  other.
- **`coverage-floor.json` has its first ever downward adjustment**, `internal/credential` 91.7 to
  91.6, with the reason written into the file's own `_comment`. Nothing became less tested: a fully
  covered file left the package, and the file store's error paths gained real tests in the same
  change (`internal/credential/file_store_errors_test.go`).

### Verification status

`build`, `vet`, `fmt`, `gosec` (8 findings, all pre-existing and individually waived),
`govulncheck` (clean), `coverage` (150 packages, none below floor), `docs-lint`, `docs-gen-check`,
and `make arch` all pass. `go test ./...` is clean except
`cmd/runner`'s `TestAnsibleReleaseGate_RealPlaybookThroughTheFullChain`, which failed once under
full parallel load with `connection string: port "4222/tcp" not found` and passes in isolation:
`cmd/runner` is already listed in `flaky-packages.json` with a written reason, and this is
`FAILURE_PATTERNS.md` #61's shape exactly. Race detector clean across every touched package.

### Next step

Continue Stage 22a: the `internal/credtype` data layer (`InputSchema`, `Injectors`, validation),
written corpus-test-first against
`tests/parity/testdata/credential_types/custom-rest-api-token.json`, which must decode straight into
`credtype.CredentialType` with no translation layer because the AWX JSON tags are the proof. Then
the ent schemas for `CredentialType` and `Credential`, both dialects' generated migrations, the AAD
addition to `EnvelopeService` scoped to credential inputs, and wiring the built-but-never-composed
`crypto.SavedLaunchConfigAnswersHook` (which means `SavedLaunchConfig.answers` is plaintext in the
database today while the API schema tells callers it is encrypted at rest). The full plan, including
the four decisions already settled with the user, is in the approved plan file.

