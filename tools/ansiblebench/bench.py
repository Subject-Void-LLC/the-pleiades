#!/usr/bin/env python3
"""Time the same work through ansible-playbook and pleiades run, from one
host to many.

Every host runs ten tasks that each read /etc/os-release: an Ansible
playbook of ten ansible.builtin.command tasks, and the runbook
`pleiades forge migrate-playbook` makes of it (ten exec.command tasks).
Both tools run in one container built from this repository's
Dockerfile.legacy-ansible-runner, against N targets built from
Dockerfile.target beside this file, on one Docker network. Pleiades is
timed with connections persisting (its default) and with
--persist-connections=false; Ansible with its own defaults, whose
ControlPersist keeps a connection open between runs, so every timed
Ansible run after the first reuses an earlier login. That is Ansible's
best case.

A run counts only when it reports all ten tasks done on every host. One
that does not is recorded as failed, with how many processes the runner's
memory limit (--runner-memory) killed and the tool's first error line, its
full output kept under failures/ in the work directory, and the benchmark
moves on. The control side's peak anonymous memory is sampled from the
runner container's cgroup during each run, and results.json is rewritten
after every row.

Docker, Go and Python 3 are needed on the machine running it. It is NOT
run by `make ci` or any test; it is run by hand, and its results are
copied into docs/03-migrating-from-ansible.md with the date and machine.

    python3 tools/ansiblebench/bench.py --sizes 1,10,50,100,200 --forks 5,25

Every container and the network it starts are removed on exit, including
on an interrupt.
"""

import argparse
import json
import os
import shutil
import signal
import statistics
import subprocess
import sys
import tempfile
import threading
import time

NETWORK = "ansiblebench"
TARGET_IMAGE = "ansiblebench-target:local"
RUNNER_IMAGE = "ansiblebench-runner:local"
RUNNER = "ab-runner"
LABEL = "ansiblebench=1"
# BENCH is where the tools work inside the runner: its own filesystem.
BENCH = "/bench"
USER, PASSWORD = "testuser", "testpass123"
TASKS = 10


def sh(*cmd, **kw):
    """Run cmd, failing loudly with its output."""
    p = subprocess.run(cmd, capture_output=True, text=True, **kw)
    if p.returncode != 0:
        raise SystemExit(f"{' '.join(cmd)} failed ({p.returncode}):\n{p.stdout[-3000:]}\n{p.stderr[-3000:]}")
    return p.stdout


def target(i):
    return f"ab-{i}"


