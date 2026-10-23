# FRR routing lab: a change gated on Batfish, applied and verified with The Pleiades

This lab is a small OSPF network built with containerlab, driven entirely through the
`pleiades` command line. It walks one routing change on router r3 through the three stages
the Pleiades network-verification guide describes: the model (Batfish), the change inside a
`rollback_on_failure` block, and live verification of the change against the model it passed.

It shows four things, each with captured `--json` reports in `captures/`:

1. A **bad** candidate (r3 stops advertising its LAN) is refused by the model, and nothing on
   any router changes.
2. A **corrected** candidate (a new interface description on r3) passes the model, is applied,
   and the live network is verified against the model.
3. An **injected failure**: the same change is applied again while r1's link to r3 is taken down
   from a separate `pleiades` call. The live verification fails, and the block undoes the change
   by itself.
4. After each change, the routers' running configurations are byte-identical to the baseline
   (same SHA-256), and the model of them still matches the routes each router reports live.

Everything Pleiades did not do, did wrong, or documented wrong is in [GAPS.md](GAPS.md).

## The lab

```
               r2  10.255.0.2
   10.0.12.0/30   \   10.0.23.0/30
     eth1 /        \ eth2
         /          \
       r1 ---------- r3
 10.255.0.1  10.0.13.0/30  10.255.0.3
       | eth3            | eth3
      sw1 (OVS br0)      h2 10.3.3.10   LAN 10.3.3.0/24
       |
      h1 10.1.1.10                      LAN 10.1.1.0/24
```

| Node | Image | Role | How Pleiades reaches it |
| --- | --- | --- | --- |
| r1, r2, r3 | `frrlab/frr-ssh:10.1.1` (`images/frr`, on `quay.io/frrouting/frr:10.1.1`) | FRR, OSPF area 0, point-to-point links, a loopback each, passive LAN interfaces | `generic_ssh`, 127.0.0.1:2201, 2202, 2203 |
| sw1 | `frrlab/ovs-ssh:alpine3.20.10` (`images/ovs`, on `alpine:3.20.10`) | Open vSwitch, a userspace (netdev) bridge `br0` made by `runbooks/switch.yaml` | `generic_ssh`, 127.0.0.1:2211 |
| h1, h2 | `alpine:3.20.10` | hosts on each LAN | not managed (no SSH) |
| Batfish | `batfish/batfish:2026.08.27.3685`, pinned by digest | the model, started by `runbooks/batfish_up.yaml` | `batfish` device, `api_base=http://127.0.0.1:9996` |
| this machine's Docker | | runs the Batfish container | `dockerhost`, a `container_host` with no `host` (the local socket) |

Every node logs in as `netops`, an unprivileged account (uid 1000) holding the lab key
`keys/netops`. On the routers it is in the `frrvty` group, and the image also makes
`/var/run/frr` writable by that group, because `net.frr.config` needs it (GAPS.md, G1).
On sw1 it is in the `ovs` group that owns the OVS database socket.

## Layout

| Path | What it is |
| --- | --- |
| `images/frr/`, `images/ovs/`, `images/build.sh` | Dockerfiles and start scripts for the router and switch images |
| `lab/frrlab.clab.yml`, `lab/configs/` | the containerlab topology, each router's startup `frr.conf`, FRR's `daemons` file |
| `lab/clab.sh` | runs containerlab from its own image (`deploy`, `destroy`) |
| `lab/inject-link-failure.sh` | step 5's fault injection: waits for r3 to run the candidate, then shuts r1 eth2 with `pleiades adhoc` |
| `pleiades/` | the Pleiades project, where every `pleiades` command runs |
| `pleiades/inventory.yaml` | the inventory: r1, r2, r3, sw1 (imported from containerlab), batfish, dockerhost |
| `pleiades/runbooks/` | `switch`, `batfish_up`, `batfish_down`, `baseline`, `change_r3` |
| `pleiades/configs/` | r3's running configuration as read at baseline, and the two candidates |
| `captures/` | the `--json` report of every run below, journals and logs; `captures/attempt1/` is the first attempt, which found G1 and G2 |
| `pleiades/SKILL.md`, `pleiades/AGENT_JOURNAL.md`, `pleiades/.pleiades/`, `keys/` | made when you run it (the skill, the agent's run log, the encrypted credential store and its key, the lab's SSH key pair), and never committed: see `.gitignore` |

