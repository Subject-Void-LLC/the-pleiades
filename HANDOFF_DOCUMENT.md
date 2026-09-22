# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 96d (path traversal and wire confidentiality) is COMPLETE and COMMITTED on branch
`feature/Path-Traversal-and-Wire-Confidentiality`, cut from `3b60a80` (Phase 84's merge), pending
`make ci` and a push.** The roadmap reads 10 of 10 with no open gate, and 96d is no longer in the
tracker's `phases_without_implements`. The user explicitly asked for this branch to be committed and
pushed, which is why the standing do-not-commit rule does not apply to it.

Four commits, deliberately separate rather than one bundle:

1. `feat(topology): prove wss:// traversal and validate the mesh URL` (Phase 96d itself)
2. `fix(e2e): stop the upgrade gates asserting on state that is absent` (two Phase 84 test defects
   this branch surfaced; the user approved them riding along)
3. `docs(readme): describe the work that shipped and drop an em dash`
4. the living documents

**`commitgate` refuses a `Co-Authored-By` trailer naming an AI**, citing AGENTS.md's Licensing
section on intellectual-property ambiguity. The session's own attribution instruction asked for one;
the project rule wins and the trailer is absent from every commit here. Expect this on every future
session.

**The roadmap resequencing is NOT in these commits and cannot be**: `.SPECIFICATION/` is gitignored,
so `IMPLEMENTATION.md` and `SECURITY_ATTESTATION.md` changes live only in this working copy. See
"Roadmap and spec changes" below for what was done there, because a fresh clone will not have it.

### What this session did

96d was 7 of 10 when it started. The three open items were one claim: a direct listener does not
prove traversal. Closing them turned up four more things inside the phase's own territory sitting
under items already ticked `[x]`, and the user asked for all four to be folded in.

**The Release Gate (`tests/e2e/mesh_wss_release_gate_test.go`).** A real `nats-server` with a real
`websocket {}` block and no host route to its client port, a real nginx terminating real TLS in
front of it, and the real `cmd/controller` and `cmd/runner` reaching it over `wss://` with
`NATS_CA_FILE`. Passes under `-race` in about 50s. Four layered proofs: real work completes; the
broker publishes no bypass route (asserted against the live container); nginx logs a real `101
Switching Protocols` carrying `upgrade="websocket"` and `proto=HTTP/1.1`; and the broker's own
`/connz`, fetched through the proxy, reports all seven client connections as type `websocket` from
the proxy's address. Two negative controls: a raw `tls://` dial at the same address must fail (a
proxy that answered it would be forwarding bytes, not terminating HTTP), and an unrelated root must
be refused. **Every proof was observed failing under a deliberate inversion before being trusted**,
which is the part worth repeating if any of this is revised.

**Three shipping changes**, not just tests:

1. `topology.ValidateNatsURL` and `TLSFromEnv` moved into the configuration block of both
   composition roots. They used to sit ~280 lines past the env read, past `net.Listen`, past the
   `controller listening` line and past the schema migration, so a typo'd scheme bound a port,
   answered 200 on `/healthz`, migrated a database and then exited 1 (FAILURE_PATTERNS 297). The
   shipped changelog already claimed "checked at startup"; it is true now.
2. `tlscert.ClientConfig(roots)` added as the one place a client `tls.Config` is written.
   `TLSFromEnv` and the Vault lookup both consume it. The phase's first commit message claimed
   `TLSFromEnv` did this and it did not.
3. The chart gated the broker's certificate volume and mount on `nats.tls.enabled` alone while the
   rendered `nats.conf` named the files unconditionally, so `nats.websocket.tls` without
   `nats.tls.enabled` produced a manifest that applies cleanly and a broker that dies on a file
   nothing mounted. Both are now gated on `or tls.enabled websocket.tls`.

