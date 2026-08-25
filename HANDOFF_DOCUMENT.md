# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 78 is complete except PFX/PKI, which is blocked on a consumer that does not exist.** 78a is
committed as `7fbcb62`. 78b and 78c are uncommitted on branch
`feature/Phase-78a-External-Secret-Store` and await the user's own go-ahead. Docker was down for
part of the session and came back, so everything gated on it has now actually run.

### 78c, built this session

Two new rotation passes, `RotateCredentialInputs` and `RotateSavedLaunchConfigAnswers`, joining the
Device one. `Device` and `SavedLaunchConfig` gained a `secret_binding` column (migrations `0019`
sqlite / `0016` postgres) and the bound envelope that `Credential` has had since Phase 22, closing
the residual `internal/crypto/envelope_bound.go` recorded and deferred there. `IsBoundEnvelope`
reads the algorithm tag so the read path opens either form, which is what makes the upgrade
lossless.

### The forcing constraint, which was not predicted

Sealing a value against its row's binding means the write hook needs that binding, and **ent exposes
`OldSecretBinding` on `UpdateOne` alone**. `Device.properties` was written by the BULK `Update`
builder, and the old design tolerated that precisely because an unbound ciphertext was equally valid
on every row, which is the property that made it relocatable.

So the bulk path is refused now, and **both real callers were converted to `UpdateOneID` with the
version predicate**: `internal/inventory`'s own `Save` and `RotateDeviceProperties` itself. One
behavioural difference is recorded where it lands: `UpdateOne` reports an unmatched predicate as a
not-found rather than as zero rows affected, so `Save`'s optimistic-concurrency check reads
`ent.IsNotFound` where it read `affected == 0`. `internal/inventory`'s own concurrency tests pass
unchanged, which is the evidence that conversion preserved the semantics.

### Two asymmetries worth carrying forward

**A Device migrates itself**; ordinary operation rewrites its properties and the hook assigns a
binding to any row lacking one, so the pass is a sweep for rows nobody touches. **A
SavedLaunchConfig never does**: nothing in this platform updates its answers, so the pass is the
only path from unbound to bound for that entity. Since a survey is the one way a password reaches a
stored row, that pass is worth running even when no key is changing. Both are documented in Book 10.

And `Device.secret_binding` is Optional and NOT Immutable where `Credential`'s is neither. Forced,
not sloppy: the column arrives on a table that already has rows, no migration generates a UUID per
row portably, and the application only ever sets it when empty.

### The residual, stated rather than implied

Accepting both forms on read is what makes the upgrade lossless, and **until a deployment's passes
have run, any row still unbound remains relocatable**. The passes return counts so an operator can
tell when the window shut. That is the honest limit of what this stage closes.

### Verified

`go test ./...` passes in full with Docker available. `coverage-check` clean across 201 packages;
`internal/crypto` floor raised 88.9 to 89.0 (measured 89.2). `go build`, `go vet`, `gofmt`,
`docs-lint`, `gosec` all clean. Migration parity passes for both dialects.

**78b's Release Gate ran and passed** (13.7s against a real `hashicorp/vault` container) once Docker
returned. It had been left recorded as open rather than ticked while it could only skip, which is
what checkbox rule 1 is for; it passed first try.

**78c's own gate is `TestReleaseGate_TheAADMigrationClosesTheRelocationHole`**, written in three acts
so it is falsifiable in both directions: both forms read correctly, the relocation attack SUCCEEDS
before the migration, and the identical attack fails after it. Without the middle act the last one
cannot tell a working binding from a badly set up attack.

### The one thing left in Phase 78

**PFX/PKI (78b) is blocked on a consumer.** Section 17.4 wants the Runner to unlock a private key in
memory, so the deliverable is an unlocked certificate presented to something, and there is no
`tls.Certificate`, no `Certificates:` field and no client-certificate handling anywhere in
`internal/transport`, `pkg/` or `internal/adapters`. Building it now yields a parser with no caller,
which this phase's own Gate 2 rejects in its own words. What must exist first: a transport that
presents a client certificate (WinRM over HTTPS is the realistic first, `pkg/winrmexec` exists), a
licence check on `software.sslmate.com/src/go-pkcs12`, and a deliberate exemption in
`TestTheCatalogCoversEveryAWXManagedType`, since a PFX type would be the first managed type this
platform ships that AWX does not have. A reserved `pleiades_` prefix is the cheapest sound one.

### Loose ends

- **`b1a63ba` and `7fbcb62` are still not on `main`.** Without the first, `make ci` fails on `main`
  for a reason unrelated to any current work.
- **`make ci` has not been run end to end this session**, only its constituent parts. Worth one run
  before merging.
- **Two AWX namespaces are unverified**: `aws_secretsmanager` and `centrify_vault` came from
  `credtype.DeclaredLookups` rather than a fresh read of AWX's registry, which may spell them
  `aws_secretsmanager_credential` and `centrify_vault_kv`.