## Reproducing it

The lab is built from this directory, and every `pleiades` command runs in `pleiades/`. Batfish needs 2 GiB, so run only one at a time.

```sh
cd examples/routing_lab          # with a pleiades built from this repository on PATH
# Keep the lab's host keys out of ~/.ssh/known_hosts: every redeploy makes new ones (G7).
export PLEIADES_KNOWN_HOSTS=$PWD/lab/known_hosts
```

### 1. Build, deploy and onboard the lab

```sh
ssh-keygen -q -t ed25519 -N '' -C netops@frrlab -f keys/netops   # once; the topology mounts keys/netops.pub
./images/build.sh
lab/clab.sh deploy

cd pleiades                       # the Pleiades project: inventory, runbooks, candidates
pleiades doc --create-skill       # SKILL.md

pleiades import-containerlab --topology ../lab/clab-frrlab/topology-data.json --lab frrlab \
  --nodes r1,r2,r3,sw1 --username netops --key ../keys/netops --trust-first-connect --onboard --replace --json \
  > ../captures/01-import-containerlab.json 2> ../captures/01-import-containerlab.stderr.txt

# batfish and dockerhost are already in inventory.yaml (pleiades add-host batfish --type batfish
# --set api_base=http://127.0.0.1:9996, and pleiades add-host dockerhost --type container_host).
pleiades onboard dockerhost --json > ../captures/02-onboard-dockerhost.json
pleiades validate > ../captures/03-validate.txt

pleiades run runbooks/switch.yaml --json > ../captures/04-switch.json
pleiades adhoc r1 net.trace dest=10.1.1.10 'expect:={reached: true}' --json > ../captures/05-trace-r1-h1.json

pleiades run runbooks/batfish_up.yaml --json > ../captures/06-batfish-up.json   # held to 2 GiB by container.run's memory
pleiades adhoc batfish http.check url=http://127.0.0.1:9996/v2/version status_code:=200 --json \
  > ../captures/06-batfish-ready.json   # repeat until it exits 0: nothing in Pleiades waits for it (G6)
```

What it showed:

- Onboarding proved what each node's account can do (`captures/01-import-containerlab.stderr.txt`):

  ```
  r1 (generic_ssh) over ssh: discovered -> active
    capabilities: FRRCapable, FactGathererCapable, LinuxCapable, POSIXFileSystemCapable, ShellExecCapable
  sw1 (generic_ssh) over ssh: discovered -> active
    capabilities: FactGathererCapable, LinuxCapable, OpenVSwitchCapable, POSIXFileSystemCapable, ShellExecCapable
  ```

- `dockerhost` onboarded as the local engine: `ContainerEngineCapable, DockerCapable, EngineLocalCapable`,
  Docker 29.8.0 at `/var/run/docker.sock`.
- `pleiades validate`: `validate: 5 runbooks checked, no issues found`.
- `switch.yaml` created `br0` (netdev) and its ports eth1 and eth2, each `changed` with a recorded undo.
  The trace from r1 to h1 then reached `10.1.1.10` in one hop, across the OVS bridge.
- Batfish ran as `frrlab-batfish` from the pinned digest (`pulled: false`, it was already local), was
  capped at `209.3MiB / 2GiB`, and answered `http.check` with 200. (The capture was made before
  `container.run` took `memory`, with a `docker update` after the run; the runbook now sets it.)

### 2. Baseline

```sh
pleiades run runbooks/baseline.yaml --json > ../captures/07-baseline.json
```

