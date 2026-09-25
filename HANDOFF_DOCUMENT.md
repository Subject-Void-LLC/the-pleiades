# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 110 (connection persistence) and Phase 111 (generic device types) are both committed on
`feature/playbook-migration`, on top of Phase 35.** Phase 110's commits, the benchmark and its report,
the Ansible adapter's init fix and the capability reach fix are described in the previous handoff
(archive). Phase 111 is below. What remains open is listed under "Decisions for the user"; everything
else in both phases is ticked in the tracker with evidence.

### What Phase 111 is

A device nobody has written a type for can now be managed by the protocol it speaks:
`generic_ssh`, `generic_netconf`, `generic_http` and `generic_grpc`
(`internal/inventory/devices/generic`). Beyond a small baseline, a generic device's capabilities come
from the device: `internal/inventory/onboard` probes it (one prober per type, on a `pkg/registry`) and
records what its answers prove in the reserved `discovered` property, which only
`record.Base.RecordDiscovery` writes. Every other write path refuses it: `AddInfo`/`RemoveInfo`,
`add-host --set`, a hand-written `hosts.yaml` (the file inventory keeps the discovery in its generated
state file), and a sync plugin adding or updating a device. A generic device starts `discovered`
(`record.InitialState`), which runs nothing.

- **CLI:** `pleiades onboard <name> [--json] [--timeout]`.
- **Controller:** `POST /inventory/devices/{name}/onboard`, scope `inventory:onboard` (operators;
  `inventory:write` does not imply it), probe run in the Controller with the dispatch credential
  store, logged with the caller.
- **`http.request` device mode:** a `url` that is a path is joined to an onboarded `generic_http`
  device's base URL and carries that device's credential (`pkg/httpapi`: join, same-origin redirects,
  credential modes).
- **Capability tree:** `NetconfCapable` no longer sits under `NetworkCLICapable` (FAILURE_PATTERNS 345).
- **Two new capabilities:** `HTTPAPICapable`, `GRPCCapable`.
- **Recorded deviations:** a probe maps facts to capabilities directly rather than through a
  classification path (LESSONS 239), and `generic_ssh`'s shell is discovered rather than baseline.

### Verification run

- **Release gates, real binary, real servers, all passing:** `TestGenericReleaseGate_SSH` (Debian sshd;
  `identity.group.create` and `pkg.install` after onboarding), `_NETCONF` (Netopeer2 in `notconf`, an
  edit read back), `_HTTP` (HTTPS through `SSL_CERT_FILE`, device-mode credential), `_GRPC`
  (`grpc/java-example-hostname`, services from reflection). Plus `TestCLI_OnboardGenericSSH`.
- **`-race`** on every touched package, `cmd/pleiades` in full, `cmd/controller -short`.
- **Fuzz:** `FuzzParseSSHProbe` 2.24 M execs, `FuzzParseOpenAPI` 1.89 M, `FuzzFactText` 2.24 M,
  `FuzzJoin` 7.02 M, all clean.
- **Coverage:** new floors for `onboard` 91.9%, `generic` 97.3%, `pkg/httpapi` 98.1%; `pkg/inventory`
  and `internal/catalog/http` at 100%.
- **Push gate:** run on the combined tip before the push; see the closing report. The previous run
  (on `49cc4fd`) failed only `internal/adapters/native`'s floor (92.5% against 92.9%), fixed by
  `ca3cecd`'s stand-in child tests (93.9%). `internal/topology` failed twice under load and passed alone
  both times.
- **Not run:** `make ci` in full.

### Decisions for the user

1. **Walk tier:** a Runner rebuilds a dispatched device from its SSH address and capability names
   (`pkg/external.Device`), so a method reading any other accessor refuses there by name:
   `http.request`'s device mode, `net.netconf.config` (the Cisco types too), and the generic
   `pkg.*`/`svc.*` dispatchers. Disclosed in docs/10 and docs/01. The fix is rehydrating the real type
   on the Runner, which puts device properties on the dispatch (Phase 105's exposure question).
   Where it goes is open; it is the one open item in Phase 111.
2. **Phase 110 and 111 versions** are proposed as v0.3.0, beside Phase 35.
3. **Not built in Phase 111:** mTLS for `generic_http`, plain-gRPC Collection methods (out of scope
   until a device needs one), and a UI or API path to set a generic HTTP or gRPC device's address (the
   API and the web UI create devices without properties, as for every type; a sync or `add-host` does).
4. Carried over: `persist_connections` needs Organization, Project and System levels (Phases 103c, 104).

### Files changed (Phase 111)

New: `internal/inventory/devices/generic/`, `internal/inventory/onboard/`, `pkg/httpapi/`,
`pkg/inventory/discovery.go`, `pkg/capability/capabilities_api.go`,
`internal/inventory/record/initial_state.go`, `internal/api/devices_onboard.go`,
`internal/catalog/http/request_device.go`, `cmd/pleiades/onboard.go`, their tests, and
`cmd/pleiades/generic_release_gate_test.go`. Changed: the file inventory (sidecar discovery, the
refusal, the start state), `record.Base`, the sync reconciler, `add-host`, the API's device create,
the web UI device form (start state, and naming a type that needs properties), `internal/auth`
(scope, relation), `internal/apispec`, `cmd/controller`, `internal/clispec`, `tools/gendocs`
(generic rows), `internal/archtest`, `coverage-floor.json`, docs 01, 02, 10, 11 and CLAUDE.md, generated
references, three changelog fragments, FAILURE_PATTERNS 345 and 346, LESSONS 239. Gitignored:
`.SPECIFICATION/IMPLEMENTATION.md` (Phase 111), `.SPECIFICATION/SECURITY_ATTESTATION.md` (PW.9).
