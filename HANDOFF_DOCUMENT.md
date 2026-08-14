# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/launch-fields-and-push-gate`. Directive: plan and build Phase 22, Credential Types
and the Injector Engine, aiming for AWX parity. Everything below is uncommitted, held per standing
instruction. Commit messages for 22a and 22b were prepared and given; 22c's is not written yet.**

**PHASE 22 IS COMPLETE: 22a, 22b AND 22c.** The first two stages' status is in
`HANDOFF_ARCHIVE.md`. This section covers 22c: managed-type data, the reconcile, both UI views, the
template form's credential controls, the import CLI, and the docs.

### The finding that reshaped this stage

The plan said roughly twenty of AWX's managed credential types have injectors that are pure data,
and sized 22c around shipping them. That is wrong about AWX, and the correction came before the
code per the Architecture Mismatch protocol.

`awx_plugins.credentials.plugins` is the authority. Of the twenty-two managed types AWX registers,
SEVEN build their environment in Python through a `custom_injectors` function with an empty injector
document (`aws`, `gce`, `azure_rm`, `openstack`, `vmware`, `kubernetes_bearer_token`, `terraform`);
TWO use Jinja control flow this platform's renderer refuses by design (`insights`, `rhv`); TWELVE
declare no injectors at all because a subsystem consumes them rather than an injection; and exactly
ONE has a document this platform can copy (`controller`).

So 22c ships SIX types and declares SIXTEEN, close to the inverse of the plan. `LESSONS_LEARNED.md`
#109 records the general rule; the package doc of `internal/credtype/managed` records the count
next to the data it governs.

For the seven Python types there is no document to be faithful to, so fidelity is measured on the
resulting ENVIRONMENT rather than on the document. That is what licensed `Injectors.OmitEmpty`, the
one field on that struct AWX does not have: it expresses in data the `has_input` condition AWX
expresses in code, and it is what makes `aws` produce byte-identical output. Without it,
`AWS_SESSION_TOKEN` is set to the empty string when no token is configured, and botocore treats a
present-but-empty session token as a credential to use, failing the request instead of falling back
to the access key.

### The defect real vendor data found

`FAILURE_PATTERNS.md` #121. Transcribing `controller` and running it failed the ENTIRE injection
with an undefined-variable error, because an operator using an OAuth token supplies no username and
no password, and `RenderVars` built the namespace from the values a credential actually held while
the renderer is strict-undefined.

Strict-undefined has two jobs separated in time, and only one belongs at render. Catching an
undeclared name is a check about the TYPE and `Injectors.Validate` already does it at save.
Catching a blank optional is a check about the CREDENTIAL and refusing is wrong there. `RenderVars`
now seeds every DECLARED input from the schema, which is also exactly what AWX does. The real case
that still has to fail, a required input prompted at launch and never answered, moved to
`Credential.checkReady`, which names the input where the undefined-variable error never did.

Two AWX behaviours were transcribed at the same time: booleans render as `True`/`False` (and
`False` when unset), and an `ssh_private_key`-format input gains a trailing newline if it lacks one.

Every unit test, both release gates and three fuzzers were green while this defect existed. It was
killed by twenty lines of somebody else's real configuration.

### What 22c built

**The managed catalog (`internal/credtype/managed`).** One embedded JSON document per shipped type,
named after its own namespace and held to it by the parser. Six types: `ssh`, `vault`, `net`,
`aws`, `controller`, `hcp_terraform`. Sixteen `NotImplemented` entries, each with one of four closed
reasons plus a per-type detail naming the specific missing thing.
`TestTheCatalogCoversEveryAWXManagedType` compares shipped-plus-declared against AWX's own registry
in both directions, so a type AWX adds is a failing test rather than an import reporting an unknown
namespace.

`machineTarget` now accepts `KindNet` as well as `KindSSH`. AWX's `net` type declares exactly the
four transport inputs under exactly the four ids, AWX consumes them by reaching the device over SSH,
and this platform's only transport is that same SSH. Shipping it without a target would have stored
a username and a private key nothing read, which is #116's shape with an authentication failure as
the symptom.

**The reconcile (`credstore.ReconcileManaged`).** Idempotent, keyed on namespace, run at every
controller start rather than by a migration, because a migration cannot be re-run when a later
release adds a type or corrects one. It never deletes, and a namespace held by a CUSTOM type is
left exactly alone and logged with what to do about it, rather than overwritten.