`baseline.yaml` reads every router with `net.frr.facts` (`include_config: true`) and holds Batfish's
model of those running configurations to the routes each router reports (`routes_match`). The gate
returns the SHA-256 of each configuration it was given, which is the baseline digest (and equals
`sha256sum` of each `running_config` in the report):

| Router | Running configuration SHA-256 at baseline |
| --- | --- |
| r1 | `1a2185dc791d458ff8672e756262b92b6d7ec1e2ab554a3b50531df4edd5976a` |
| r2 | `193c9b962cfa50b5061078fe5bf97724a6156d310fee4450abf26ff1b1ea1afe` |
| r3 | `42d9d3592f1aad6abfbbd89dd0b945338ec38a191bcd62d31ccae494825a73fc` |

```
ospf_established true 6 OSPF sessions established
route true r1 routes 10.3.3.0/24 by ospf through 10.0.13.2
route true r2 routes 10.3.3.0/24 by ospf through 10.0.23.2
route true r2 routes 10.1.1.0/24 by ospf through 10.0.12.1
route true r3 routes 10.1.1.0/24 by ospf through 10.0.13.1
routes_match true r1's 8 routes match the model
routes_match true r2's 8 routes match the model
routes_match true r3's 8 routes match the model
```

Each router's live routes are in the report under `tasks[0..2].stats.routes`. FRR adds
`domainname localdomain` to a container's running configuration, which Batfish does not recognise;
every gate accepts that one line by name with a reason, and the report lists it under `not_modelled`.

The candidates were written from r3's running configuration as read here
(`configs/r3.running.frr.conf`, identical to the report's `tasks[2].stats.running_config`):

```diff
--- configs/r3.running.frr.conf
+++ configs/r3.bad.frr.conf          (r3 stops advertising 10.3.3.0/24)
 interface eth3
  description lan-10.3.3.0
  ip address 10.3.3.1/24
- ip ospf area 0
  ip ospf passive

--- configs/r3.running.frr.conf
+++ configs/r3.good.frr.conf         (a harmless description change)
 interface eth3
- description lan-10.3.3.0
+ description lan-10.3.3.0 to h2
```

### 3. The bad candidate is refused, and nothing changes

```sh
pleiades run runbooks/change_r3.yaml -e candidate=configs/r3.bad.frr.conf --json > ../captures/08-bad-candidate.json
pleiades journal show a5d872f5-6c27-4b98-a112-51dfb4eec66f   # ../captures/08-bad-candidate.journal.txt
pleiades run runbooks/baseline.yaml --json > ../captures/09-after-bad.json
```

`change_r3.yaml` reads r1 and r2, gates r3's candidate with `net.batfish.check` (every router routes
both LANs, flows from each LAN reach the other, and a scenario with the r1-r3 link down), and only then
runs a `rollback_on_failure` block that applies and verifies it.

The run exited 1 at the gate (`a5d872f5-6c27-4b98-a112-51dfb4eec66f`). The evidence, from
`tasks[2].stats.assertions`:

```
as given        | route | false | r1 has no route to 10.3.3.0/24
as given        | route | false | r2 has no route to 10.3.3.0/24
as given        | reach | false | a path ends NO_ROUTE, a path does not go through r3 (from @enter(r1[eth3]) to 10.3.3.10: r1 NO_ROUTE)
as given        | reach | true  | from @enter(r3[eth3]) to 10.1.1.10: r3>r1 DELIVERED_TO_SUBNET
r1-r3 link down | route | false | r1 has no route to 10.3.3.0/24
r1-r3 link down | reach | false | a path ends NO_ROUTE, a path does not go through r2 (from @enter(r1[eth3]) to 10.3.3.10: r1 NO_ROUTE)
```

Proof that nothing changed:

- The task statuses are `ok, ok, failed`; `net.frr.config` never ran (it is in the report's `plan`,
  not in `tasks`).
