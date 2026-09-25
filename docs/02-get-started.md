---
status: beta
---

# Get started

## Quickstart: Crawl tier

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
# Pleiades static inventory (Crawl tier: no server, no database, no broker).
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

A device with no type of its own gets a generic one, and its capabilities come from the
device rather than from the inventory. It starts `discovered`, which runs nothing, until it
is onboarded once its credential is stored (step 4):

```bash
$ pleiades add-host edge1 --type generic_ssh --set host=10.0.0.5
$ pleiades onboard edge1        # after add-credential; --json prints the same result as JSON
```

See [Extending Pleiades](11-extending-pleiades.md#before-writing-a-device-type-the-generic-types)
for the four generic types and what each probe proves.

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

`ssh_exec` is a legacy action name, kept working, and it is the shortest thing to
start with because it needs no module lookup. It is not the only thing that reaches a
device: 77 of the catalog's 81 methods are implemented and run against real devices,
including `exec.command`, which is the modern equivalent of this task. See the
[module catalog](reference/modules/index.md). This one checks that a webserver
answers:

```yaml
# runbooks/check_web1.yaml
id: check_web1
hosts: web1
tasks:
  - name: Confirm the webserver answers
    ssh_exec:
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
    ssh_exec:
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

### 8. See what a run would change first

`--mode check` is a dry run. Each task whose method can say what it would change does
so and changes nothing; each task that cannot is named, and the command then ends
non-zero so a check never reads as a pass it did not earn. No run journal is written.

```yaml
# runbooks/site.yaml
id: site
hosts: web1
tasks:
  - name: Make the release directory
    file.directory:
      path: /tmp/app/releases
      mode: "0750"
  - name: Record the build
    exec.command:
      cmd: /bin/sh -c "date > /tmp/app/built-at"
```

```console
$ pleiades run runbooks/site.yaml --mode check --verbose
plan for runbooks/site.yaml (2 nodes, 1 inventory hosts loaded):
mode: check (tasks report what they would change; nothing on any device is changed)
service-effecting: false
blast radius: 1 devices

tasks:
  Make the release directory
  Record the build

checking:
  tasks[0] [d255cc19-1d72-47c0-8fa1-b0ab2572a3bc]: would change
    diff: map[after:map[exists:true kind:directory mode:0750] before:map[exists:false kind:absent] predicted:true]
    path: /tmp/app/releases
    predicted: true
  tasks[1] [d255cc19-1d72-47c0-8fa1-b0ab2572a3bc]: COULD NOT CHECK (exec.command cannot be checked: what a command changes cannot be known without running it; a creates or removes guard would say what its having run looks like, and a check would read that)

pleiades: check incomplete: 1 task(s) could not be checked, so this check does not cover them (nothing was changed)
```

A check ends with status 0 when every task was checked, 3 when some could not be
checked and nothing failed, and 1 when anything failed, even if something was also
unchecked, so a pipeline can accept an incomplete check without hiding a failure. To
accept particular gaps on purpose, name their method: `--allow-unchecked exec.command`
(repeatable) still lists those tasks, but a check whose only gaps are theirs ends with
0, and any other method left unchecked still ends it with 3.

Every result a check produces says `predicted: true`, and so does its diff, so a result
that is only a prediction cannot be mistaken for one that happened, wherever it is read
later: a later task's condition, or a stored result.

The first task read the device and predicted exactly what it would do; its diff's
`after` half leaves out the owner and group, because a new directory gets those from
the device and a prediction does not guess. The second task runs an arbitrary command,
and what a command would change cannot be known without running it, so it is named
rather than guessed. Give it a `creates` or `removes` guard and it can be checked: the
check reads the guard's path and reports whether the command would run, running
nothing. After a real run, the same check reports `tasks[0]: ok`. Which methods can be
checked, and for a few of them which calls, is on each one's page in the
[module catalog](reference/modules/index.md), under "Check mode".

A runbook can ask for this itself, with Ansible's own key. `check_mode: true` at the top
of a runbook makes every run of it a check, whatever `--mode` says. On a task, or on a
block (which covers its `rescue` and `always` tasks too), it checks just those tasks in
an otherwise real run: they report `would change` or `ok (checked only)`, and they are
left out of the run journal.

```yaml
  - name: Make the release directory
    check_mode: true
    file.directory:
      path: /tmp/app/releases
      mode: "0750"
```

Three things are refused when the runbook is validated. `check_mode: false` would run
a task for real during a check, which is the one thing a check promises not to do, so it
is not accepted anywhere. `check_mode: true` on a task that could only ever be named
unchecked is refused too, naming why: a method with no check, an `exec.command` or
`exec.shell` with neither `creates` nor `removes`, or an `http.request` in a method other
than GET, HEAD, OPTIONS or TRACE. And a task that runs for real may not base its `when`
on a checked task's registered result, because that result is only a prediction of what
the checked task would have done.

A condition in a check is answered wherever it can be. A task that could not be checked
registers nothing, so a later `when` reading its result has no answer, and that task is
named as unchecked too. But a condition the rest of it settles is answered anyway: a
`when_or` list with another member that holds is checked whatever the unchecked task
would have registered, and a `when` list with a member that is false is skipped, just as
a real run would skip it. A condition that is simply wrong, such as one reading a
register no earlier task registers, fails the check the way it would fail a real run.

## Quickstart: Walk tier

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
$ go run internal/ent/migrate/gen/main.go sqlite add_jobs
wrote internal/ent/migrate/migrations/sqlite/0004_add_jobs.sql
```

The generator takes the dialect first, because the controller supports both
PostgreSQL and SQLite and a schema change has to be generated for each. Run it
once per dialect.

Kept here rather than smoothed over, because it is the second time in this same
documentation effort that a real run surfaced a real defect (see
[`examples/webserver_lab/README.md`](../examples/webserver_lab/README.md#a-real-finding-kept-rather-than-smoothed-over)
for the first). Running the thing is what finds what prose alone does not.

### Start the mesh

```bash
docker run -d --name pleiades-nats -p 4222:4222 nats:2.14.4-alpine -js -sd /data -m 8222

./controller setup --dir . --non-interactive
set -a; . ./.env; set +a
export NATS_URL="nats://127.0.0.1:4222"
export DB_DSN="sqlite://./controller.db"

./controller
```

`controller setup` makes the two secrets the controller refuses to start without, a master
encryption key and a JWT secret, at the sizes it requires, and writes them to `.env`. The
`set -a` line hands them to this shell for the bare binary. `.env` is then the only copy of
the key that encrypts every credential this controller stores, so keep it.

One consequence of exporting them: a shell that has loaded `.env` this way now carries the
key as an environment variable, and `make up` refuses to run from it, because docker compose
would use the shell's value over the one in `.env`. Use a fresh shell for the compose stack.

That is deliberately the same broker `docker-compose.yml` runs, down to the flags, and a test
(`internal/testsupport`'s `TestGettingStartedRunsThePinnedBroker`) fails if this line and that
file stop agreeing. Running a different broker from the one you deploy is how a quickstart
quietly stops representing the thing it documents.

Each piece, since none of it is decoration:

- `nats:2.14.4-alpine` carries the identical nats-server binary as plain `nats:2.14.4`, so
  JetStream behaves the same either way. It is the only published build of that version that
  also contains a program able to probe the server from inside the container, which is what
  the compose stack's healthcheck needs.
- Passing any command at all replaces the image's default one, which was a config file. Every
  setting that file used to make has to be made again as a flag, which is what the other two
  are.
- `-sd /data` puts the JetStream store somewhere that survives a restart. Without it the
  server uses a temporary directory and says so in its own log.
- `-m 8222` opens the monitoring port. Nothing below needs it, but a broker you cannot ask
  about is a bad habit to start with.

Nothing here configures TLS, and the controller still speaks `https`. With no
certificate configured it writes a self-signed one into `./tls/` (gitignored) on the
first start and reuses it on every later one. It never serves plain HTTP unless
`PLEIADES_TLS_TERMINATED_UPSTREAM=1` says an ingress in front of it has already
terminated TLS.

The reason is the session cookie, which is `Secure` and `__Host-` prefixed: a browser
refuses such a cookie on an unencrypted origin, and says nothing about having done so,
which surfaces as a rejected sign-in for a correct password. Loopback is the single
exception browsers make, so a `localhost` trial like this one would in fact have
worked over plain HTTP, and any other address would not.

That certificate encrypts the connection and does not authenticate the server, which
is why every `curl` below passes `--cacert ./tls/cert.pem` and why a browser warns
once. A real deployment sets `TLS_CERT_FILE` and `TLS_KEY_FILE` instead, and those
always win; see [Running in production](10-running-in-production.md#pki-and-tls).

`DB_DSN` names the database. A `postgres://` URL points the controller at a real
PostgreSQL server, which is what a multi-user deployment runs:

```bash
export DB_DSN="postgres://pleiades:password@127.0.0.1:5432/pleiades?sslmode=disable"
```

A `sqlite://` URL, or a bare filesystem path, uses an on-disk SQLite file instead,
which needs no server and suits a single-process trial like this one. The older
`DB_PATH` variable still works and still means SQLite. Setting both `DB_DSN` and
`DB_PATH` is a startup error rather than a silent preference for one of them.

```console
{"level":"INFO","msg":"controller listening","addr":":8080","scheme":"https"}
{"level":"INFO","msg":"Acquired Scheduler Lease","key":"pleiades-scheduler-leader"}
```

```console
$ curl -s --cacert ./tls/cert.pem https://localhost:8080/readyz
{"status":"ready","checks":{"database":"ok","nats":"ok"}}
```

### Launch a job over the real API

Minted a JWT signed with the same `JWT_SECRET`, claiming `role: admin` (see
`cmd/demo/main.go` for the same pattern this project's own demo binary uses).

A launch names a **template**: the saved definition of what to run, where to run it,
and how. Create one first, naming the inventory it targets:

```console
$ curl -s --cacert ./tls/cert.pem -X POST "https://localhost:8080/api/v1/templates" \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"name":"sample","kind":"runbook","definition":"sample","inventory":1}'
{"id":1,"name":"sample","kind":"runbook","kind_label":"Runbook","definition":"sample","inventory":1,"organization":1,"prompts":[],"allow_simultaneous":false}
```

Then launch it:

```console
$ curl -s --cacert ./tls/cert.pem -X POST "https://localhost:8080/api/v1/templates/1/launch" \
    -H "Authorization: Bearer $TOKEN"
{"_links":[{"rel":"execute","href":"/api/v1/templates/1/launch","method":"POST"}],"status":"accepted","job_id":"2cd07f0c-460d-40e4-a8d9-498314c7a5aa","ignored_fields":[]}
```

`ignored_fields` is empty here because this launch supplied nothing. A template
declares which of its fields a launch may override; supplying one it does not is
reported by name rather than silently applied or silently dropped.

```console
$ curl -s --cacert ./tls/cert.pem "https://localhost:8080/api/v1/jobs/2cd07f0c-460d-40e4-a8d9-498314c7a5aa" \
    -H "Authorization: Bearer $TOKEN"
{
  "_links": [{"rel": "self", "href": "/api/v1/jobs/2cd07f0c-460d-40e4-a8d9-498314c7a5aa", "method": "GET"}],
  "job_id": "2cd07f0c-460d-40e4-a8d9-498314c7a5aa",
  "runbook_id": "sample",
  "group_name": "",
  "state": "completed",
  "dispatched": 0,
  "skipped": 0,
  "failed": 0,
  "tasks": []
}
```

`dispatched: 0` is honest, not broken: the inventory this template names holds no
devices yet. The Controller's own inventory is a separate store from the Crawl-tier
CLI's local `inventory.yaml`, and populating it means creating devices
(`POST /api/v1/inventory/devices`), grouping them, and putting those groups in an
Inventory (`POST /api/v1/inventories`) for a template to target.

### Watch the log stream

```console
$ curl -s -N --cacert ./tls/cert.pem "https://localhost:8080/api/v1/jobs/2cd07f0c-460d-40e4-a8d9-498314c7a5aa/logs" \
    -H "Authorization: Bearer $TOKEN"
event: init
data: connected to job 2cd07f0c-460d-40e4-a8d9-498314c7a5aa
```

A real Server-Sent Events connection, over a real HTTP response, authenticated the
same way every other route is. No log events follow yet in this example, for the same
reason `dispatched` was 0 above.

### The HATEOAS `Allow` header is real too

```console
$ curl -s --cacert ./tls/cert.pem -X OPTIONS "https://localhost:8080/api/v1/templates/1/launch" -H "Authorization: Bearer $TOKEN" -i
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
