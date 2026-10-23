---
status: beta
---

# Verifying a network change

A routing change that looks right can still black-hole a subnet. The Pleiades checks a change in three
stages, in this order:

1. **The model.** `net.batfish.check` holds the candidate configurations to
   [Batfish](https://www.batfish.org/)'s model of the network before anything is applied. It needs
   only the configurations and a Batfish service, whatever the network runs on: real devices,
   containerlab, GNS3 or CML2.
2. **A rehearsal** on lab devices running the same OS image as production. A model of an FRR
   router says nothing about a Cisco one.
3. **The change**, in a block with `rollback_on_failure: true`, verified live against the model it
   passed. If the verification fails, the block undoes the change by itself.

A model that agrees is evidence, not proof. Batfish models steady state from configuration text. It
does not model runtime state (whether an IP SLA probe's target answers, how fast BFD notices a
failure), nor the features listed under [what the model sees](#what-the-model-sees). That is why
stages 2 and 3 still run after a model passes.

`pleiades doc --create-skill` teaches an agent these stages, together with three rules. An agent
never starts Batfish, or deploys or destroys a lab, without the person's say-so. A missing service
is reported, never skipped. And an unmodelled line is accepted only with a reason the person
would agree with.

## The devices

| Device | Type | How it is reached | What grants its methods |
| --- | --- | --- | --- |
| An FRRouting router | `generic_ssh` | SSH, as an unprivileged account allowed to use `vtysh` (the `frrvty` group) and to write FRR's run directory, `/var/run/frr`, where `frr-reload.py` writes its reload files (for example `chgrp frrvty /var/run/frr; chmod 2775 /var/run/frr`) | `pleiades onboard` grants `FRRCapable` only after `vtysh -c 'show version'` succeeds as that account; `net.frr.config` checks the write access itself before any change, in a check too |
| An Open vSwitch switch | `generic_ssh` | SSH, as an account allowed to use the OVS database | `pleiades onboard` grants `OpenVSwitchCapable` only after `ovs-vsctl show` succeeds as that account |
| A Batfish service | `batfish` | Its REST API (`api_base`, e.g. `http://127.0.0.1:9996`) | Declared by the type: `BatfishAPICapable` |

Each grant proves the account is allowed, not just that the program is installed. A router whose
`vtysh` the account may not run is not granted `FRRCapable`.

Batfish has no authentication. A `batfish` device therefore reaches it over plain HTTP only at a
loopback address, unless its record sets `batfish_allow_plaintext: true` for a private network you
have decided to trust. Every run against such a device then warns. Over HTTPS, the device's own TLS
settings apply (`tls_ca_pem`, `tls_server_name`).

```console
pleiades add-host batfish --type batfish --set api_base=http://127.0.0.1:9996
```

A containerlab lab's nodes come in with one command. It reads the `topology-data.json` containerlab
writes on deploy, and refuses any node that is not the named lab's:

```console
pleiades import-containerlab --topology clab-routing-lab/topology-data.json --lab routing-lab \
  --username netops --key keys/id_ed25519 --trust-first-connect --onboard
```

Each node becomes a `generic_ssh` device at its published SSH port, or else at its management
address. `--trust-first-connect` trusts whatever host keys answer, unverified, as
`trust-host --first-connect` does. A lab makes new host keys on every deploy, at the same addresses, so
they go in a file of the lab's own, never your `~/.ssh/known_hosts`: the import refuses
`--trust-first-connect` unless `PLEIADES_KNOWN_HOSTS` names one (`export PLEIADES_KNOWN_HOSTS=$PWD/known_hosts`),
and every later command reads the same variable.

## Running Batfish

Batfish runs as a container on a machine you choose; this runbook runs it on the machine `pleiades`
runs on, through a `container_host` device with no `host` (its local engine socket). Its API is
published on loopback only, since Batfish has no authentication, and it is held to 2 GiB, since the image
otherwise sizes its heap to the machine.

```yaml
# What this does: runs the Batfish service net.batfish.check asks, its API on 127.0.0.1:9996 only.
# Blast radius: this machine's container engine; one new container, batfish. No device is touched.
# Changes: pulls the pinned image if absent, creates and starts the container.
# Undo: pleiades rollback <run-id> removes it.
id: batfish_up
hosts: dockerhost
tasks:
  - name: Batfish runs, its API on loopback only
    container.run:
      name: batfish
      image: batfish/batfish:2026.08.27.3685
      network: bridge
      ports: ["127.0.0.1:9996:9996"]
      memory: 2g
      memory_swap: 2g
```

```console
pleiades add-host dockerhost --type container_host
pleiades onboard dockerhost
pleiades run runbooks/batfish_up.yaml
until pleiades adhoc batfish http.check url=http://127.0.0.1:9996/v2/version status_code:=200 >/dev/null; do sleep 5; done
pleiades add-host batfish --type batfish --set api_base=http://127.0.0.1:9996
```

Batfish takes a minute or two to answer after it starts; the loop above waits for it. An agent never
runs this without the person's say-so.

## The methods

| Method | What it does | Check | Undo |
| --- | --- | --- | --- |
| `net.frr.facts` | A router's version, interfaces, OSPF neighbours, routes and BGP peers, and with `include_config` its running configuration, every secret masked | yes | read only |
| `net.frr.command` | One `show` command; anything else is refused before the router is reached | yes | read only |
| `net.frr.config` | The whole intended configuration, applied through FRR's own `frr-reload.py` | yes | restores the backup it took |
| `net.frr.interface` | An interface administratively up or down | yes | the state it found |
| `wait.routes` | Waits until the routing table holds the routes given, through the next hops given, and has stopped changing | no (a wait) | read only |
| `net.trace` | `tracepath` from a Linux device, failing when the path is not the one expected | yes | read only |
| `net.ovs.bridge`, `net.ovs.port` | A bridge and its datapath, a port and its VLAN tag | yes | the state they found |
| `net.batfish.check` | The candidate held to the model, before any change | yes | read only |

`pleiades doc <method>` prints each one's full contract.

### Applying a configuration

`net.frr.config` takes the whole intended configuration: `config`, or `src`, a file in the project.
A configuration line not in it is removed from the router. Before the router is touched:

- the candidate is checked with `vtysh -m -f`. A line vtysh refuses fails the task, because
  `frr-reload` reads a file vtysh cannot parse as an empty configuration and would delete
  everything;
- `expect_sha256` refuses a candidate whose bytes are not the ones the gate checked.

A real run then saves the running configuration to a backup on the router (mode 0600, under the
connecting account's `~/.pleiades-frr`), reloads, and confirms the router now runs the candidate as
written. FRR silently changes some lines. It keeps only the first eight characters of an OSPF simple
authentication key, for example. A candidate FRR alters is a failure that names the lines, and its
undo is kept.

The undo names only the backup's path, so no configuration text (and no secret) reaches the
journal. It restores exactly what was running before the change. It refuses when the router has
been changed since, unless the undo's step sets `force`. With `save: true`, `frr.conf` is written
too, keeping its owner and mode, and the undo puts it back.

## The gate

`net.batfish.check` runs on the `batfish` device. It takes each device's whole configuration: the
running one (a registered `net.frr.facts` result's `running_config`), or the candidate as a project
file. Every secret is masked before anything is sent. Equal secrets get equal placeholders, so a key
mismatch between two routers still shows.

What it asserts:

| Assertion | Holds when |
| --- | --- |
| `ospf_established: true`, `bgp_established: true` | every session of that protocol the model finds is established, and there is at least one |
| `route: {node, prefix, next_hops, protocol}` | the node has a route to the prefix, through those next hops, by that protocol |
| `no_route: {node, prefix}` | the node has no route to the prefix |
| `reach: {from, dst, delivered, through, avoids, accepted_by}` | a flow from a node, or `node[interface]`, to an address is delivered (or not), every path goes through or around the nodes named, and `accepted_by` names the node that answers, such as an HSRP or VRRP group's elected owner of a virtual address |
| `routes_match: {node, routes, ignore}` | the model's routes for the node equal the routes `net.frr.facts` read from it live, compared by prefix, protocol and next hop for connected, static, OSPF, BGP, IS-IS, EIGRP and RIP routes; `ignore` lists prefixes no configuration holds, such as a management network |
| `question: {name, variables, where, min_rows, max_rows}` | any Batfish question (filters, peers, first-hop redundancy properties) returns between `min_rows` and `max_rows` rows whose cells equal `where` |

A scenario is a fork of the snapshot with interfaces or nodes down (`down_interfaces: ["r1[eth2]"]`,
`down_nodes: [r3]`), checked against its own assertions. That is how a link failure is checked
before it happens.

It fails closed in five cases:

- an assertion does not hold;
- a file did not parse;
- a question could not be answered;
- Batfish reports a convert error, so part of a device is missing from the model, and the task did not accept it by its text;
- a line or feature the model does not use was not accepted by name.

FRR in a container writes `domainname localdomain` into its running configuration, which Batfish does not
recognise, so a gate over such routers accepts it: `{text: domainname localdomain, reason: "the DNS search domain,
not routing"}`.

The model's result reports three things separately:

- **`parse`:** each file's parse status;
- **`assertions`:** whether the model agrees with each assertion;
- **`not_modelled`:** what the model ignores, each with its reason.

Each configuration's SHA-256 comes back too, for `net.frr.config`'s `expect_sha256`.

## What the model sees

Batfish recognising a line does not mean it models the line. The table below was measured against
the Batfish release this project pins (2026.08.27), not taken from documentation. A line or feature
in the last two rows refuses a gate until the task accepts it in `accept_unrecognized`, by `text`
for a line or by `feature` for a feature, with a reason.

| | FRR | Cisco IOS |
| --- | --- | --- |
| Modelled | connected and static routes, OSPF (including passive interfaces), BGP | the same, HSRP with interface tracking (failover included), VRRP |
| Reported unrecognised | `ip ospf authentication-key` (so a key mismatch is not modelled), `no bgp ebgp-requires-policy`, VRRP | |
| Parsed, then ignored | | GLBP (no forwarder is elected), IP SLA, SLA tracking (an SLA-tracked route never fails over), BFD |

The configuration formats taken are `frr` and `cisco_ios`. Batfish reads Arista EOS and Junos as
they are too. They will be accepted once their device types exist and each format is proven against
real devices.

## An example

```yaml
# What this does: checks r3's candidate against the model, then applies it and verifies it live.
# Blast radius: r3's routing; r1 and r2 are read only.
# Changes: r3's running configuration and frr.conf.
# Undo: the block undoes the change itself if any step after it fails; pleiades rollback undoes it later.
id: r3_change
hosts: r3
metadata:
  service_effecting: true
tasks:
  - name: Read r1
    net.frr.facts: {target: r1, include_config: true}
    register: r1
  - name: Read r2
    net.frr.facts: {target: r2, include_config: true}
    register: r2
  - name: The model routes both LANs with r3's candidate, and without r1's link to r3
    net.batfish.check:
      target: batfish
      devices:
        - {name: r1, config: "{{ result.r1.running_config }}"}
        - {name: r2, config: "{{ result.r2.running_config }}"}
        - {name: r3, src: configs/r3.frr.conf}
      assertions:
        - ospf_established: true
        - route: {node: r1, prefix: 10.3.3.0/24}
      scenarios:
        - name: r1-r3 down
          down_interfaces: ["r1[eth2]"]
          assertions:
            - reach: {from: "r1[eth3]", dst: 10.3.3.10, through: [r2]}
    register: gate
  - name: Apply and verify, undone if the verification fails
    rollback_on_failure: true
    block:
      - name: r3 runs the checked candidate
        net.frr.config:
          src: configs/r3.frr.conf
          expect_sha256: "{{ result.gate.sha256.r3 }}"
          save: true
      - name: r1 routes r3's LAN through r3 again
        wait.routes:
          target: r1
          prefixes: [10.3.3.0/24]
```

## Limits

- A run holds a device only while one of its tasks works on it, so nothing stops another run changing a
  neighbouring router while a change is verified. The verification notices only what it checks; run one
  change to a network at a time.

- `src` is read by the command line only. A Walk-tier Runner runs each task against its one
  dispatched device, so a gate that reads several routers' registered facts runs on the command line
  until segmented dispatch is built.
- Masked placeholders are equal for equal secrets within one command-line run. A Runner reads each
  device separately, so there they compare only within one router.
- `net.trace` claims Linux only, and needs iputils `tracepath`.
