---
status: beta
---

# Get started

## Quickstart: Walk tier

Everything below is real, captured output from a real run: `pleiades` against a live
Ubuntu container over SSH, the same lab set up in
[`examples/webserver_lab/`](../examples/webserver_lab/). Nothing here is
hand-written to look plausible.

### 1. Build the binary

```console
$ go build -o pleiades ./cmd/pleiades
```

### 2. Scaffold a project

```console
$ pleiades init
created:
  inventory.yaml
  runbooks/sample.yaml
  README.md
```

`pleiades init` never overwrites a file that already exists, so running it again in
the same directory is safe and just fills in whatever is missing.

### 3. Add a host

```console
$ pleiades add-host web1 --type linux_server --set host=127.0.0.1 --set port=2222 --tags webserver
added host "web1" (linux_server) to inventory.yaml
```

`inventory.yaml` now has a real entry, written by the CLI, not by hand:

```yaml
# Pleiades static inventory (Walk tier: no server, no database, no broker).
# Add hosts by hand below, or run: pleiades add-host <name> --type <type>
hosts:
    - id: 6e3dd54a-763e-45f2-ae77-dd9fd90d0898
      name: web1
      type: linux_server
      tags:
        - webserver
      properties:
        host: 127.0.0.1
        port: 2222
```

### 4. Store a credential

```console
$ pleiades add-credential web1 --username svc-netauto
password:
stored credential for device "web1"
```

With neither `--password` nor `--key` given, `add-credential` prompts interactively
with no echo, so a password never lands in shell history. The credential is written
to `.pleiades/credentials.yaml`, AES-256-GCM encrypted with a locally held master key
(`.pleiades/master.key`, generated on first use). Neither file is ever committed:
both are gitignored by default.

### 5. Validate

```console
$ pleiades validate
validate: no issues found
```

`validate` checks the runbook against the inventory without touching any device:
every task's FQCN resolves to a registered, implemented method, and every conditional
expression compiles.