def host_main(args):
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    sizes = sorted(int(s) for s in args.sizes.split(","))
    forks = [int(f) for f in args.forks.split(",")]
    work = args.work or tempfile.mkdtemp(prefix="ansiblebench-")
    os.makedirs(work, exist_ok=True)
    started = []

    def cleanup(*_):
        if started:
            # The runner writes as root; hand the work directory back.
            subprocess.run(["docker", "exec", RUNNER, "chown", "-R", f"{os.getuid()}:{os.getgid()}", "/work"], capture_output=True)
        if started and not args.keep:
            remove_labelled()
        started.clear()

    def on_signal(signum, _):
        cleanup()
        sys.exit(128 + signum)

    signal.signal(signal.SIGTERM, on_signal)
    signal.signal(signal.SIGINT, on_signal)
    try:
        print(f"work directory: {work}", file=sys.stderr)
        env = dict(os.environ, CGO_ENABLED="0")
        sh("go", "build", "-o", os.path.join(work, "pleiades"), "./cmd/pleiades", cwd=root, env=env)
        sh("docker", "build", "-q", "-t", TARGET_IMAGE, "-f", os.path.join(root, "tools/ansiblebench/Dockerfile.target"), os.path.join(root, "tools/ansiblebench"))
        sh("docker", "build", "-q", "-t", RUNNER_IMAGE, "-f", os.path.join(root, "Dockerfile.legacy-ansible-runner"), root)
        shutil.copy(os.path.abspath(__file__), os.path.join(work, "bench.py"))
        remove_labelled()
        sh("docker", "network", "create", NETWORK)
        started.append(RUNNER)
        for i in range(1, sizes[-1] + 1):
            sh("docker", "run", "-d", "--label", LABEL, "--name", target(i), "--network", NETWORK, "--memory", "64m", TARGET_IMAGE)
        # --init: Ansible leaves orphans (its dead workers, its persisted
        # ssh processes) for PID 1 to reap, and `sleep` never reaps, so
        # without an init they pile up as zombies until the kernel runs out
        # of tasks (FAILURE_PATTERNS 343).
        sh("docker", "run", "-d", "--init", "--label", LABEL, "--name", RUNNER, "--network", NETWORK, "--memory", args.runner_memory,
           "-v", f"{work}:/work", "-e", "HOME=/work/home", RUNNER_IMAGE, "sleep", "infinity")
        inner = ["docker", "exec", "-w", "/work", RUNNER, "python3", "/work/bench.py", "--inner",
                 "--sizes", args.sizes, "--forks", args.forks, "--rounds", str(args.rounds),
                 "--single-rounds", str(args.single_rounds)]
        p = subprocess.run(inner)
        # The runner wrote as root; hand the work directory back before
        # reading and rewriting its results.
        subprocess.run(["docker", "exec", RUNNER, "chown", "-R", f"{os.getuid()}:{os.getgid()}", "/work"], capture_output=True)
        # The runner writes results.json after every row, so a run that
        # stopped part way still reports what it measured.
        path = os.path.join(work, "results.json")
        if os.path.exists(path):
            with open(path) as f:
                results = json.load(f)
            # What Ansible needs on each device and Pleiades does not.
            du = subprocess.run(["docker", "exec", target(1), "du", "-sk", "/usr/lib/python3.12", "/usr/bin/python3.12"],
                                capture_output=True, text=True).stdout
            results["meta"].setdefault("footprint", {})["device_python_mib"] = sum(int(l.split()[0]) for l in du.splitlines()) / 1024
            with open(path, "w") as f:
                json.dump(results, f, indent=2)
            print(report(results))
        if p.returncode != 0:
            raise SystemExit(p.returncode)
    finally:
        cleanup()


def remove_labelled():
    """Remove every container this tool labelled, and its network, checking
    until none is left: one bulk `docker rm` has been seen to stop only
    some of two hundred."""
    for _ in range(5):
        ids = subprocess.run(["docker", "ps", "-aq", "--filter", "label=" + LABEL], capture_output=True, text=True).stdout.split()
        if not ids:
            break
        for i in range(0, len(ids), 50):
            subprocess.run(["docker", "rm", "-f", *ids[i:i + 50]], capture_output=True)
    subprocess.run(["docker", "network", "rm", NETWORK], capture_output=True)