**New guards.** `internal/archtest/tlsconfig_test.go` (zero `tls.Config` literals under
`internal/topology`, with `internal/tlscert` as a positive control so the zero assertion cannot pass
vacuously; plus a module-wide `MinVersion` ratchet, no allowlist). `tools/helm-lint`'s
`checkConfiguredFilesAreMounted` (every file path a mounted configuration names must fall inside one
of that container's mounts) plus the profile that renders the combination: neither half finds the
chart defect without the other, and both were verified against the unfixed chart.

**The benchmark the Fuzz/Stress item asked for and never got.** `BenchmarkTransportConnect`, four
schemes rather than two so the cost decomposes: `nats` 609us/145 allocs, `ws` 615us/190, `tls`
1245us/577, `wss` 1207us/625. The "strictly more work" claim holds, and the decomposition corrects
what the prose implies: almost none of it is the WebSocket upgrade, the TLS handshake is essentially
all of it.

### Two findings worth reading before touching this again

- **`ValidateNatsURL` must stay AFTER the masking logger.** Its messages quote the URL back,
  `internal/redact` has a `url_userinfo` rule for `scheme://user:pass@host`, and in both roots the
  env read happens before `slog.SetDefault` and `log.SetOutput(redact...)`. Moving the check "up
  beside the env read" looks like a harmless cleanup and prints passwords on a fatal path
  (FAILURE_PATTERNS 298).
- **An empty `ExposedPorts` publishes MORE, not fewer.** testcontainers inspects the IMAGE and
  publishes everything its `EXPOSE` declares. The nats image declares three ports, so the gate's
  "no other way in" fixture initially published all of them. `tests/e2e/integration_chaos_test.go`
  had carried the same claim as a comment, untrue, for months; corrected in this change
  (FAILURE_PATTERNS 299).

### Roadmap and spec changes (gitignored, this working copy only)

- **v0.4.0 is now the security release.** Phase 103a and Phase 105 moved there from v0.8.0, and 45
  phases cascaded down one version to make room. The cascade STOPS at v0.9.0, which absorbed
  v0.8.0's remainder: v1.0.0 holds the Secure Development Compliance block and is a real boundary,
  not a number. Phase 101 deliberately stayed at v0.3.0 because it is over half done.
- **Phase 105 carries the zero-trust linkage**, including a publication embargo: no documentation,
  datasheet or sales material may call this platform zero trust until 105 merges, because until then
  a compromised broker yields every credential dispatched inside the retention window (seven days at
  the default outage budget, 168 at the maximum). Also records why `needed_by` stays empty: the
  tracker DERIVES it from other phases' `Depends on:` lines, so do not manufacture a fake dependency
  to populate it.
- **Phase 105's Pattern Entry Gate now settles the payload question**: `Secrets` and `Injected` leave
  the SERIALIZED form, not the Go struct. The Runner repopulates them after redeeming. That holds the
  blast radius to about six sites instead of the 61 that reference those fields, and leaves the
  Crawl tier, which has no broker, untouched.
- **SSDF PW.9 was split** so 96d ticks what it proved and "encrypted and authorized BY DEFAULT" stays
  open against Phase 101 and 106d.

### Verification run

Green: `go build ./...`, `make fmt`, `vet` under both tag sets, `tidy-check`, `gosec` (22 findings,
all pre-existing and individually waived), `govulncheck` (0 reachable), `docs-lint`,
`docs-gen-check`, `helm-lint`, `templ-gen-check`, and the wss gate under `-race`.

`make test-race`: one failure, `internal/backup`, classified as contention by the isolation re-run
(60s failing under load, 3.8s alone, 36s for the whole package alone). Not stash-baselined.

`make test-integration`: 166 packages green. Three real failures, all in tests rather than the
product, two fixed here and verified by re-running each gate alone, one recorded and left alone
(FAILURE_PATTERNS 301, a check test in `internal/catalog/file/line` comparing an mtime for equality,
which is #294 recurring in a package this branch does not own).

**Still to run: `make ci` in full**, which is the remaining step before this is verified, and which
on this machine has to run alone.

### Next

The tracker's repo-wide `next_item` is Phase 101 (Mesh Identity: NKey/JWT, subject-scoped
authorization), which is the honest successor: 96d encrypts the wire and authenticates the server,
and deliberately does not authenticate clients. `SECURITY_ATTESTATION.md`'s SSDF PW.9 was split to
say exactly that: what 96d proves is ticked, and "encrypted and authorized BY DEFAULT" is left open
against 101 and 106d.

### Commit message

```
feat(topology): prove wss:// traversal and refuse a bad mesh URL at startup

Phase 96d's last three items, which were one claim: a direct listener does
not prove traversal. The existing gate reached a real broker over ws:// and
tls://, but dialed it directly from the test process, which says the client
speaks the protocol and nothing about the thing wss:// exists for.

tests/e2e now stands a real nginx terminating real TLS in front of a real
nats-server with a real websocket block, and drives it with the real
controller and runner binaries over wss://, under -race. Four layered
proofs: work completes, the broker publishes no route this host could use to
bypass the proxy, nginx logs a real 101 Switching Protocols carrying the
Upgrade header and HTTP/1.1, and the broker's own /connz reports all seven
client connections as type websocket from the proxy's address. Two controls:
a raw tls:// dial at the same address must fail, since a proxy that answered
it would be forwarding bytes rather than terminating HTTP, and an unrelated
root must be refused. Each proof was watched failing under a deliberate
inversion before being trusted.

NATS_URL was checked at the dial, roughly 280 lines past where it is read
and on the far side of net.Listen and the schema migration, so a typo'd
scheme bound a port, answered 200 on /healthz, migrated a database and then
exited 1. Both composition roots now check it while reading configuration.
It goes after the masking logger rather than beside the env read, because
the validator quotes the URL back and a URL can carry a password.

tlscert.ClientConfig is now the one place a client tls.Config is written,
consumed by TLSFromEnv and by the Vault lookup, and an archtest requires
zero literals under internal/topology with internal/tlscert as a positive
control. A module-wide rule requires every tls.Config anywhere to state a
version floor, since the zero value is TLS 1.0.

The chart gated the broker's certificate volume and mount on
nats.tls.enabled while the rendered nats.conf named those files whenever
nats.websocket.tls was set, so serving wss:// with a plaintext in-cluster
listener produced a manifest that applies cleanly and a broker that dies on
a file nothing mounted. Both are gated on either now, and helm-lint gained a
general rule that every file a mounted configuration names must be inside
one of that container's mounts. externalNats.url gained the scheme pattern
the first commit named as a defect and did not fix.

Benchmarked rather than asserted: wss:// costs about twice what nats:// does
per connect, and almost none of that is the upgrade. The TLS handshake is
essentially all of it, so tls:// and wss:// measure the same.

Recorded: FAILURE_PATTERNS.md 297, 298 and 299, LESSONS_LEARNED.md 216 and
217. 299 is worth reading before writing another container fixture: an empty
ExposedPorts publishes every port the IMAGE declares, so "name nothing to
publish nothing" is backwards, and a sibling test had carried that claim in
a comment, untrue, for months.
```