**Both UI views are implemented.** `credentialtypes` lists cross-tenant with a Test action that
previews a type's injectors against caller-supplied values. `credentials` ships the list, and its
package doc REVISES the earlier written promise never to enumerate rather than silently
contradicting it: the argument proved too much (Devices, Templates and Inventories already disclose
the same reconnaissance to the same reader), rotation is impossible without enumeration, and the
original sentence's own second half asked for an AUTHORIZED lookup, which is what `credential:read`
being its own scope provides. Neither view can leak a value: the projection they hold has no field
one could occupy.

Both are READ-ONLY. An injector document decides what environment the customer's playbook runs
with, and `internal/credtype` refuses `LD_PRELOAD` and its relatives precisely because that is code
execution inside the run, so authoring stays on the API.

**The template form.** Prompted credential inputs render as `KindPassword` controls named
`credential_<id>_<inputid>`, read back through the same function that produced them so a value can
neither arrive undeclared nor be silently dropped, and passed as the separate `PromptedInputs`
argument that `recordConfig` structurally cannot see.

Binding is a RecordAction rather than a control on the edit form, which corrects the plan.
A control on the edit form is gated by that form's scope, so anybody who could rename a template
could change what it authenticates as. `auth.RelCredentials` was added for it: sharing `RelUpdate`
with the template's own edit both collides in the view registry and conflates two different
privileges.

**`pleiades import awx-credential-types <export.json>`.** Reports rather than writes, because the
Walk tier does not dial a controller. Four verdicts (importable, already shipped, not implemented,
refused), decoded through the same structs and validated through the same engine the Controller
uses, so a type it accepts is a type the Controller accepts. Non-zero exit when something would not
import, so it works as a migration gate; `--out` writes each importable type ready to post. Tested
against the real captured AWX corpus fixture.

### Gate results

Green: build, vet, fmt, gosec (11 findings, all individually waived), docs-lint, arch,
`push-gate-race`, `push-gate-integration`, and coverage (157 packages, none below floor).

Coverage floors raised, none lowered: `credstore` 86.3 to 87.3, `credtype` 97.7 to 97.8,
`cmd/pleiades` 68.8 to 69.4, and a first floor of 95.3 for `credtype/managed`. Every
regression this stage produced was code that had been ADDED and undertested, so it was
tested rather than recorded.

One gosec waiver was added: `credentialPrefix`, the string `"credential_"`, is a form
control name prefix and G101's heuristic matches the identifier's name. The reason is
written out in `gosec-waivers.json` rather than the constant being renamed, because the
name is what pairs it with `surveyPrefix` directly above it.

Both tolerant test gates reported warnings on one run and passed clean on a rerun, in
`internal/ent` (24) and then `cmd/runner` plus `tests/e2e` (16). All three are
flaky-packages.json entries and this is FAILURE_PATTERNS.md #61's shape. Verified not
caused by this work: `internal/ent` passes alone and passed with every change stashed,
and both credential release gates
(`-run 'CredentialInjection|ReleaseGate'`) pass on their own.

Two are not green and both are known:

- `govulncheck` reports 6 stdlib advisories, verified pre-existing in 22b by stashing all changes.
  They are `go1.26.5` findings fixed in `go1.26.6`, a toolchain bump unrelated to this work.
- `docs-gen-check` fails until the regenerated files are committed, which is the same state 22b
  ended in. Generator output was verified stable across two consecutive runs by md5.

### What is still true after Phase 22

- `env` and `file` injectors are legacy-path only; the native path refuses both at bind time and at
  run time. Named follow-up: `sdk.RunbookContext.InjectFiles` over the existing stdin plus fd-3
  child channel.
- One external secret source (`file`), eight declared and not implemented.
- Sixteen AWX managed credential types declared and not implemented, each with its reason.
- The one-credential-per-kind rule is application-enforced, not a database constraint.
- JetStream retention: injected secrets ride the one stream for up to seven days, and this phase
  makes that worse in VOLUME and identical in KIND. The fix is reference passing, which needs a
  Runner identity story that does not exist.
- `SavedLaunchConfig.answers` and `Device.properties` remain unbound by AAD.
- No credential-row key rotation: `crypto.RotateDeviceProperties` covers Device only.

### Next

Write the 22c commit message. Nothing is committed; all three stages are staged in the working
tree, held per the standing instruction.
