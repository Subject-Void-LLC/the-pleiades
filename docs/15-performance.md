---
status: beta
---

# Performance compared with Ansible

The same work, run by `ansible-playbook` and by the runbook `pleiades forge migrate-playbook`
converts it to, on 1 to 200 hosts at once, with time, CPU, memory, threads and processes, SSH
logins, network and disk all measured. Everything on this page comes from one run of
[`tools/ansiblebench`](../tools/ansiblebench/bench.py) on 2026-09-24; its raw output is
[`tools/ansiblebench/results/2026-09-24.json`](../tools/ansiblebench/results/2026-09-24.json).

**In short.** On this workload Pleiades finished 24 to 41 times sooner at every size. At 200 hosts
it used about 57 times less CPU on the machine running it, and about 72 times less across the
whole machine, devices included. It also used 9 to 14 times less memory on the control node. At
every size it sent about 62 times less network traffic. Neither tool's disk use was significant. Most of Ansible's cost is not SSH:
for every task on every host it builds a Python module, copies it over and starts Python on the
device. Pleiades sends the command and reads the answer.

## What was measured

- **The work.** Every host runs ten tasks, and each reads `/etc/os-release`. On the Ansible side
  that is a playbook of ten `ansible.builtin.command` tasks. On the Pleiades side it is the runbook
  `migrate-playbook` makes of that playbook: ten `exec.command` tasks. Each run is accepted only
  when the tool reports all ten tasks done on every host.
- **The tools.**
  - **Ansible:** ansible-core 2.19.11 with its defaults. Its `ControlPersist` keeps each host's
    SSH connection open for 60 seconds after a run, so every timed run reuses the logins its
    warm-up run made. That is Ansible's best case.
  - **Pleiades:** a development build of this repository at commit `0b5fe4e`, with the connection
    persistence work on top. It was measured twice:
    - with connections persisting between a host's tasks, its default, logging in once per host
      per run;
    - with `--persist-connections=false`, logging in once per task.
- **How many devices at once.**
  - **5:** the default of both tools.
  - **25:** set with `-f 25` and `--forks 25`.
- **The devices.** Up to 200 containers of Alpine Linux 3.20, each running OpenSSH 9.7 as root,
  with the usual privilege separation for every login, plus Python 3, which only Ansible needs
  ([`Dockerfile.target`](../tools/ansiblebench/Dockerfile.target)). Each is limited to 64 MiB of
  memory, and all of them share one host key.
- **The control node.** One container both tools run in, built from this repository's
  `Dockerfile.legacy-ansible-runner`. It has an init process as PID 1, is limited to 3 GiB of
  memory, and keeps both tools' working files on its own filesystem.
- **The machine.** A WSL2 virtual machine on an Intel Core Ultra 7 265KF, with 20 CPUs and 9.7 GiB
  of memory, running Docker Desktop 29.8.0 on Linux 6.18. Every container is on one Docker
  network, so there is no network latency to speak of.
- **Each figure** is the median of three timed runs (fifteen for one host), each after one warm-up
  run.

How each resource was read:

| Resource | Control node (the container the tool runs in) | Whole machine (control node, devices and Docker) |
|---|---|---|
| Time | wall clock around the tool's command | - |
| CPU | the container's `cpu.stat`, seconds used during the run | busy time in `/proc/stat` during the run |
| Memory | peak anonymous memory in the container's cgroup, above the level before the run | peak `AnonPages` in `/proc/meminfo`, above the level before the run |
| Threads and processes | peak `pids.current` in the container's cgroup | peak kernel task count (`/proc/loadavg`), and the processes created (`/proc/stat`) |
| SSH logins | TCP connections the container opened (`ActiveOpens` in `/proc/net/snmp`) | - |
| Network | bytes in and out of the container's interface | - |
| Disk | bytes read and written, from the container's `io.stat`, after a `sync` | bytes read and written on the machine's disks, from `/proc/diskstats`, after a `sync` |

The whole-machine figures include the devices, and the devices share the machine's CPUs with the
tools. They are the total cost of a run, not the control node's alone.

## Results

Wall-clock time per run:

| Hosts | Forks | Ansible | Pleiades | Pleiades, persistence off | Pleiades faster by |
|---|---|---|---|---|---|
| 1 | 5 | 2.45 s | 0.10 s | 0.63 s | 24x |
| 10 | 5 | 5.15 s | 0.19 s | 1.27 s | 28x |
| 10 | 25 | 3.35 s | 0.12 s | 0.66 s | 28x |
| 50 | 5 | 23.11 s | 0.83 s | 6.03 s | 28x |
| 50 | 25 | 11.84 s | 0.29 s | 1.52 s | 41x |
| 100 | 5 | 45.91 s | 1.64 s | 11.90 s | 28x |
| 100 | 25 | 22.72 s | 0.57 s | 2.86 s | 40x |
| 200 | 5 | 92.42 s | 3.22 s | 23.65 s | 29x |
| 200 | 25 | 42.99 s | 1.05 s | 5.45 s | 41x |

CPU seconds per run (control node / whole machine):

| Hosts | Forks | Ansible | Pleiades | Pleiades, persistence off |
|---|---|---|---|---|
| 1 | 5 | 1.27 / 4.24 | 0.04 / 0.12 | 0.06 / 0.38 |
| 10 | 5 | 9.63 / 29.04 | 0.19 / 0.45 | 0.31 / 2.15 |
| 10 | 25 | 10.50 / 30.75 | 0.22 / 0.51 | 0.34 / 1.21 |
| 50 | 5 | 48.46 / 148.07 | 0.85 / 1.91 | 1.44 / 9.07 |
| 50 | 25 | 62.09 / 199.85 | 1.01 / 2.50 | 1.57 / 8.42 |
| 100 | 5 | 98.04 / 295.83 | 1.65 / 3.93 | 2.83 / 16.38 |
| 100 | 25 | 127.83 / 404.76 | 1.94 / 4.52 | 3.02 / 15.19 |
| 200 | 5 | 199.57 / 600.08 | 3.48 / 8.31 | 5.66 / 32.20 |
| 200 | 25 | 258.18 / 803.00 | 3.66 / 8.93 | 5.70 / 29.42 |

Peak memory added, MiB (control node / whole machine):

| Hosts | Forks | Ansible | Pleiades | Pleiades, persistence off |
|---|---|---|---|---|
| 1 | 5 | 63 / 105 | 10 / 24 | 11 / 15 |
| 10 | 5 | 136 / 213 | 16 / 29 | 15 / 23 |
| 10 | 25 | 202 / 332 | 17 / 37 | 19 / 27 |
| 50 | 5 | 183 / 325 | 20 / 103 | 17 / 37 |
| 50 | 25 | 445 / 732 | 32 / 89 | 32 / 73 |
| 100 | 5 | 278 / 521 | 27 / 192 | 19 / 26 |
| 100 | 25 | 459 / 737 | 40 / 189 | 32 / 66 |
| 200 | 5 | 479 / 882 | 35 / 335 | 19 / 33 |
| 200 | 25 | 555 / 654 | 62 / 357 | 34 / 63 |

Tasks (threads and processes): peak added on the control node / on the whole machine, and processes created per run:

| Hosts | Forks | Ansible | Pleiades | Pleiades, persistence off |
|---|---|---|---|---|
| 1 | 5 | 8 / 12, 289 created | 13 / 18, 26 created | 13 / 28, 59 created |
| 10 | 5 | 32 / 70, 2592 created | 17 / 42, 147 created | 17 / 32, 429 created |
| 10 | 25 | 43 / 60, 2567 created | 22 / 55, 154 created | 22 / 46, 428 created |
| 50 | 5 | 61 / 172, 12838 created | 22 / 127, 677 created | 20 / 43, 2113 created |
| 50 | 25 | 83 / 164, 12678 created | 25 / 133, 679 created | 25 / 86, 2046 created |
| 100 | 5 | 123 / 349, 25658 created | 22 / 230, 1343 created | 19 / 43, 4192 created |
| 100 | 25 | 86 / 176, 25343 created | 27 / 235, 1344 created | 25 / 86, 4072 created |
| 200 | 5 | 223 / 645, 51330 created | 23 / 429, 2670 created | 21 / 44, 8367 created |
| 200 | 25 | 84 / 174, 50619 created | 29 / 444, 2633 created | 25 / 87, 8090 created |

SSH logins (TCP connections opened) and network traffic per run:

