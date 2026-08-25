# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 78b is one item short of complete: `hashivault_kv` is built, PFX/PKI is blocked, and the
Release Gate has never run.** Phase 78a was committed as `7fbcb62`. 78b is uncommitted and awaiting
the user's own go-ahead. Branch `feature/Phase-78a-External-Secret-Store`, which now carries both
stages; it was cut from `feature/Phase-74a-NETCONF`'s tip rather than `main`, for the reason under
"Loose ends".

### What was built

`internal/credtype/lookup/hashivault` (new): a HashiCorp Vault key/value source, implementing
`credtype.Lookup` and `credtype.LookupFactory` with no change to either port, which is Phase 22's
additive-upgrade claim holding for a second time. A bounded `net/http` client rather than
`github.com/hashicorp/vault/api`: a KV read is one GET, and owning the `tls.Config` outright is what
makes Section 17.4's strict chain verification a property of the TYPE rather than of a default,
since there is no field anywhere that could carry an insecure-skip. Both engine versions, the
version query, the three status classes, scalar coercion, and a 1 MiB response bound.

Registered as a factory by NAMESPACE only, never as a deployment-wide `Lookup` by name, because a
reference string has nowhere to put an address or a token. Naming it in the string form now answers
the new `ErrLookupRowOnly` rather than "not implemented", because telling somebody a built feature
is unbuilt sends them to wait for something they already have.

`internal/credtype/managed/types/hashivault_kv.json` (new), plus seven declared-not-implemented
external types.

### The catalog correction, which is the more interesting half

`awxManagedNamespaces` was complete against the two entry-point groups it was drawn from, and AWX
registers its credential PLUGINS in a third. So **none of the eight external secret sources existed
as credential types at all**: an AWX export carrying a `hashivault_kv` credential reported an unknown
namespace, which is exactly what `TestTheCatalogCoversEveryAWXManagedType` exists to prevent. It went
unseen because the list was complete against the wrong question. All eight are now in it, and
`ReasonExternalSource` (declared in Phase 22, never used) is finally what the seven are declared
under. Its own stated reasoning stopped being true when 78a made a source a row.

**One caveat to check if an import ever reports an unknown namespace:** the original twenty-two
namespaces were read off AWX's registry; these eight came from `credtype.DeclaredLookups` and were
NOT re-read against AWX. AWX may spell two of them `aws_secretsmanager_credential` and
`centrify_vault_kv`.

### One defect, found by a test rather than by reading

`encoding/json` unmarshals a JSON `null` into a string target successfully, leaving it empty and
returning no error. A Vault key holding null was therefore reaching the generic empty-value refusal
instead of its own. Both refuse, so nothing unsafe happened, but only one of them names what is
actually wrong. Null is checked first now, with the ordering's reason written beside it.

### Two things NOT done, and why

**PFX/PKI is blocked on a consumer that does not exist.** Section 17.4 wants the Runner to unlock a
private key in memory, so the deliverable is an unlocked certificate presented to something. Verified
against the real source: there is no `tls.Certificate`, no `Certificates:` field and no
client-certificate handling anywhere in `internal/transport`, `pkg/` or `internal/adapters`. Building
it now yields a parser with no caller, which this phase's own Gate 2 rejects in its own words. What
must exist first: a transport that presents a client certificate (WinRM over HTTPS is the realistic
first, `pkg/winrmexec` already exists), a licence check on `software.sslmate.com/src/go-pkcs12`, and
a deliberate exemption in the AWX parity test, since a PFX type would be the first managed type this
platform ships that AWX does not have. A reserved `pleiades_` prefix is the cheapest sound one.

**The 78b Release Gate has never run.** `TestReleaseGate_AnInputResolvesOutOfARealVault` drives a real
`hashicorp/vault` container through the shipped type, the real factory, the real resolver and a real
database, asserting the read, the value's absence from the credential row, and rotation taking effect
with no edit to the target. Docker was unavailable, so it has only ever SKIPPED. It skips locally and
FAILS under CI, per `pkg/netconf`'s precedent. Checkbox rule 1 is explicit that a skip is not a run,
so that item stays open and 78b is not complete.

### Verified this session, and what was not

Ran clean: `go build ./...`, `go vet ./...`, `gofmt`, `internal/credtype/...`,
`internal/credstore/...`, `internal/api/...`, `internal/apispec/...`, `internal/archtest/...`,
`tools/docs-lint`. The hashivault package is 96.0% covered across 54 assertions, including a private
certificate authority verified with the negative control (the same server refused without its
authority and accepted with it).

NOT run, because Docker is unavailable: `make ci`, `tools/coverage-check` (it runs the full suite
internally), the twelve `/postgres` conformance subtests in `internal/ent`, and every container gate
including 78b's own. The floor for the new package was set from a direct package measurement rather
than from a full ratchet run, so `coverage-check` should be run once Docker is back.

### Loose ends for the next session

- **`b1a63ba` is still not on `main`**, and neither is `7fbcb62`. Without the first, `make ci` fails
  on `main` for a reason unrelated to any current work.
- **Run `make ci` and the two container gates once Docker returns**, before calling either stage
  verified.
- **78c is untouched**: credential-row key rotation plus the associated-data migration of
  `Device.properties` and `SavedLaunchConfig.answers`.
