# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 110 (connection persistence) and Phase 111 (generic device types) are committed and pushed on
`feature/playbook-migration` (push gate passed at `6530e2e`).** On 2026-09-25 the user answered the open
decisions: put the Walk-tier fix in Phase 111, run `make ci`, leave versioning, and build TLS and mutual
TLS for `generic_http`, with deprecated TLS allowed only explicitly, loudly, and never by default. That
follow-up is built on branch `phase-111b` (from `6530e2e`) and described below; the previous status
(Phase 111 itself) is in the archive.

### What the follow-up is

- **Device TLS (`pkg/devicetls`)**, used by `generic_http`, `generic_grpc`, both onboarding probes and
  `http.request`'s device mode: `tls_ca_pem` (a pinned authority instead of the system roots),
  `tls_server_name`, `tls_min_version` (1.2 default, 1.3 allowed), and `tls_client_certificate` (mutual
  TLS from the stored certificate and key, or a PKCS#12 bundle). Certificates are always verified.
- **Weakening, only per device, each behind its own flag, each warned on every use** (the user's rule;
  saved as a memory): `tls_allow_deprecated_versions` for TLS 1.0/1.1; the separate
  `tls_allow_legacy_ciphers` for 3DES, RC4 and RSA key exchange (the user's choice for the second tier;
  SSL 3.0 is impossible in Go); and `http_allow_plaintext_credentials` for a credential over `http://`,
  whose warning says to rotate it (the user reversed "keep refusing" to "explicit allow"). A flag that
  allows nothing is refused. `generic_grpc` refuses both TLS weakenings (HTTP/2 forbids them) and still
  refuses a credential over plaintext. `http.request`'s device mode refuses `validate_certs: false`.
- **Warnings reach the person:** `sdk.StatWarnings`, printed by `pleiades run` always; a `task.warning`
  job-log event on the Walk tier; `onboard.Result.Warnings` on the CLI and the API.
- **Downgrade proof first, as the user asked:** a 35-handshake matrix in `pkg/devicetls`, then the HTTP
  probe, the gRPC probe and `http.request` each shown to refuse a TLS 1.0-only or 3DES-only server by
  default and reach it only with its flag, a modern server still negotiating TLS 1.3; every such test
  mutation-checked. `TestOnlyDevicetlsLowersTLS` keeps every weak version and suite name in
  `pkg/devicetls`.
- **Walk tier:** a Runner rebuilds each dispatched device as its real type (`record.LookupType`) from
  `wire.DispatchPayload.DeviceType`/`DeviceProperties`, which carry only the keys the type declares its
  accessors read (`record.RegisterDispatchProperties`, one small file per device package) plus its
  discovery. `TestDispatchPropertiesAreWhatTheCodeReads` holds each package's declaration equal to what
  its code reads and refuses a secret-named key (two validated exemptions, each self-verifying). The
  forge scaffold emits an empty declaration. An older Controller's payload falls back to the
  address-only device. Found on the way: the Controller's worker would have skipped every generic HTTP
  or gRPC device for having no `host` (FAILURE_PATTERNS 348, LESSONS 240), fixed by falling back to the
  declared address.

### Verification

The Walk-tier gate is `TestGenericWalkReleaseGate_DeviceAccessorsReachTheRunner` (`cmd/runner`),
passing under `-race` with the rest of `cmd/runner` and `cmd/pleiades`. Five touched packages had
fallen below their coverage floors (native, sdk, generic, `catalog/http`, onboard). Tests covering
the new refusals and the warning events bring each back above its floor.

**`make ci` on `6530e2e` failed in its coverage pass, twice.** Every step before coverage passed. Then
`internal/backup`'s Postgres container missed readiness at exactly 60 seconds, on different tests each
time. The package passes alone in 35 seconds. The cause is a test-harness defect, not contention:
FAILURE_PATTERNS 310's fix bounded testcontainers' wait group at two minutes, but each step inside the
group kept the library's own 60-second default, so the bound never applied (FAILURE_PATTERNS 350,
LESSONS 243). Fixed on this branch for Postgres, Toxiproxy, LocalStack and one SSH gate, with a test
that reads each step's own timeout.

