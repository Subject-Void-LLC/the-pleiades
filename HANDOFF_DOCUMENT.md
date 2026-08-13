# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/launch-fields-and-push-gate`. Directive: plan and build Phase 22, Credential Types
and the Injector Engine, aiming for AWX parity. Everything below is uncommitted, held per standing
instruction (a commit message is prepared, no commit was made).**

**STAGE 22a IS COMPLETE and every CI gate is green.** The phase was planned in
three staged commits (22a primitives plus data model plus API, 22b the injector engine plus both
adapters plus the release gate, 22c managed types plus UI plus docs). What is built is the first
part of 22a: the two PLAN.md Section 25 Build Once Contracts this phase owes, their wiring, and the
structural guards that keep them singular. The credential data model, the injector engine and the
API surface are NOT built yet.

### Built and verified

**Part one, the two Section 25 primitives** (detail below in "Primitives").

**Part two, the credential data layer and encryption at rest:**

8. **`internal/credtype`** (new, 97.7%): `CredentialType`, `InputSchema`, `Injectors`, and their
   validation. The JSON tags are AWX's own field names, and `corpus_test.go` is the proof rather
   than the claim: the committed production Ascender fixture decodes STRAIGHT into
   `credtype.CredentialType` with no translation layer and no intermediate DTO. Validation compiles
   every injector template at save time and refuses one naming an input the type does not declare,
   which is what makes the renderer's strict-undefined rule safe at run time. `FuzzInjectorDocument`
   ran 5.4M executions with two security properties asserted over arbitrary documents: a validated
   document never names a reserved environment variable, and never carries a file label that escapes
   its directory.

9. **The reserved environment-variable refusal** is the headline item of this phase's Schema and
   Injection Hardening audit. PLAN.md Section 29.4 accepts the ephemeral container as the trust
   boundary, which is what permits secrets in the environment at all, but the customer's playbook
   runs INSIDE that boundary, so an injector able to set `LD_PRELOAD` is arbitrary code execution
   inside the very run the credential was meant to authenticate. Fourteen names plus the
   `BASH_FUNC_` prefix are refused, each with its reason written beside it.

10. **ent schemas for `CredentialType` and `Credential`**, both dialects' migrations generated and
    committed together (`sqlite/0012`, `postgres/0009`), plus the `Template` to `Credential` M2M
    that is the AWX binding axis this platform did not have. Typed `field.JSON` over the credtype
    structs generated cleanly, which resolves one of the plan's open questions.

11. **The AAD binding, and it is the most consequential thing in this half.**
    `internal/crypto/envelope.go` has always documented a residual: its ciphertext carries nothing
    tying it to its row, so an envelope copied between rows still decrypts. That was accepted when
    the only consumer was `Device.properties`. A credential row makes it unacceptable: relocating
    one organization's inputs onto another organization's credential makes the platform inject the
    first organization's secrets into the second organization's jobs, and the attacker never reads
    anything. `EncryptBound`/`DecryptBound` close it, scoped to credential inputs, under a distinct
    algorithm tag so old unbound ciphertext still decrypts and a bound one presented without its
    binding fails closed. The AAD is `Credential.secret_binding`, an immutable per-row UUID.
    `TestRelocatingStoredCiphertextBetweenCredentialsFails` performs the attack with raw SQL against
    a real database and requires it to fail; `TestTheUnboundFormStillRelocates` is the negative
    control proving the binding is doing the work.

12. **`SavedLaunchConfigAnswersHook` is finally composed.** It was written, tested, and registered
    nowhere, so survey answers were plaintext in every deployment while `internal/apispec`'s own
    schema told API callers they were encrypted at rest.
    `cmd/controller/composition_test.go`'s `TestEveryCryptoHookIsComposed` now reads both sides as
    source and fails the build if any exported `ent.Hook`/`ent.Interceptor` is left unregistered. It
    was verified to fail on exactly the historical bug.

13. **Parity moved, measurably.** `credential_types` went 0/7 to 7/7 represented, overall 37/119 to
    44/119, and A2's gap list dropped from 11 fields to 4. The remaining four are template binding,
    which is Stage 22b.

### Primitives

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

**`internal/credstore` and `internal/credstore/resolve` are BUILT.** The split is the security
control, not a style choice: `credstore.Credential` has no field a plaintext secret could occupy
(secrets read back as `redact.Marker`), `resolve.Resolver` returns the real values, and
`internal/archtest`'s `TestAPINeverImportsTheCredentialResolver` fails the build if the API layer
reaches the latter. Verified by making a handler import it and watching both guards fire.
`credtype.CheckBinding` is built with both callers, and the store enforces tenancy on binding
(`ErrCrossOrganization`) because ent cannot express it.

**The API surface is BUILT.** `credential:read` and `credential:write` scopes (binding sits under
write, not `template:write`: a template author decides WHAT runs, whoever binds a credential decides
what it runs AS). Thirteen endpoints in `internal/apispec/credential_endpoints.go`, all mounted, all
handlers holding `credstore.Store` and never the resolver. The generated OpenAPI grew from 58
operations to 71 with zero pre-existing operations altered, verified by comparing operation sets
rather than diff text (the raw diff looks like 5,663 changed lines and is entirely alphabetical
realignment).

Two G101 gosec findings were waived with individually written reasons: the heuristic fires on the
word "credential" in the two scope constants. No secret is involved and none ever will be.

### The disclosure guarantee, and how it is enforced

Worth stating in one place, because it is the point of the whole two-package split:

- `credstore.Credential` has no field a plaintext secret could occupy. Secrets read back as
  `redact.Marker`.
- `resolve.Resolver` returns real values and lives in its own package.
- `internal/archtest` fails the build if `internal/api` imports it. Verified by making a handler
  import it and watching both guards fire.
- `internal/api/credentials_test.go` sweeps every route on the surface and asserts the secret is
  absent from the RAW RESPONSE BYTES, not from a decoded struct. Decoding into a type with no
  password field would pass whether or not the password was on the wire. Verified by disabling the
  redaction and watching every affected route fail.

### Next step

Stage 22b: the injector engine, both adapters, and the release gate. Two things in it are already
known and should not be rediscovered: `internal/adapters/legacy/argv.go` puts extra vars on argv as
`-e <json>`, which leaks a secret extra var into the container's own `ps` and `/proc/<pid>/cmdline`
(the fix is an `-e @file` extra-vars file, unconditionally), and the native path must REFUSE `env`
and `file` injectors at bind time with a run-time backstop rather than ignoring them, because
PLAN.md Section 29.4 keeps the stricter rule for the native Go mesh.

The full plan, including the seven decisions already settled with the user, is in the approved plan
file.

