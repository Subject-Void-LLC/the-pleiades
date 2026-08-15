---
status: beta
---

# Running in production

This book covers what a real deployment needs to know before it runs Pleiades
against production infrastructure: what happens when something fails partway
through, what is checked before anything runs, how credentials and secrets are
handled, and what is genuinely built versus still design work. As with every page
in this reference, a claim here is either backed by real, tested code or is
explicitly marked otherwise; nothing below is aspirational.

## Failure semantics and safety

### A command is never retried once sent

The Walk-tier CLI's real SSH transport (`internal/transport/ssh`) retries with
backoff and jitter, and short-circuits through a per-target circuit breaker, but
**only during the dial phase**: establishing the TCP connection and completing the
SSH handshake. The moment a command has actually been sent to the remote side, it
is never retried, for a specific reason: a command may have partially executed
remotely already, and blindly retrying it could re-apply an unknown side effect
(delete a file twice, restart a service twice, and so on). A connection lost
mid-session is reported as a genuine error, never silently retried and never folded
into a fabricated exit code.

Practically, this means: a dial failure (device unreachable, port closed, auth
rejected) is retried automatically up to a bounded limit before it is reported as an
error. A failure *after* the command was sent (the connection drops mid-run, the
device reboots unexpectedly) is reported as an error immediately, and it is the
caller's job to determine what state the device was left in and re-run explicitly
once that is known, not Pleiades' job to guess.

### Blast radius is always computed, never authored

Before `pleiades run` executes a runbook, it prints the blast radius: the number of
distinct inventory devices the runbook targets (deduplicated, across every
`pretasks`/`tasks`/`posttasks` task and every `block`/`rescue`/`always` descendant),
and the distinct set of `tier` property values those devices declare. This is
computed fresh from the runbook and the current inventory state at call time; a
runbook author never writes a `blast_radius:` field by hand, and a stale one can
never exist. There is no defined tier ranking today (no built-in notion that `prod`
outranks `lab`), so blast radius reports the raw set of tiers touched, not a single
worst-case severity score.

### Only an active device accepts work

Every inventory item carries one of eight lifecycle states
(`discovered`, `quarantined`, `onboarding`, `active`, `simulate-locked`,
`unreachable`, `decommissioning`, `archived`). Only `active` accepts real work.
`pleiades validate` rejects any task whose target resolves to a non-active device,
by name, before anything runs; the Walk-tier executor and the Crawl-tier dispatcher
both re-check the same rule at their own layer as well, so a device is never
executed against by a path that happened to skip validation.

### Locking

**The Walk-tier CLI's locking is in-process only. Two `pleiades run` invocations do
not exclude each other.** `cmd/pleiades/run.go` wires `lock.NewInProcessManager`,
whose own doc comment says it "only guards against concurrent access within this
process." Every `pleiades run` is a separate process that starts with its own empty
lock table, so a lock one run holds is invisible to every other run. Two runs on the
same machine can configure the same device at the same second. This lock manager holds
a plain in-memory map and touches neither the network nor the filesystem, so two
machines cannot exclude each other either.
This is measured, not inferred: two `pleiades run` processes started one second apart
against the same device were both connecting to it at the same instant, each with its
own socket, and neither reported any contention.

A real distributed lock manager does exist (`lock.NewNatsLockManager`, backed by NATS
JetStream), and only the `controller` and `runner` binaries construct it. Since Phase
16 the distributed execution plane does reach real devices, so that tier both runs for
real and holds a per-device lease while it does. The gap is now narrower and lives
entirely on this side: the Walk-tier CLI still has no distributed locking, so two
concurrent `pleiades run` invocations against one device do not coordinate.

**What to do instead:** serialize device access outside Pleiades. Run one
`pleiades run` at a time per device set. If more than one person or scheduler can start
a run, gate it with your own mutual exclusion: a CI concurrency group, a change window,
or a `flock` on a shared path. Do not rely on Pleiades to stop two operators from
touching one router.

`lock_acquisition:` controls *when* a task takes its locks, and its scope is a single
task, not the whole run. `per_device_as_reached` (the default) locks each device only
as execution reaches it, so one contended device does not stop that task's other
devices. `all_at_plan_time` locks every device that *that one task* targets up front,
all-or-nothing: if any one of them is contended, that task runs against none of them.
It does not lock the run's whole device set before the first task, so the name oversells
it. A later task set to `all_at_plan_time` still fails only once execution reaches it,
after earlier tasks have already changed earlier devices. Both strategies lock per
device, never a whole job, so two tasks targeting disjoint device sets never block each
other.