- The journal holds only the two reads and the failed gate, and `pleiades journal list` says
  `change_r3  3 task(s), 0 changed, 1 failed`:

  ```
  run a5d872f5-6c27-4b98-a112-51dfb4eec66f
      1 tasks[0]                 r1               ran      net.frr.facts
      2 tasks[1]                 r2               ran      net.frr.facts
      3 tasks[2]                 batfish          failed   net.batfish.check (failed at action)
  ```

- The digests read afterwards (`09-after-bad.json`) equal the baseline's for all three routers, and
  every router's routes (prefix and selected next hops) equal the baseline's.

### 4. The corrected candidate passes, is applied, and is verified live

```sh
pleiades run runbooks/change_r3.yaml -e candidate=configs/r3.good.frr.conf --mode check \
  --allow-unchecked wait.routes --json > ../captures/10-good-candidate-check.json
pleiades run runbooks/change_r3.yaml -e candidate=configs/r3.good.frr.conf --json > ../captures/11-good-candidate.json
```

The check predicted `net.frr.config`'s change (`would_change`, adding ` description lan-10.3.3.0 to h2`)
and changed nothing; the two waits cannot be checked and were accepted by name.

The real run (`078bcea2-e9ab-4815-aa7e-d805ea874f82`) completed with exit 0:

| Task | Status | Evidence |
| --- | --- | --- |
| the gate | ok | every assertion passed, the r1-r3-down scenario included (`r1>r2>r3 DELIVERED_TO_SUBNET`); r3's candidate digest `848dfc7c...` |
| `net.frr.config` with `src: configs/r3.good.frr.conf` and `expect_sha256: "{{ result.gate.sha256.r3 }}"` | changed | `sha256 848dfc7c4d8b31c0aa240f2bf659766d1abab63f4cce99fde7bfd050a5aac186`, a backup kept on r3 |
| `wait.routes` on r1 (10.3.3.0/24 via 10.0.13.2, stable for 10 s) | ok | waited 10.3 s |
| `wait.routes` on r3 (10.1.1.0/24 via 10.0.13.1) | ok | waited 5.2 s |
| `net.trace` from r1 to h2 | ok | hops `10.0.13.2, 10.3.3.10` |
| facts read again, and `routes_match` against the model of the candidate | ok | `r1's 8 routes match the model`, the same for r2 and r3 |

The running configuration r3 reported afterwards hashes to `848dfc7c...`, the same as
`sha256sum configs/r3.good.frr.conf`: r3 runs exactly the bytes the gate checked.

The change was then undone with Pleiades' own rollback, so step 5 starts from the baseline:

```sh
pleiades rollback 078bcea2-e9ab-4815-aa7e-d805ea874f82 --mode check --json > ../captures/12-rollback-good-check.json
pleiades rollback 078bcea2-e9ab-4815-aa7e-d805ea874f82 --json > ../captures/12-rollback-good.json
pleiades run runbooks/baseline.yaml --json > ../captures/13-baseline-after-rollback.json
```

The rollback restored r3 to `42d9d359...`, and the baseline run read the three baseline digests again.

### 5. Injected failure: the block undoes the change by itself

In one terminal the change, in a second the fault injection:

```sh
pleiades run runbooks/change_r3.yaml -e candidate=configs/r3.good.frr.conf --json > ../captures/14-injected-failure.json
../lab/inject-link-failure.sh   # waits for r3 to run the candidate, then:
                             # pleiades adhoc r1 net.frr.interface name=eth2 enabled:=false --json
```

(The captured run typed the script's two commands, the docker poll and the `pleiades adhoc` call, inline;
the script packages them unchanged.)

Timeline (`captures/14-injection-timeline.txt`):

```
22:32:02.564351631 r3 now runs the candidate; taking r1 eth2 down
22:32:02.789304551 adhoc rc=0 (r1 eth2 shut down)
change run rc=1
```