def report(results):
    """Render the results as the Markdown tables docs/15 carries, one per
    resource, each cell naming its side: the control node (the container
    the tool runs in) or the whole machine (control node, devices and
    Docker together)."""
    meta, runs = results["meta"], results["runs"]
    tools = [("ansible", "Ansible"), ("pleiades", "Pleiades"), ("pleiades_off", "Pleiades, persistence off")]
    mib = 1048576
    out = [f"Measured {meta['date']}: ansible-core {meta['ansible']}, pleiades {meta['pleiades']}, "
           f"{meta['cpus']} CPUs, the runner limited to {meta['runner_memory']}. Median of {meta['rounds']} runs "
           f"({meta['single_rounds']} for one host), each after one warm-up run; {TASKS} tasks per host."]

    def table(title, fmt, extra=None):
        out.extend(["", title, "", "| Hosts | Forks | " + " | ".join(t for _, t in tools) + (f" | {extra[0]} |" if extra else " |"),
                    "|---|---|---|---|---" + ("|---|" if extra else "|")])
        for r in runs:
            row = f"| {r['hosts']} | {r['forks']} | " + " | ".join(fmt(r[k]) for k, _ in tools)
            out.append(row + (f" | {extra[1](r)} |" if extra else " |"))

    def speed(r):
        a, p = r["ansible"], r["pleiades"]
        return f"{a['median'] / p['median']:.0f}x" if "median" in a and "median" in p else "-"

    def get(m, k, scale=1.0):
        return m[k] / scale if k in m else float("nan")

    table("Wall-clock time per run:", cell, ("Pleiades faster by", speed))
    table("CPU seconds per run (control node / whole machine):",
          lambda m: f"{get(m, 'control_cpu_s'):.2f} / {get(m, 'machine_cpu_s'):.2f}")
    table("Peak memory added, MiB (control node / whole machine):",
          lambda m: f"{m['control_mib']:.0f} / {m['machine_mib']:.0f}")
    table("Tasks (threads and processes): peak added on the control node / on the whole machine, and processes created per run:",
          lambda m: f"{m['control_tasks']} / {m['machine_tasks']}, {get(m, 'forks'):.0f} created")
    table("SSH logins (TCP connections opened) and network traffic per run:",
          lambda m: f"{get(m, 'tcp_opens'):.0f} login{'' if get(m, 'tcp_opens') == 1 else 's'}, {get(m, 'net_bytes', mib):.1f} MiB")
    table("Disk per run, MiB (control node read / write; whole machine read / write):",
          lambda m: f"{get(m, 'control_read', mib):.1f} / {get(m, 'control_write', mib):.1f}; "
                    f"{get(m, 'machine_read', mib):.1f} / {get(m, 'machine_write', mib):.1f}")
    fp = meta.get("footprint", {})
    if fp:
        out.extend(["", "Install footprint:", "",
                    f"- Pleiades on the control node: one static binary, {fp['pleiades_binary_mib']:.0f} MiB. On a device: nothing but a shell.",
                    f"- Ansible on the control node: ansible-core {fp['ansible_core_mib']:.0f} MiB, inside a Python installation of "
                    f"{fp['python_mib']:.0f} MiB in all. On a device: Python, {fp.get('device_python_mib', float('nan')):.0f} MiB here."])
    return "\n".join(out)


def cell(m):
    """One measurement's table cell: its median, or why it has none."""
    if "failed" in m:
        return f"failed: {m['failed']}"
    return f"{m['median']:.2f} s"


# ---- inside the runner container ----

def cgroup_tasks():
    """Tasks (threads and processes) in the runner container's cgroup."""
    with open("/sys/fs/cgroup/pids.current") as f:
        return int(f.read())


def kernel_tasks():
    """Tasks on the whole machine: /proc/loadavg is not namespaced."""
    with open("/proc/loadavg") as f:
        return int(f.read().split()[3].split("/")[1])


def kernel_anon():
    """Anonymous memory on the whole machine, in bytes: /proc/meminfo is
    not namespaced either."""
    with open("/proc/meminfo") as f:
        for line in f:
            if line.startswith("AnonPages:"):
                return int(line.split()[1]) * 1024
    return 0


SAMPLERS = {"control_mib": lambda: anon_bytes() / 1048576, "control_tasks": cgroup_tasks,
            "machine_tasks": kernel_tasks, "machine_mib": lambda: kernel_anon() / 1048576}


def snapshot():
    return {k: f() for k, f in SAMPLERS.items()}


def read_kv(path, prefix):
    """The integer after prefix on the first line of path starting with it."""
    try:
        with open(path) as f:
            for line in f:
                if line.startswith(prefix):
                    return int(line[len(prefix):].split()[0])
    except OSError:
        pass
    return 0


def machine_cpu_s():
    """Busy CPU seconds on the whole machine (/proc/stat is global):
    everything but idle and iowait."""
    with open("/proc/stat") as f:
        v = [int(x) for x in f.readline().split()[1:]]
    busy = v[0] + v[1] + v[2] + v[5] + v[6] + v[7]
    return busy / os.sysconf("SC_CLK_TCK")


def tcp_active_opens():
    """Outgoing TCP connections this container has opened: one per SSH
    login."""
    with open("/proc/net/snmp") as f:
        rows = [l.split() for l in f if l.startswith("Tcp:")]
    return int(rows[1][rows[0].index("ActiveOpens")])