### Safety versus dry-run

`pleiades validate` is the closest thing to a dry-run today: it loads the inventory
and runbook, runs every registered validation rule (lifecycle
gating, collection reachability, conditional compilation, and more), and reports every finding without
executing anything. Capability matching is the one rule that barely runs: it covers
only the two legacy action names `ssh_exec` and `ios_backup`, never a catalog FQCN,
so `validate` passes a task pointed at a device that cannot run it. See
[Start here](01-start-here.md#implementation-status). There is no separate `--dry-run` or `--check` flag on `run`
itself, and no mechanism yet that reports *what would change* without actually
changing it (Ansible's `--check` mode has no Pleiades equivalent). `pleiades run`
always validates first and refuses to execute if validation reports any error.

## Security and credentials

### Operator accounts and sign-in

Create the first administrator on the host, before anybody can sign in:

```bash
controller bootstrap-admin --email you@example.com
```

It prompts for a password with echo disabled, creates the user, puts it in a team,
and grants that team system-scope admin. Re-running it is safe: the identity is left
alone and an existing password is refused rather than replaced, unless you pass
`--force`. For automation, `--password-stdin` reads the password as one line on
standard input:

```bash
echo "$PASSWORD" | controller bootstrap-admin --email you@example.com --password-stdin
```

A password is never accepted as a flag value on any path, because a flag is visible
in shell history and in the process argument list to every other user on the machine
for as long as it runs.

The other two commands are the recovery paths:

| Command | What it does |
|---|---|
| `controller reset-password --email <address>` | Replaces the password and revokes every session that account holds. Marks the password as needing to be changed at next sign-in. |
| `controller unlock --email <address>` | Clears a lockout without touching the password. |

**There is no password reset by email, deliberately.** Adding one would make the
first sign-in on a fresh machine depend on outbound mail working, which is the wrong
thing to put between an operator and their own control plane. Both commands run over
the shell access you already have.

**How a password is stored.** Argon2id, 19 MiB of memory, two passes, one lane, with
a 16-byte random salt per password, encoded as a PHC string so the parameters travel
with the hash and a later cost increase needs no migration. A password verified at
older parameters is transparently re-hashed at the current ones on the next
successful sign-in. It is a one-way hash and not encryption: nothing in the system
can recover a password, including you.

**Lockout.** Ten consecutive failures lock an account for fifteen minutes. The
counter is a row in the shared database rather than per-process state, so it is not
reset by a restart and cannot be multiplied by running more replicas. The expiry does
not reset the counter, so the next burst re-locks immediately. Nothing exempts an
account from lockout, including the first administrator, which is why `unlock` exists
and runs off the network.

**What a sign-in failure tells an attacker: nothing.** A wrong password, an unknown
address, an account with no local password and a locked account all produce the same
response, and all cost the same amount of time. The real reason is written to the
controller's log, where an operator can read it and a caller cannot.

**Signing in with a token still works** and is the only route for a deployment that
federates against an external issuer and holds no local passwords at all.

### Threat model, briefly

Pleiades stores two classes of durable secret.

The first is device credentials: a username plus a password or an SSH private key,
keyed by device name, encrypted at rest in a local file. This is the Walk tier's
store and it is still what the control plane falls back to when a template binds no
machine credential.

The second is control-plane credentials: a credential of a declared type, holding
whatever inputs that type declares, encrypted at rest in the database. These are the
ones bound to templates, and they are what an AWX migration brings with it.

One external secrets manager integration is implemented, and it is deliberately the
simplest one: a credential input can name a file, resolved at the moment a job
dispatches rather than when it was created. That covers a Kubernetes projected
volume, a Vault Agent sidecar and the External Secrets Operator, which is how
secrets arrive in a large fraction of deployments. Eight further sources are named
after their AWX equivalents and return an explicit "declared but not implemented"
error. There is still no direct Vault, KMS, or cloud secrets-manager client. Read
that as a real constraint when deciding whether Pleiades fits an environment that
mandates one, not as a gap to work around.

### Credential storage

`pleiades add-credential <device> --username <user>` prompts for a password or a
private-key path (never accepting a secret as a bare CLI flag value, since that
would be visible in shell history and to any other user on the same machine for as
long as the process runs) and writes it to `<project>/.pleiades/credentials.yaml`,
encrypted field-by-field with AES-256-GCM.

The 32-byte master key is resolved in this order, and the choice is never silent:

1. The `PLEIADES_MASTER_KEY` environment variable, if set (base64-encoded). A
   malformed value is a hard error; a deliberately set env var is never silently
   ignored in favor of the file.
2. `<project>/.pleiades/master.key`, if it already exists. A corrupt or
   wrong-length file is a hard error, never silently regenerated (regenerating
   would produce a key that cannot decrypt anything already encrypted under the
   old one).
3. Otherwise, a fresh key is generated with `crypto/rand` and written to
   `<project>/.pleiades/master.key` with mode `0600`, inside a `0700` directory.

Neither a runbook file nor `inventory.yaml` ever contains a password: the only
place a device's own connection detail (host, port, and anything credential-shaped
an inventory source adds) is stored is `Device.properties`, itself encrypted at
rest with the same AES-256-GCM envelope mechanism in the control plane's database.

### Credential types and the injector engine

A credential type is data: an input schema saying what a credential of that type
holds, and an injector document saying where those inputs go at run time. An
administrator defines one over the API and an AWX export decodes into it directly.

Six types ship with the platform and are installed on every controller start, under
the same namespaces AWX uses: `ssh` (Machine), `vault`, `net` (Network), `aws`,
`controller` (Red Hat Ansible Automation Platform) and `hcp_terraform`. They cannot
be edited or deleted, which is what lets an import reuse them rather than recreating
them as custom copies that silently stop tracking the shipped ones at the next
upgrade. Installing them is a reconcile keyed on namespace rather than a migration,
so a later release that adds a type or corrects one reaches a deployment that already
started.

Sixteen further AWX types are recognised and not implemented. `pleiades import
awx-credential-types <export.json>` reports which is which for your own export, with
the specific reason for each, before you commit to a migration window.

Four things about this are security-relevant in production.

**An injector document is executable.** It decides which environment variables the
customer's playbook runs with, and the playbook runs inside the same container the
credential was injected into. A type that could set `LD_PRELOAD`, `PYTHONPATH`,
`ANSIBLE_CONFIG`, `PATH` or a `BASH_FUNC_` variable would be arbitrary code execution
inside the run it was meant to authenticate, so those names and their relatives are
refused when the type is saved and again when a dispatch reaches it. The container
being single use does not help: the code would run before the container is destroyed.
For the same reason the web UI lists and tests credential types but does not author
them; that stays on the API, where a caller had to construct the request
deliberately.

**Binding is a higher privilege than editing.** Changing what a template runs is
`template:write`. Changing what it runs *as* is `credential:write`, granted
separately, because whoever binds a credential decides which identity the automation
acts under. The UI follows the same split: the control that binds credentials to a
template is gated by the credential scope, not by the template form's own.

**Secrets are never readable back.** Every read path returns a projection with no
field a plaintext value could occupy: a secret input reads back as `$encrypted$` and
there is no endpoint, parameter or header that returns the real thing. It is
decrypted only at dispatch, inside the process that injects it. Submitting the marker
back on an update leaves the stored value alone, so editing an unrelated field does
not destroy a secret.

**A prompted input is never stored.** A type may declare an input asked at launch
rather than saved. The answer travels with that one job and is written nowhere, which
also means a job launched with one cannot be relaunched: the platform says so rather
than silently repeating the run without it.

The residual to plan around is the message bus. Injected material rides the same
JetStream stream every dispatch uses, whose retention is seven days, so a rendered
secret can sit there for that long. This phase makes that worse in volume and
identical in kind: where a dispatch previously carried one flattened SSH credential,
a template binding a cloud credential and two file-generating ones puts several more
on the same message, whole PEM bodies included. The real fix is reference passing,
where the payload carries a handle and the runner fetches it over a
mutually-authenticated short-lived connection, and that needs a runner identity story
that does not exist yet. Until it does, treat the stream as holding secrets and size
its retention accordingly.

### PKI and TLS

An mTLS mesh between the Controller and Runners is designed (`internal/pki`
declares the interface: sign a CSR, issue a 72-hour certificate) but not
implemented; there is no certificate issuance, rotation, or mTLS enforcement today.
Treat this as `design`, not `beta`.

## Data handling disclosure

Two different things get called "secret" in this codebase, and they are protected
differently. Knowing which one applies to a given value matters for a real
deployment.

### What is encrypted at rest

`Device.properties` (the JSONB bag holding connection details and any
credential-shaped value an inventory source adds) is encrypted transparently by an
ent hook the moment it is written, and decrypted transparently on read. `Fact.payload`
(what a device reports about itself, telemetry) is a deliberate exception: it is
**not** encrypted. This is a data-classification decision made in code, not an
oversight: connection and credential material is treated as secret by default,
while gathered facts are treated as operational telemetry. If a Collection method
ever emits a genuinely sensitive value as a fact, `register_mask`/`secret_mask`
(below) is the mechanism to keep it out of printed and streamed output, but it does
not encrypt the stored fact itself.

### `register_mask` and `secret_mask` are a security contract, not a convenience

`register_mask:` on a task masks one or more fields of that task's own registered
result the instant it registers, before anything downstream can see it unmasked;
dotted paths are supported (`register_mask: response.body.token`). `secret_mask:`
retroactively marks an earlier, already-registered task's top-level fields as
secret; it does not support dotted paths, an intentional asymmetry, not an
oversight. Both require the masked value to be a string at least 8 bytes long:
substring-masking a shorter value risks scrubbing an unrelated, coincidentally
matching substring out of unrelated output, which would be worse than not masking
at all.

**Where masking applies, precisely:**

- Every printed line of the run's own final output (`cmd/pleiades/run.go`) is
  scrubbed using the complete set of values discovered as secret by the time the
  run finished.
- Every live progress event published during the run (the same event stream a
  Crawl-tier job log or SSE viewer would read) is scrubbed **best-effort, in
  flight**, using only the secrets known at the moment that specific event is
  published. An event published before a later task marks something secret cannot
  be retroactively scrubbed. This is a real, load-bearing limitation, not a
  hypothetical edge case: a runbook that discovers a secret late should not assume
  every earlier live event was masked.
- `when_cel` conditions always see the real, unmasked value. Masking a value from
  the conditional engine would silently break branching logic that depends on it.

**Where masking does not apply today:** the Crawl-tier distributed execution path
(a job dispatched to a `runner` over NATS) does not yet run real tasks at all (see
[Start here](01-start-here.md)), so there is no real stored job record or SSE
stream carrying task output to audit for masking yet. This section will need a real
audit once that path executes for real; treat the guarantees above as proven only
for the Walk-tier CLI's own output today.

### Telemetry

OpenTelemetry tracing exists in the control plane (a "Run" click ties to every step
across every device via one trace). What exactly a span attribute carries per
Collection method has not been separately audited against the masking rules above;
treat this as an open item for a future security review, not a settled guarantee.

## Configuration reference

Not yet generated. Each of the four binaries (`pleiades`, `controller`, `runner`,
and the web UI's own server) reads its configuration differently today, and no
single source of truth exists yet to generate a reference from. This page will
follow the same "read from the real source, never hand-maintained" discipline as
the module catalog once one does.

## Backup and restore, data retention and purge

Not documented yet. The control plane's data layer (ent-backed) has real schema
migrations (`internal/ent/migrate`), but no documented backup/restore procedure or
retention/purge policy exists today.

## Observability and troubleshooting

Prometheus metrics are exposed at `/metrics`. Structured logging exists throughout
the control plane and the runner agent. A generated metrics reference, an alerting
guide, and a troubleshooting/diagnostics-collection guide do not exist yet.

## HA and leader failover

Distributed leader election is real and tested (`internal/election`), used for
control-plane components that must run as a single active instance. A full
operational guide to configuring and verifying HA in a real multi-node deployment
does not exist yet.

## Multi-tenancy

None today. RBAC has three roles (`viewer`, `operator`, `admin`) and a scope model,
but there is no tenant isolation concept above that.

## Sizing

Not written. The distributed execution plane now reaches real devices, but the
reference Helm chart is still unmodified `helm create` output
(`image.repository: nginx`), so no deployment has been run at a scale worth sizing
against. One number is known and worth planning around in the meantime: a task that
calls a Collection method pays roughly 10 ms of process-isolation overhead, on top of
whatever the device work itself costs, because each such task runs in its own child
process (see [Start here](01-start-here.md)). Tasks using `ssh_exec` do not pay it.
Beyond that, writing a sizing guide would be a guess dressed up as guidance.