**The rerun on `e3d84b9` failed differently.** `cmd/runner` hit go test's 30-minute timeout, because a
test helper read an SSH banner with no deadline from a port Docker's proxy had accepted
(FAILURE_PATTERNS 351). It is fixed by `testsupport.CaptureHostKey`, which bounds each attempt and
retries. The same run's `internal/event` failure, and one more in the next run, were a broker whose
published port refused connections after it said it was ready (FAILURE_PATTERNS 353). In one run it
stayed refused for two minutes, so this is Docker Desktop's port forwarding going dead, cause not
diagnosed, not a short lag. `StartNATS` now waits for the port to answer with a NATS greeting, and a
port that never does fails in the harness, naming it. That is a clearer failure, not a cure.

**Security finding, measured and fixed (FAILURE_PATTERNS 352).** Found by checking the other callers
of the same SSH call. Through a bastion, the handshake with the device had no bound:
`ssh.NewClientConn` takes no context, a tunneled connection supports no deadline, and
`ssh.ClientConfig.Timeout` covers only `ssh.Dial`. So anything answering on a device's address behind
a bastion could hold a run, or a Runner's task and its device lease, for as long as it liked. It only
had to accept the connection and send nothing, with no credential. `pkg/remoteexec`'s direct dial
already had the guard. Both paths now share it (`closeOnDone`, `handshakeContext`), and
`TestConnect_HopChain_ASilentTargetIsBounded` returns at its context's two seconds where before it hung.
No upstream fix applies. Recorded in the vulnerability corpus as new class C15, unbounded wait on a
peer. What happens next with it (a tracked issue, anything further) is the user's decision.

Also fixed on the way: the Runner's per-dispatch executor had lost its check that a call's device is
the dispatched one, when the address-only type check was removed. A bound executor now refuses any
other device by name, and a nil one, before a child starts.

### Attestation evidence hunt (2026-09-25)

The roadmap's attestation checker reported four security problems: finished Fuzz/Stress,
Schema/Injection and Release Gate items citing nothing it could check (Phases 20, 53, 54, 55, 57, 74a,
79, 83, 96a, 96b, 96c). Each item now carries a dated evidence line in the tracker, and the checker
reports zero. Tracing them found real gaps, recorded as FAILURE_PATTERNS 349 and LESSONS 241 and 242:

- **Phase 96c's Release Gate is re-opened.** No test runs one dispatch, severed and healed over real
  NATS and Toxiproxy past the old two-minute window, and counts it delivered exactly once. The halves
  exist separately (the admission check on an in-memory store; 96a's recovery with no dedup).
- **Two ticked items were split** into open items for their unbuilt halves: 96c's dedup-table cost
  benchmark, and Phase 79's fuzzing of the sign-in email (only the password is fuzzed).
- **Phase 55's "no I/O" claim was false** (`ShiftTimezone` reads the zone database). The claim is now
  exact and enforced by `TestFiltersDoNoNetworkFileOrProcessIO`.
- **Phase 20's Helm audit, recorded after the fact:** four chart values are interpolated unquoted into
  `nats.conf` and the broker StatefulSet. All came from later phases and all are supplied by the
  installer, so none crosses a trust boundary. No finding.

The tracker cites `TestFiltersDoNoNetworkFileOrProcessIO` from four phases, so those citations read as
missing until this branch lands.

### Decisions for the user

1. Versioning stays as proposed (the user's answer, 2026-09-25).
2. The remaining TLS clients (WinRM, Catalyst Center, a full-URL `http.request`, Git over HTTPS) keep
   their current fixed settings; the device-TLS model can extend to them when wanted.
3. When to build what the hunt re-opened: 96c's exactly-once release gate (a real NATS and Toxiproxy
   test that runs past two minutes), 96c's dedup benchmark, and 79's email fuzz.
