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

Every device-targeting task acquires a distributed lock on its target device before
running, so two concurrent runs never touch the same device at once. `lock_acquisition:`
controls *when*: `per_device_as_reached` (the default) acquires each device's lock only
once execution actually reaches that task; `all_at_plan_time` acquires every targeted
device's lock up front, before the first task runs, so a run that would eventually
contend for a device fails fast at the start instead of partway through. Locking is
per-device, not a single job-level lock: two runbooks touching disjoint device sets
proceed fully in parallel.

### Safety versus dry-run

`pleiades validate` is the closest thing to a dry-run today: it loads the inventory
and runbook, runs every registered validation rule (capability matching, lifecycle
gating, collection reachability, and more), and reports every finding without
executing anything. There is no separate `--dry-run` or `--check` flag on `run`
itself, and no mechanism yet that reports *what would change* without actually
changing it (Ansible's `--check` mode has no Pleiades equivalent). `pleiades run`
always validates first and refuses to execute if validation reports any error.

## Security and credentials

### Threat model, briefly

Pleiades stores exactly one class of durable secret today: device credentials
(username plus a password or an SSH private key), encrypted at rest. There is no
Vault, KMS, or other external secrets manager integration; the encrypted local file
is the only credential store that exists. Read this as a real constraint when
deciding whether Pleiades fits an environment that mandates a centralized secrets
manager, not as a gap to work around.

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

Not written. The distributed execution plane is a stub (see
[Start here](01-start-here.md)), and the reference Helm chart is unmodified
`helm create` output (`image.repository: nginx`), so no real throughput has been
measured to size against yet. Writing a sizing guide before either of those is true
would be a guess dressed up as guidance.