def cgroup_io():
    """Block bytes read and written by this container, from io.stat."""
    r = w = 0
    try:
        with open("/sys/fs/cgroup/io.stat") as f:
            for line in f:
                for field in line.split()[1:]:
                    k, _, v = field.partition("=")
                    r += int(v) if k == "rbytes" else 0
                    w += int(v) if k == "wbytes" else 0
    except OSError:
        pass
    return r, w


def machine_io():
    """Bytes read and written on the machine's whole disks (diskstats is
    global); partitions are skipped so nothing counts twice."""
    r = w = 0
    with open("/proc/diskstats") as f:
        for line in f:
            x = line.split()
            name = x[2]
            if (name.startswith(("sd", "vd")) and name[-1].isalpha()) or (name.startswith("nvme") and "p" not in name[4:]):
                r += int(x[5]) * 512
                w += int(x[9]) * 512
    return r, w


def net_bytes():
    total = 0
    for d in ("tx", "rx"):
        with open(f"/sys/class/net/eth0/statistics/{d}_bytes") as f:
            total += int(f.read())
    return total


def counters():
    """Cumulative counters read before and after a run; the report shows
    what one run added. Disk is read after a sync, so writes a run left in
    the page cache are counted rather than deferred to the next run."""
    os.sync()
    cr, cw = cgroup_io()
    mr, mw = machine_io()
    return {"control_cpu_s": read_kv("/sys/fs/cgroup/cpu.stat", "usage_usec ") / 1e6, "machine_cpu_s": machine_cpu_s(),
            "net_bytes": net_bytes(), "tcp_opens": tcp_active_opens(), "forks": read_kv("/proc/stat", "processes "),
            "control_read": cr, "control_write": cw, "machine_read": mr, "machine_write": mw}


def anon_bytes():
    with open("/sys/fs/cgroup/memory.stat") as f:
        for line in f:
            if line.startswith("anon "):
                return int(line.split()[1])
    return 0


def oom_kills():
    with open("/sys/fs/cgroup/memory.events") as f:
        for line in f:
            if line.startswith("oom_kill "):
                return int(line.split()[1])
    return 0


class RunFailed(Exception):
    """A timed run that did not finish every task on every host."""

    def __init__(self, reason, peak):
        super().__init__(reason)
        self.reason, self.peak = reason, peak


def timed(cmd, cwd, env, check, log):
    """Run cmd once, returning seconds and the peak of every SAMPLERS
    metric while it ran, and what it added to every counters() value.
    check validates the output; a run that fails it raises RunFailed, its
    output saved to log."""
    kills = oom_kills()
    before = counters()
    peak = snapshot()
    done = threading.Event()

    def sample():
        while not done.is_set():
            for k, v in snapshot().items():
                peak[k] = max(peak[k], v)
            time.sleep(0.02)

    t = threading.Thread(target=sample)
    t.start()
    t0 = time.perf_counter()
    p = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=1800)
    dt = time.perf_counter() - t0
    done.set()
    t.join()
    if p.returncode != 0 or not check(p.stdout):
        with open(log, "w") as f:
            f.write(f"{' '.join(cmd)}\nexit {p.returncode}\n--- stdout\n{p.stdout}\n--- stderr\n{p.stderr}")
        killed = oom_kills() - kills
        first = next((l.strip() for l in (p.stderr + p.stdout).splitlines()
                      if l.startswith(("ERROR!", "fatal:", "execution")) or "dead state" in l), f"exit {p.returncode}")
        reason = f"{killed} processes killed out of memory; {first[:160]}" if killed else first[:200]
        raise RunFailed(reason, peak)
    after = counters()
    return dt, peak, {k: after[k] - before[k] for k in after}


