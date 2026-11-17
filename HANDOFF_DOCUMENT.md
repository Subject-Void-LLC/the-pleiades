# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-17. Branch `docs/records-2026-11-17` (records only, from `main` at `88fcb3b4`). Merged since the last handoff:
#166 to #191; this run's were #183 (SNMP), #184 (TLS key log), #185 (gNMI client certificate and target), #186 (pcapsum
transport encryption), #187 (NETCONF assurance), #188 (durations guard), #189 (`net.yang.schema`), #190 (brownfield TLS,
Phase 143), #191 (RESTCONF session cap). Next PR: `chore/cml-vpn-auto-accept`.**

### What works

- Before merging, `make pr-check` passed and `make branch-containers` passed the changed container packages for #186
  (066f1e9e, 6 packages), #187 (45b6c88d, 33), #189 (b4162a15, 36), #190 (f183f412, 39) and #191 (e23e6bf8, 14).
- On real IOS XE 17.15 (CML Catalyst 8000V): NETCONF and RESTCONF facts, config, re-run and rollback; `net.yang.schema`
  over NETCONF (676 sources) and over RESTCONF (674 sources, once #191 waited out the session cap; the 673 both protocols list byte-identical). `net.ios.command` on
  IOL XE 17.15.1 (11 commands on 4 nodes).
- `TestEveryContainerPackageHasADuration` holds `durations.json` to the container list on every branch (FAILURE_PATTERNS
  1253).
- Records: FAILURE_PATTERNS 1253 to 1269, LESSONS 447 to 453.

### Blocker

- None for the queue. NX1 (nxosv9000 in CML) booted four times and never answered on mgmt0; `pleiades` cannot read a
  non-IOS console, so it waits on the owner's look at its console in the CML UI.

### Open

- CML defects and gaps, eighteen found on real devices, each for a fix PR (no known inconsistency left open). Recorded
  as failure patterns only where they repeat one: 1268 (over-masking, a repeat of 640) and 1269 (failed reads taint
  rollback, a repeat of 650). The rest are work items: ioll2-xe seeding leaves Ethernet0/0 a switchport; a large
  `virt.cml.capture.stop` fails; the run counter's lock and the run lock refuse parallel adhoc reads and rollback;
  `net.trace` drops asymmetry and passes an all-`*` path; `net.pcap.summary` lacks DSCP, duration and bytes per flow; a
  wrong `tls_fingerprint` drops discovered capabilities; a failed onboard stays `onboarding`, and a credential stored
  before its host stays `unbound`; `is defined` does not compile in `when:`; validate's `literal_secret` warns on `aaa
  authentication login default group ... local` and on a word after a secret reference; an `always:` cleanup needs
  `--leave`; `net.ios.config`'s check places a global line inside an interface and cannot see IOL VLANs;
  `wait.connection` cannot pass a host key that changes every boot; each adhoc command is a fresh SSH login, filling the
  device's log. Gaps beside them: no controller headroom from `virt.cml.*`, no copy of a stored credential to a second
  record, no revert-timer apply for `net.ios.config`, no IPFIX/NetFlow decoder (Phase 145), no console read for non-IOS
  CML nodes, no NX-OS seeding.
- FAILURE_PATTERNS 1267: `internal/api/device_facts_test.go` still builds its routes by hand.
- Roadmap: Phase 144 (core, v0.6.0), 145 (QoS, v0.7.0), 146 (health catalogue, v0.7.0); Phase 91 split into 91 (v0.4.0),
  91b (v0.8.0) and 91c (v0.10.0).
- Owner decisions: put-back undos journaling device text (NETCONF and SNMP, FAILURE_PATTERNS 1257); `inventory:assert`
  for first use (built, awaiting his word); DSCP CS3 or CS5 for call signaling; the Groundconfig QoS template findings;
  Phase 144's question list; NX1's console.

### Next steps

1. Commit these records, gate and merge them.
2. PR `chore/cml-vpn-auto-accept`.
3. Fix PRs for the CML defects, starting with 1269 and 1268, proving each on the CML lab while it is up.
4. Tear the CML lab down before 2026-11-18 22:00Z.

### Files changed

`FAILURE_PATTERNS.md`, `FAILURE_PATTERNS_ARCHIVE.md`, `LESSONS_LEARNED.md`, `LESSONS_LEARNED_ARCHIVE.md`,
`HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Lab: the CML lab `pleiades-proofs` is up, with NX1 left running (run `fdeed9a6`) for the owner; tear it down before
2026-11-18 22:00Z by rolling back `fdeed9a6` and then running the proofs teardown runbook. The local VMs were not part
of this run.