The change run (`7bc10005-e273-44ae-ada4-25c7cd960dd5`):

```
r3 runs exactly the candidate the gate checked                     changed  sha256 848dfc7c...
r1 routes r3's LAN over the r1-r3 link, and the table holds still   failed   wait.routes: after 45s the router still has no selected
                                                                             route to 10.3.3.0/24 through 10.0.13.2 (it uses 10.0.12.2)
undo tasks[3].block[0] (r3 runs exactly the candidate ...)         changed  sha256 42d9d359...  (the baseline digest)
```

Its journal (`captures/14-injected-failure.journal.txt`) records the change, the failed verification
and the block's own undo, and a rollback of the run is refused because nothing is left to undo:

```
rollback of run 7bc10005-e273-44ae-ada4-25c7cd960dd5 refused, before any device was contacted:
  every change this run made has already been undone, by the run itself or by an earlier rollback of it; there is nothing left to undo
```

Then the injected fault itself was undone, and the network compared with the baseline:

```sh
pleiades rollback 2f68de29-e756-4686-a421-176c8a1db97a --json > ../captures/15-restore-link.json   # the adhoc run's undo: eth2 up
pleiades adhoc r1 wait.routes 'prefixes:=[10.3.3.0/24]' 'next_hops:={"10.3.3.0/24": [10.0.13.2]}' \
  stable_for:=10 timeout:=60 --json > ../captures/15-wait-reconverged.json
pleiades run runbooks/baseline.yaml --json > ../captures/16-baseline-after-injected-failure.json
```

All three digests equal the baseline's, every assertion of the baseline model holds again
(`routes_match` for r1, r2 and r3 included), and every router's live routes (prefix and selected next
hops, the management network aside) equal the baseline's.

### 6. Tear down

```sh
pleiades run runbooks/batfish_down.yaml --json > ../captures/18-batfish-down.json
cd .. && lab/clab.sh destroy
```

No `frrlab` container or network remains (`captures/18-clab-destroy.log`,
`captures/18-free-after-teardown.txt`).

## Run ids

From `pleiades journal list` (`captures/17-journal-list.txt`):

| Step | Run id | Result |
| --- | --- | --- |
| switch | 2ad789e8-5e11-43fd-aa0d-cd83d9c510c8 | 3 changed |
| batfish_up | 6d7da164-14a3-4983-b723-1e387ed2aee5 | 1 changed |
| baseline | 4ade5148-9b98-4244-8176-d2c4fa486a59 | read only |
| bad candidate | a5d872f5-6c27-4b98-a112-51dfb4eec66f | refused at the gate, 0 changed |
| after the bad candidate | 49178e52-c3a3-43cb-8c5a-05a62f5a99a2 | read only, baseline digests |
| corrected candidate | 078bcea2-e9ab-4815-aa7e-d805ea874f82 | 1 changed, rolled back by d4e87f2c-2d3a-4ccd-b957-6eeb4c57d536 |
| injected failure | 7bc10005-e273-44ae-ada4-25c7cd960dd5 | changed, verification failed, undone within the run |
| link down (the fault) | 2f68de29-e756-4686-a421-176c8a1db97a | rolled back by 4e210831-550a-4c6e-a12c-99ff73110c48 |
| baseline after everything | 2678e8b6-771e-42ca-b6ab-5cc01629cbcd | read only, baseline digests |
| batfish_down | 7d97c292-ff51-4af4-a39f-3c32785a6afd | Batfish removed |

## The first attempt

`captures/attempt1/` holds the first pass through the same steps, against router images built
without the `/var/run/frr` permission. There, the first real `net.frr.config` failed partway:
frr-reload removed r3's eth3 description and then could not add the new one, the block's undo failed
the same way, and r3 was left running neither the baseline nor the candidate until a later
`pleiades rollback` restored it (after the permission was fixed). That is GAPS.md G1 and G2. The lab
was then destroyed, the project state removed, and every step above re-run from nothing.
