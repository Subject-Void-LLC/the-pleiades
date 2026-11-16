---
status: beta
---

# SNMP

Some devices speak nothing but SNMP: PDUs, console servers, satellite modems, UPSes and plenty of
older switches. The Pleiades reaches them as `generic_snmp` devices and reads and writes them with
four methods: `net.snmp.facts`, `net.snmp.get`, `net.snmp.set` and `wait.snmp`. They run on the
Crawl tier (`pleiades run`, `pleiades adhoc`) and on a Runner.

## SNMPv3 by default; v1 and v2c only when the device asks for them

SNMPv1 and SNMPv2c have **no authentication and no encryption**. Their community string is a
password sent in the clear in every request, and nothing proves which agent answered. Some devices
offer nothing else, so The Pleiades supports them, but only when the device's record says so:

```yaml
hosts:
  old-pdu:
    type: generic_snmp
    host: 10.20.0.15
    snmp_version: 2c
    snmp_allow_cleartext_community: true
```

Without `snmp_allow_cleartext_community: true` a record asking for `snmp_version` `1` or `2c` is
refused when the inventory loads. With it, every onboarding and every run warns that the community
crosses the network in the clear. A setting that allows nothing (the flag with SNMPv3) is refused
too, so a record never claims a weakening it does not have.

SNMPv3 with the User-based Security Model is the default, at whichever level the device's user is
configured: `authPriv` (authenticated and encrypted), `authNoPriv` (authenticated, not encrypted) or
`noAuthNoPriv` (neither). Each level below `authPriv` is warned about on every run.

The old SNMPv3 algorithms, MD5 authentication and DES privacy, need their own opt-in,
`snmp_allow_legacy_algorithms: true`, since newer devices have dropped them and the ones that
still use them are the ones that cannot be upgraded. That flag is refused when the user uses no
legacy algorithm.

| Algorithm | Names | Proven against a real agent |
|---|---|---|
| Authentication | `md5`, `sha`, `sha224`, `sha256`, `sha384`, `sha512` | each, against net-snmp 5.9.4 |
| Privacy | `des`, `aes128` | each, against net-snmp 5.9.4 |
| AES-192 and AES-256, Blumenthal key extension | `aes192`, `aes256` | each, against net-snmp 5.9.4 |
| AES-192 and AES-256, Reeder key extension (often called the Cisco variant) | `aes192-reeder`, `aes256-reeder` | each, against net-snmp 5.9.4 |
| Privacy | `3des` | **not supported**: no client here implements it, and net-snmp 5.9.4 does not either |

The two AES key extensions are different protocols on the wire: an agent configured for one does
not answer a client using the other. The tests prove that too. A credential naming `3des` can be
stored, but every SNMP method refuses it before sending anything. Real legacy hardware (an old
Catalyst switch speaking only MD5 and DES, or 3DES) has not been tested yet.

## Adding and onboarding a device

```bash
pleiades add-host pdu-1 --type generic_snmp --set host=10.20.0.16
pleiades add-credential pdu-1 --snmp-user monitor --snmp-auth sha256 --snmp-priv aes128
pleiades onboard pdu-1
```

`add-credential` prompts for the passphrases, or reads them one per line with
`--snmp-passphrase-stdin`. Use `--snmp-community` (or `--snmp-community-stdin`) for a v1 or v2c
device instead.

Onboarding reads the agent's system group with the device's own credential and grants
`SNMPCapable` only when the agent answers it. It records the version, the security level,
`sysDescr`, `sysObjectID`, `sysName`, the engine ID and the MIB groups the agent answers. The
discovery holds only for the host, port and version it was made against: change any of them and
the device must be onboarded again.

SNMP runs over UDP, which a route of SSH hops does not carry, so an SNMP method refuses a device
whose record names a route rather than reach its agent some other way.

A device's record can also set `snmp_port` (161 unless set), `snmp_timeout` (seconds, 5),
`snmp_retries` (1), and, for an agent with quirks, `snmp_no_bulk: true` (walk with GETNEXT only),
`snmp_max_repetitions` (GETBULK's size, 10) and `snmp_min_interval_ms` (the least time between two
requests).

## The methods

- `net.snmp.facts` records the system group, the engine ID and boots, and the interface table. It
  records them as facts only when SNMPv3 authenticated the answers. Over a community or a
  noAuthNoPriv user, nothing proves which agent answered, so they are stats only, and the run
  says so.
- `net.snmp.get` reads OIDs in one GET, or walks each one's subtree, bounded by `max_results`.
- `net.snmp.set` writes one object. It reads the object first, so a check predicts the change
  and a run that would change nothing sends nothing. After the SET it reads the object again,
  because an agent can accept a write and drop it. IOS XE 17.15.1 did exactly that, accepting
  SNMPv3 users it never created. It records an exact undo (the value it read before), so
  `pleiades rollback` puts the object back. Objects under `snmpModules` (`1.3.6.1.6.3`), where
  an agent keeps its own users, keys and communities, are refused, since their values would ride
  the undo and the run's journal.
- `wait.snmp` waits until the agent answers its credential. An SNMPv3 refusal (an unknown user,
  a wrong passphrase) ends the wait at once. A wrong community cannot: an agent drops such a
  request without a word, so it looks exactly like an agent that is not up yet.

```yaml
- name: Name the device's location
  net.snmp.set:
    oid: sysLocation.0
    type: octet-string
    value: rack 7, row C
```

## OIDs

An OID is written numerically (`1.3.6.1.2.1.1.5.0`) or by a name from a **small curated table**:
the MIB-II system and interfaces groups, IF-MIB's `ifXTable` columns and the SNMP engine group,
with the instance after the name (`sysName.0`, `ifDescr.2`, `SNMPv2-MIB::sysName.0`). The table is
not a MIB compiler, and a vendor MIB is not loaded: any other object is written numerically, and a
name outside the table is refused before anything is sent.

## What is masked

The device's community and passphrases are masked out of every error, stat and fact, a short
community included where it stands as its own word. Every octet string in the agent's own
community table (`SNMP-COMMUNITY-MIB`) or USM user table is masked whole, whatever it holds.