def measure(name, cmd, cwd, env, check, rounds):
    """Time cmd rounds times after one warm-up, from a baseline taken once
    Ansible's persisted connections are gone, so each metric is what this
    tool added: its warm-up's persisted connections count, since they are
    part of its steady state. A failed run ends the measurement and is
    recorded with what it had reached."""
    log = f"/work/failures/{name}.log"
    os.makedirs("/work/failures", exist_ok=True)
    drop_ansible_connections()
    base = snapshot()
    times, peaks, deltas = [], [], []

    def added(ps):
        return {k: max(p[k] for p in ps) - base[k] for k in base}

    def per_run():
        return {k: statistics.median(d[k] for d in deltas) for k in deltas[0]} if deltas else {}

    try:
        timed(cmd, cwd, env, check, log)  # warm-up
        for _ in range(rounds):
            dt, peak, delta = timed(cmd, cwd, env, check, log)
            times.append(dt)
            peaks.append(peak)
            deltas.append(delta)
    except RunFailed as e:
        return {"failed": e.reason, "log": log, **added(peaks + [e.peak]), **per_run()}
    return {"median": statistics.median(times), "min": min(times), "max": max(times), **added(peaks), **per_run()}


def drop_ansible_connections():
    """End Ansible's persisted ssh masters, so they are not counted against
    the next tool's memory."""
    subprocess.run(["pkill", "-x", "ssh"], capture_output=True)
    time.sleep(2)


def site_packages(*prefixes):
    """Every top-level entry in site-packages whose name starts with one of
    prefixes."""
    root = "/usr/local/lib/python3.12/site-packages"
    return [os.path.join(root, e) for e in os.listdir(root) if e.startswith(prefixes)]


def tree_mib(*paths):
    """The size of every file under paths, in MiB."""
    total = 0
    for p in paths:
        if os.path.isfile(p):
            total += os.path.getsize(p)
        for d, _, files in os.walk(p):
            total += sum(os.path.getsize(os.path.join(d, f)) for f in files if not os.path.islink(os.path.join(d, f)))
    return total / 1048576


