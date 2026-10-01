"""Times pywinrm against a real WinRM host, for TestModesComparedToPywinrm.

pywinrm is the WinRM client Ansible's winrm connection plugin is built on,
so this is the industry alternative the Go gate compares pkg/winrmexec
against: the same host, the same account, the same certificate, the same
commands, measured the same way.

It reads one JSON document on stdin:

    {"endpoint": "https://host:5986/wsman",
     "cert": "/dev/fd/3", "key": "/dev/fd/4", "ca": "/dev/fd/5",
     "workloads": [{"label": "...", "n": 40, "workers": 4,
                    "kind": "cmd" | "ps", "command": "..."}]}

The certificate, key and authority arrive as paths to memory-only files the
Go test created and handed down, so the private key is never written to
disk. It prints one JSON document on stdout: for each label, the duration
of every call in seconds and any error text.

Each worker thread holds its own Session, and each call is one run_cmd or
run_ps, which opens a shell, runs the command, and closes the shell again:
the same unit of work as one pkg/winrmexec Execute call. A Session reuses
its HTTPS connection between calls; pkg/winrmexec builds a client per call
on purpose. That difference is part of what is being measured.
"""

import json
import sys
import threading
import time

import winrm


def run(spec, workload):
    """Runs one workload and returns its durations and errors."""
    durations = [None] * workload["n"]
    errors = []
    lock = threading.Lock()
    next_index = [0]

    def worker():
        session = winrm.Session(
            spec["endpoint"],
            auth=("", ""),
            transport="certificate",
            cert_pem=spec["cert"],
            cert_key_pem=spec["key"],
            server_cert_validation="validate",
            ca_trust_path=spec["ca"],
        )
        while True:
            with lock:
                i = next_index[0]
                if i >= workload["n"]:
                    return
                next_index[0] += 1
            start = time.perf_counter()
            try:
                if workload["kind"] == "ps":
                    result = session.run_ps(workload["command"])
                else:
                    result = session.run_cmd(workload["command"])
                elapsed = time.perf_counter() - start
                if result.status_code != 0:
                    with lock:
                        errors.append("exit %d: %s" % (result.status_code, result.std_err[:200]))
            except Exception as exc:  # any failure is reported, not hidden
                elapsed = time.perf_counter() - start
                with lock:
                    errors.append("%s: %s" % (type(exc).__name__, exc))
            durations[i] = elapsed

    threads = [threading.Thread(target=worker) for _ in range(workload["workers"])]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return {"durations": [d for d in durations if d is not None], "errors": errors}


def main():
    spec = json.load(sys.stdin)
    results = {w["label"]: run(spec, w) for w in spec["workloads"]}
    json.dump(results, sys.stdout)


if __name__ == "__main__":
    main()
