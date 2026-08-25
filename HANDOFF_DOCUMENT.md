# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 78a is complete: the input-source model, the bounded resolution walk, and the credential
input-source API.** Phase 78 was split into 78a/78b/78c this session, following the precedent that
split Phase 22 into 22a/22b/22c and Phase 79 into 79a/79b/79c. Nothing has been committed (no
autonomous commits; awaiting the user's own go-ahead). Branch
`feature/Phase-78a-External-Secret-Store`, cut from `feature/Phase-74a-NETCONF`'s tip rather than
from `main`, for the reason in "Loose ends" below.

### Why the split, and what 78a is for

Phase 78's fourteen items span five separable subsystems under one Release Gate, which cannot close
until a real Vault container, a rotation pass over live encrypted data, a PFX unwrap and a cycle
refusal all close together. **78a alone is what unblocks Phase 101 (Mesh Identity)**, which needs
key custody from this phase and nothing else. 78b is the real sources (`hashivault_kv`, PFX/PKI);
78c is credential-row key rotation plus the associated-data migration of the two columns
`internal/crypto/envelope_bound.go` deliberately left unbound.

### The correction that should be read before prioritizing 78b or 101

**Phase 78 does not close the plaintext-credential-on-the-bus window, and 78a does not begin to.**
The phase text already said reference passing needs two things and that 78 owns only one of them.
Verifying that against the code made it stronger than the text: `internal/runner` has **no HTTP path
to the Controller at all**, no client, no URL, nothing. Every byte moves over NATS and that bus has
zero authentication, so a secret-resolution endpoint is necessarily NATS request/reply, and building
one before Phase 101 would let any process that can reach the broker ask for any reference. That is
a NET LOSS against today, where an attacker at least has to join the consumer group. So 78a records
the shape in `internal/credstore/resolve`'s package comment and deliberately does not build it: no
field was added to `wire.DispatchPayload` and no endpoint exists.

Two stale numbers were corrected in passing. `internal/topology/stream.go`'s `streamMaxAge = 7 * 24h`
has not existed since Phase 96c; retention is `DerivedMaxAge(budget)`, which is `budget * 336`
(`internal/topology/budget.go:119`). The default budget still yields exactly seven days, which is
why the refactor was invisible, but at `MaxOutageBudget` (12 hours) it yields **168 days** of
retention on messages carrying plaintext credentials. Phases 93, 100 and 102 all cited the dead line;
all three are corrected.

### What was built

`internal/credtype/lookup_factory.go` (new): `LookupFactory`, with `Namespace()`, `New(inputs)` and
`Reference(metadata)`. `credtype.Lookup` itself is byte-identical, which is the load-bearing claim:
Phase 22 promised the upgrade would be additive and this is where that was kept or broken.
`Lookups` gained a factory registry beside its existing name map, `NewLookups` kept its signature so
`cmd/controller` compiles unchanged, and both binding forms terminate in the new
`Lookups.ResolveThrough` so the refusals exist once.

`internal/ent/schema/credential_input_source.go` (new) plus back-references on `Credential`, both
dialect migrations (`sqlite/0018`, `postgres/0015`), unique index on (target, input id). Both edges
cascade on delete, which is deliberate and is the OPPOSITE of what `DeleteType` does: `Credential`
holds a secret, and `DeleteCredential`'s own doc comment already made this call for template
bindings. What makes it safe is that the target then fails loudly at injection with credtype's
existing "required and has no value at injection" error.

`internal/credstore/resolve/graph.go` (new): the recursive walk, `maxSourceDepth = 4`, cycle checked
BEFORE depth so a two-node loop is never reported as a chain that is too long.

`internal/credstore/ent_store_input_sources.go` (new): list, replace, and every refusal it can make
at write time, including a full graph walk for cycles. Plus `GET`/`PUT
/api/v1/credentials/{id}/input-sources`, mounted in `cmd/controller`.

### Two real defects, neither predicted by the phase text

1. **A credential whose required input comes from a source row could not be created at all.** The
   write-time required-input check knew three exemptions (a default, a string-form external
   reference, a launch prompt); a binding is a fourth, and it cannot exist before the credential it
   binds. Creating first and binding second is not a workaround, it is a window in which the
   credential exists and cannot authenticate. Fixed with `credstore.WithInputSources`, making the
   credential and its bindings one write, plus a variadic `sourced` parameter on
   `InputSchema.CheckValues`. Both variadic so all 107 existing call sites compile unchanged.
2. **The cycle refusal reached the API as an unclassified 500.** A 500 tells a caller to retry
   something that can never succeed. `ErrLookupCycle` and `ErrLookupDepth` now map to 409.

### Measured, not asserted

Fuzz, both run as real fuzzing rather than over their seeds: `FuzzExternalReference` **780,517
executions, 100 new interesting, clean**; `FuzzResolutionGraph` **2,627 executions, 28 new
interesting, clean** (it writes rows behind the store, since the store refuses cycles at write time).

Benchmarks (i7-8700K): no source **118,676 ns/op, 25,760 B, 479 allocs**; one hop **293,784 ns/op,
58,912 B, 1,147 allocs**; at the four-hop limit **744,076 ns/op, 149,392 B, 3,078 allocs**. The slope
is the finding: about **156 microseconds, 31 KB and 650 allocations per hop**, so one hop costs more
than an entire source-free resolution. That is the argument for the bound, and it runs once per bound
credential per dispatch with a device fan-out waiting behind it.

Coverage floors RAISED: `internal/credstore` 88.1 to 89.0 (measured 89.3),
`internal/credstore/resolve` 95.9 to 96.0 (measured 96.6). `internal/ent/credentialinputsource` joins
`excluded`, and so do `internal/ent/credential` and `internal/ent/credentialtype`, which Phase 22
created and never listed.

### Loose ends for the next session

- **`b1a63ba` is still not on `main`.** It is Phase 74a's coverage floors and handoff, and without it
  `make ci` fails on `main` for a reason unrelated to any current work. This branch was cut from it
  rather than from `main` so the gate is green here. That commit still needs a pull request.
- **`make ci` has NOT been run end to end this session.** Docker is unavailable on this machine, and
  roughly twenty packages provision real containers. What WAS run: `go build ./...`, `go vet`,
  `gofmt`, the full `go test ./internal/... ./pkg/...` before Docker went away, `internal/archtest`,
  `internal/ent/migrate` parity, `tools/coverage-check` (200 packages, none below floor),
  `tools/docs-lint`, and `tools/gendocs` proven idempotent by diffing two consecutive runs. The full
  gate needs re-running once Docker is back, before this is called verified.
- **78a ships a surface no source can resolve through yet**, deliberately. A binding whose source
  type nothing can build fails with an explicit error naming it and listing what this controller has,
  matching the declared-but-not-implemented convention. 78b's `hashivault_kv` is the first factory.
  It could not be done in 78a: a factory needs a credential TYPE to select it, and
  `TestTheCatalogCoversEveryAWXManagedType` refuses any managed namespace AWX does not also have, so
  inventing a `file` source type to make the row form demonstrable was not available.