| Hosts | Forks | Ansible | Pleiades | Pleiades, persistence off |
|---|---|---|---|---|
| 1 | 5 | 0 logins, 1.3 MiB | 1 login, 0.0 MiB | 10 logins, 0.1 MiB |
| 10 | 5 | 0 logins, 12.7 MiB | 10 logins, 0.2 MiB | 100 logins, 0.7 MiB |
| 10 | 25 | 0 logins, 12.7 MiB | 10 logins, 0.2 MiB | 100 logins, 0.7 MiB |
| 50 | 5 | 0 logins, 63.7 MiB | 50 logins, 1.0 MiB | 500 logins, 3.3 MiB |
| 50 | 25 | 0 logins, 63.8 MiB | 50 logins, 1.0 MiB | 500 logins, 3.3 MiB |
| 100 | 5 | 0 logins, 127.5 MiB | 100 logins, 2.0 MiB | 1000 logins, 6.5 MiB |
| 100 | 25 | 0 logins, 127.6 MiB | 100 logins, 2.0 MiB | 1000 logins, 6.6 MiB |
| 200 | 5 | 0 logins, 255.0 MiB | 200 logins, 4.1 MiB | 2000 logins, 13.1 MiB |
| 200 | 25 | 0 logins, 255.3 MiB | 200 logins, 4.0 MiB | 2000 logins, 13.1 MiB |

Disk per run, MiB (control node read / write; whole machine read / write):

| Hosts | Forks | Ansible | Pleiades | Pleiades, persistence off |
|---|---|---|---|---|
| 1 | 5 | 0.0 / 0.0; 0.0 / 0.4 | 0.0 / 0.0; 0.0 / 0.3 | 0.0 / 0.0; 0.0 / 0.4 |
| 10 | 5 | 0.0 / 0.0; 0.0 / 1.1 | 0.0 / 0.1; 0.0 / 0.6 | 0.0 / 0.1; 0.0 / 1.1 |
| 10 | 25 | 0.0 / 0.0; 0.0 / 1.0 | 0.0 / 0.1; 0.0 / 0.6 | 0.0 / 0.1; 0.0 / 1.1 |
| 50 | 5 | 0.0 / 0.0; 0.3 / 7.9 | 0.0 / 0.4; 0.0 / 1.4 | 0.0 / 0.4; 0.2 / 3.7 |
| 50 | 25 | 0.0 / 0.0; 3.8 / 8.2 | 0.0 / 0.4; 0.0 / 1.3 | 0.0 / 0.4; 0.0 / 3.4 |
| 100 | 5 | 0.0 / 0.2; 2.4 / 20.1 | 0.0 / 0.8; 0.0 / 2.5 | 0.0 / 0.8; 0.1 / 6.8 |
| 100 | 25 | 2.1 / 3.8; 12.2 / 19.6 | 0.0 / 0.8; 0.0 / 2.4 | 0.0 / 0.8; 0.0 / 6.4 |
| 200 | 5 | 0.3 / 1.4; 13.5 / 69.1 | 0.0 / 1.6; 0.0 / 4.6 | 0.0 / 1.6; 6.1 / 14.0 |
| 200 | 25 | 0.0 / 0.2; 25.1 / 38.5 | 0.0 / 1.6; 0.0 / 4.3 | 0.0 / 1.6; 0.0 / 11.8 |

## What the numbers say

- **Time grows linearly with hosts for both tools; only the slope differs.** At 5 devices at a
  time the cost per host is:
  - Ansible: 0.46 s;
  - Pleiades: 0.016 s, or 0.12 s with persistence off.

  Going from 5 devices at a time to 25 made 200 hosts 2.15 times faster for Ansible, 3.1 times
  for Pleiades and 4.3 times for Pleiades with persistence off.
- **Ansible is limited by CPU, and at 25 wide it ran out.** It used about 0.1 CPU seconds on the
  control node for each task on each host, and 0.3 CPU seconds across the whole machine.
  - At 200 hosts and 25 devices at a time, it kept about 18.7 of the machine's 20 CPUs busy for
    the whole run. That is why five times the parallelism bought it only about twice the speed.
  - Pleiades used 1.7 ms of control-node CPU per task, and 4.2 ms across the machine. At the same
    size it had about 8.5 CPUs busy for one second.
  - Put per core: one core of control-node CPU handles about 10 of these tasks a second under
    Ansible, and about 575 under Pleiades.
- **Memory.** On the control node Ansible's memory grows with hosts:
  - Ansible at 200 hosts: 479 to 555 MiB. It keeps an `ssh` process and an `sshpass` process for
    every host it has logged in to.
  - Pleiades at 200 hosts: 35 to 62 MiB.