def inner_main(args):
    sizes = sorted(int(s) for s in args.sizes.split(","))
    forks = [int(f) for f in args.forks.split(",")]
    biggest = sizes[-1]
    # Results go to the mounted /work; the tools work on the container's
    # own filesystem, so their disk I/O is block I/O this cgroup and the
    # machine's disks can count (a bind mount from another WSL distro is
    # not), and so a slow mount cannot slow either tool.
    shutil.rmtree("/work/failures", ignore_errors=True)
    if os.path.exists("/work/results.json"):
        os.remove("/work/results.json")
    os.environ["HOME"] = f"{BENCH}/home"
    os.makedirs(f"{BENCH}/home/.ssh", exist_ok=True)
    shutil.copy("/work/pleiades", f"{BENCH}/pleiades")

    # Every target shares one host key, so one wildcard line per key type
    # verifies them all.
    keys = ""
    for _ in range(30):
        keys = subprocess.run(["ssh-keyscan", target(1)], capture_output=True, text=True).stdout
        if keys.strip():
            break
        time.sleep(1)
    with open(f"{BENCH}/home/.ssh/known_hosts", "w") as f:
        for line in keys.splitlines():
            if line and not line.startswith("#"):
                f.write("ab-* " + line.split(" ", 1)[1] + "\n")

    with open(f"{BENCH}/read10.yml", "w") as f:
        f.write("- hosts: bench\n  gather_facts: false\n  tasks:\n")
        for i in range(TASKS):
            f.write(f"    - name: read a file {i}\n      ansible.builtin.command: cat /etc/os-release\n")
    for n in sizes:
        with open(f"{BENCH}/inv-{n}.ini", "w") as f:
            f.write("[bench]\n" + "".join(f"{target(i)}\n" for i in range(1, n + 1)))
            f.write(f"[bench:vars]\nansible_user={USER}\nansible_password={PASSWORD}\n"
                    "ansible_python_interpreter=/usr/bin/python3\n")

    project = f"{BENCH}/project"
    shutil.rmtree(project, ignore_errors=True)
    os.makedirs(project)
    pl = f"{BENCH}/pleiades"
    sh(pl, "init", cwd=project)
    for i in range(1, biggest + 1):
        tags = ",".join(f"n{n}" for n in sizes if i <= n)
        sh(pl, "add-host", target(i), "--type", "linux_server", "--set", f"host={target(i)}", "--set", "port=22",
           "--tags", tags, cwd=project)
        sh(pl, "add-credential", target(i), "--username", USER, "--password", PASSWORD, cwd=project)
    for n in sizes:
        with open(f"{project}/runbooks/read10-n{n}.yaml", "w") as f:
            f.write(f"id: read10-n{n}\nhosts: n{n}\ntasks:\n")
            for i in range(TASKS):
                f.write(f"  - name: read a file {i}\n    fqcn: exec.command\n    params:\n      cmd: cat /etc/os-release\n")

    env = dict(os.environ, ANSIBLE_HOST_KEY_CHECKING="False")
    time.sleep(1)
    ansible_version = sh("ansible", "--version").splitlines()[0].split("[core ")[-1].rstrip("]")
    meta = {"date": time.strftime("%Y-%m-%d"), "ansible": ansible_version,
            "pleiades": sh(pl, "version").strip().split()[-1], "cpus": os.cpu_count(),
            "runner_memory": args.runner_memory, "rounds": args.rounds, "single_rounds": args.single_rounds,
            "footprint": {"pleiades_binary_mib": os.path.getsize(pl) / 1048576,
                          "ansible_core_mib": tree_mib(*site_packages("ansible", "ansible_core")),
                          "python_mib": tree_mib("/usr/local/lib/python3.12", "/usr/local/bin/python3.12")}}
    runs = []
    for n in sizes:
        for fk in (forks if n > 1 else forks[:1]):
            rounds = args.single_rounds if n == 1 else args.rounds

            def pleiades_ok(out, n=n):
                return "run complete" in out and out.count(": changed") == TASKS * n

            def ansible_ok(out, n=n):
                return sum(1 for l in out.splitlines() if f": ok={TASKS} " in l and "failed=0" in l and "unreachable=0" in l) == n

            base = [pl, "run", f"runbooks/read10-n{n}.yaml", "--forks", str(fk)]
            tag = f"n{n}-f{fk}"
            row = {"hosts": n, "forks": fk,
                   "pleiades": measure(f"pleiades-{tag}", base, project, env, pleiades_ok, rounds),
                   "pleiades_off": measure(f"pleiades-off-{tag}", base + ["--persist-connections=false"], project, env, pleiades_ok, rounds),
                   "ansible": measure(f"ansible-{tag}", ["ansible-playbook", "-i", f"{BENCH}/inv-{n}.ini", "-f", str(fk), f"{BENCH}/read10.yml"],
                                      BENCH, env, ansible_ok, rounds)}
            runs.append(row)
            with open("/work/results.json", "w") as f:
                json.dump({"meta": meta, "runs": runs}, f, indent=2)
            print(f"hosts {n} forks {fk}: " + ", ".join(
                f"{k} {cell(row[k])} (cpu {row[k].get('control_cpu_s', 0):.1f}/{row[k].get('machine_cpu_s', 0):.1f} s, "
                f"{row[k]['control_mib']:.0f} MiB, {row[k]['control_tasks']} tasks; machine +{row[k]['machine_tasks']} tasks, "
                f"+{row[k]['machine_mib']:.0f} MiB; {row[k].get('tcp_opens', 0):.0f} logins)" for k in ("ansible", "pleiades", "pleiades_off")),
                file=sys.stderr, flush=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--sizes", default="1,10,50,100,200", help="host counts, comma-separated")
    ap.add_argument("--forks", default="5,25", help="devices worked on at once, comma-separated; one host uses the first")
    ap.add_argument("--rounds", type=int, default=3, help="timed runs per measurement")
    ap.add_argument("--single-rounds", type=int, default=15, help="timed runs for one host")
    ap.add_argument("--runner-memory", default="3g", help="memory limit of the container both tools run in")
    ap.add_argument("--work", help="work directory (default: a new temporary one)")
    ap.add_argument("--keep", action="store_true", help="leave the containers running")
    ap.add_argument("--inner", action="store_true", help=argparse.SUPPRESS)
    args = ap.parse_args()
    (inner_main if args.inner else host_main)(args)


if __name__ == "__main__":
    main()