It does not check capabilities for catalog FQCNs. Only the two legacy action names
`ssh_exec` and `ios_backup` get a device capability check, so a task calling
`net.catalyst.device_facts` against a Linux host passes `validate` and then fails
during the run. See
[Implementation status](01-start-here.md#implementation-status).

### 6. Run it

The scaffolded sample runbook is deliberately inert (a single `noop` task), so it
runs cleanly against any inventory with no setup:

```console
$ pleiades run runbooks/sample.yaml
plan for runbooks/sample.yaml (1 nodes, 1 inventory hosts loaded):
service-effecting: false
blast radius: 0 devices

tasks:
  check

executing:
  tasks[0]: ok

run complete
```

### 7. Run something real

`ssh_exec` is the one action that genuinely reaches a device today. This one checks
that a webserver answers:

```yaml
# runbooks/check_web1.yaml
id: check_web1
hosts: web1
tasks:
  - name: Confirm the webserver answers
    fqcn: ssh_exec
    params:
      command: curl -s -o /dev/null -w "%{http_code}" http://localhost/
```

```console
$ pleiades run runbooks/check_web1.yaml
plan for runbooks/check_web1.yaml (1 nodes, 1 inventory hosts loaded):
service-effecting: false
blast radius: 1 devices

tasks:
  Confirm the webserver answers

executing:
  tasks[0] [6e3dd54a-763e-45f2-ae77-dd9fd90d0898]: changed

run complete
```

Run it again, unchanged, and it reports `changed` a second time. This is not a bug:
`ssh_exec` has no way to know whether an arbitrary shell command altered anything, so
it defaults `changed` to `true`, the opposite of a real Ansible module's
auto-detected convergence. For a read-only check like this one, say so explicitly:

```yaml
    params:
      command: curl -s -o /dev/null -w "%{http_code}" http://localhost/
      changed: false
```

```console
$ pleiades run runbooks/check_web1.yaml
...
executing:
  tasks[0] [6e3dd54a-763e-45f2-ae77-dd9fd90d0898]: ok

run complete
```

Run it again and it reports `ok` again, both times, because the runbook now says so
explicitly. This is the whole of `ssh_exec`'s change-detection story today: an author
states it, the engine does not infer it.

## Quickstart: Crawl tier

**Status: real infrastructure, real execution.** Everything below is captured from a
real local mesh: a real NATS JetStream container and a real `controller` binary,
authenticated with a real signed JWT. A `runner` that picks up one of these dispatches
executes the runbook against the named device for real, over SSH. Before pointing this
at anything you care about, read the credential-handling limits in
[Implementation status](01-start-here.md#implementation-status).

### A bug found and fixed while writing this

The very first dispatch attempt below failed with `"no such table: jobs"`. The
`Job`/`JobTask` ent schema (`internal/ent/schema/job.go`, `job_task.go`) had never had
a matching migration generated, so on any deployment, not just this lab, the entire
dispatch endpoint was unreachable. Fixed by running the tool built for exactly this:

```console
$ go run internal/ent/migrate/gen/main.go add_jobs
wrote internal/ent/migrate/migrations/sqlite/0004_add_jobs.sql
```

Kept here rather than smoothed over, because it is the second time in this same
documentation effort that a real run surfaced a real defect (see
[`examples/webserver_lab/README.md`](../examples/webserver_lab/README.md#a-real-finding-kept-rather-than-smoothed-over)
for the first). Running the thing is what finds what prose alone does not.

### Start the mesh

```bash
docker run -d --name pleiades-nats -p 4222:4222 nats:latest -js

export MASTER_ENCRYPTION_KEY="$(openssl rand -base64 32)"
export JWT_SECRET="a-real-secret-at-least-32-bytes-long"
export NATS_URL="nats://127.0.0.1:4222"
export DB_PATH="./controller.db"

./controller
```

```console
{"level":"INFO","msg":"controller listening","addr":":8080"}
{"level":"INFO","msg":"Acquired Scheduler Lease","key":"pleiades-scheduler-leader"}
```

```console
$ curl -s http://localhost:8080/readyz
{"status":"ready","checks":{"database":"ok","nats":"ok"}}
```

### Dispatch a job over the real API

Minted a JWT signed with the same `JWT_SECRET`, claiming `role: admin` (see
`cmd/demo/main.go` for the same pattern this project's own demo binary uses):

```console
$ curl -s -X POST "http://localhost:8080/api/v1/jobs/dispatch?group=lab&runbook=sample" \
    -H "Authorization: Bearer $TOKEN"
{"_links":[{"rel":"execute","href":"/api/v1/jobs/dispatch","method":"POST"}],"status":"accepted","job_id":"2cd07f0c-460d-40e4-a8d9-498314c7a5aa"}
```

```console
$ curl -s "http://localhost:8080/api/v1/jobs/2cd07f0c-460d-40e4-a8d9-498314c7a5aa" \
    -H "Authorization: Bearer $TOKEN"
{
  "_links": [{"rel": "self", "href": "/api/v1/jobs/2cd07f0c-460d-40e4-a8d9-498314c7a5aa", "method": "GET"}],
  "job_id": "2cd07f0c-460d-40e4-a8d9-498314c7a5aa",
  "runbook_id": "sample",
  "group_name": "lab",
  "state": "completed",
  "dispatched": 0,
  "skipped": 0,
  "failed": 0,
  "tasks": []
}
```

`dispatched: 0` is honest, not broken: the Controller's own inventory (a separate
store from the Walk-tier CLI's local `inventory.yaml`) has no devices in group `lab`
yet, and there is no device-create endpoint in the API today, only `GET` and
`DELETE`. Populating the Controller's inventory is not yet a documented path; this
page will grow one once it exists.

### Watch the log stream

```console
$ curl -s -N "http://localhost:8080/api/v1/jobs/2cd07f0c-460d-40e4-a8d9-498314c7a5aa/logs" \
    -H "Authorization: Bearer $TOKEN"
event: init
data: connected to job 2cd07f0c-460d-40e4-a8d9-498314c7a5aa
```

A real Server-Sent Events connection, over a real HTTP response, authenticated the
same way every other route is. No log events follow yet in this example, for the same
reason `dispatched` was 0 above.

### The HATEOAS `Allow` header is real too

```console
$ curl -s -X OPTIONS "http://localhost:8080/api/v1/jobs/dispatch" -H "Authorization: Bearer $TOKEN" -i
HTTP/1.1 204 No Content
Allow: OPTIONS, POST
```

The `Allow` header is generated from the same admission chain that enforces 403s, so
it cannot advertise a method the caller is not actually permitted to use.

## Your second runbook

The sample runbook and the check above cover a single task against a single host.
Next: [`examples/upgrade_ios/`](../examples/upgrade_ios/) writes a real, multi-task
runbook side by side with the Ansible playbook it replaces, including `block`,
`register`, `register_mask`, and `when_cel`.