- **Kept connections cost something on the devices.** With persistence on, Pleiades holds one
  SSH session open on every device until its run ends. Across the machine that added about
  1.5 MiB and two processes per device: 335 MiB and 429 tasks at 200 hosts, against 33 MiB and
  44 tasks with persistence off. Ansible's kept connections cost the same kind of thing, and they
  outlive its run by 60 seconds.
- **Threads and processes.**
  - Ansible created about 26 processes per task on each host, 51,330 for one 200-host run.
  - Pleiades created about 1.3 per task with persistence and 4.2 without.
  - Pleiades's control-node thread count stayed between 13 and 29 at every size.
  - Ansible's whole-machine task peak at 25 devices at a time (174 at 200 hosts) is lower than at
    5 (645). The sampler runs in the same container, and at 25 wide Ansible kept the machine's
    CPUs about 94% busy, so those two peaks may be understated. The counts of processes created
    come from a kernel counter and are not affected.
- **SSH logins and network.**
  - Ansible's timed runs opened no connections at all, because each reused the ones its warm-up
    kept open. Instead it sent about 130 KiB per task: the module, and Python's output coming
    back.
  - Pleiades logged in once per host per run and sent about 2 KiB per task.
  - With persistence off, Pleiades logs in once per task. Each extra login cost about 57 ms here:
    200 hosts took 20 s longer at 5 devices at a time, over 1,800 extra logins. The logins also
    cost CPU on the devices, 32 CPU seconds against 8 for 200 hosts.
- **Disk.**
  - Pleiades writes its run journal on the control node, about 8 KiB per host per run.
  - Ansible's per-task module files on the devices are usually deleted before they are ever
    written out. They showed up as up to 69 MiB written across the machine for 200 hosts.
  - Neither is a constraint at this scale.
- **Install footprint.**
  - Pleiades: one static binary of 90 MiB on the control node, and nothing on a device beyond a
    shell.
  - Ansible: ansible-core is 15 MiB, inside a Python installation of 68 MiB. Every device needs
    Python too, 30 MiB here.

## The limits of this measurement

- **One machine.** The devices share the tools' CPUs and memory, so a real fleet would not slow
  the control node the way 200 local containers do. The whole-machine CPU figures include the
  devices' own work.
- **A trivial task.** Each task reads one small file. A task that does real work on the device
  (installing a package, restarting a service) adds the same device time to both tools, so the
  ratios shrink as the device work grows. The per-task overhead each tool adds does not.
- **No network latency.** Over a real network, every round trip costs more. That penalizes
  logging in per task, and it penalizes Ansible's several round trips per task.
- **Ansible's defaults only.** Pipelining, which skips the module copy, was 7 to 12% faster for
  Ansible in an earlier single-host measurement (on the earlier target, below). Other strategies (`free`, Mitogen) were not
  tried.
- **The command line only.** The Controller and Runner run the same engine and the same
  connection pool. Their parallelism comes from the number of Runners (each works on 5 devices
  at a time), and their `forks` launch field is not read yet. They were not measured here.
- **Only the conditions above.** A figure from this page predicts a different machine only
  roughly. Rerun the benchmark there.

## Found while measuring

- **A benchmark device must log in the way a real one does.** The first comparison used a
  container that runs `sshd` as an unprivileged user, which skips the privilege separation a
  root `sshd` performs for every login. Its logins cost about 22 ms. The same login costs about
  60 ms on a root `sshd` like the target above. The first comparison therefore understated
  login cost, most of all for Pleiades with persistence off, and this page supersedes it.
- **Ansible needs an init process in a container.** Ansible leaves orphaned processes for PID 1
  to reap: its finished worker processes, and its kept `ssh` connections. An ordinary host's
  init does that. The benchmark's control container first ran `sleep` as PID 1, which never
  reaps. About one zombie process per task execution piled up, until after the 100-host runs the
  kernel ran out of task slots and every program on the machine failed to start new processes.
  The control container now runs with an init.

## Reproducing it

```bash
python3 tools/ansiblebench/bench.py --sizes 1,10,50,100,200 --forks 5,25
```

It needs Docker, Go and Python 3, and takes about 40 minutes for these sizes on this machine.
The 200 devices take about 1.2 GiB of memory, and the control container is capped at 3 GiB
(`--runner-memory`). It prints these tables, and it writes `results.json` into its work directory
after every row. A run that fails is recorded with its first error line and any processes
killed for memory, and the benchmark continues. It removes every container it started, including
after an interrupt. Nothing in `make ci` runs it.
