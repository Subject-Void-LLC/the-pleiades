---
status: beta
---

# Running in production

This book covers what a real deployment needs to know before it runs Pleiades
against production infrastructure: what happens when something fails partway
through, what is checked before anything runs, how credentials and secrets are
handled, and what is genuinely built versus still design work. As with every page
in this reference, a claim here is either backed by real, tested code or is
explicitly marked otherwise; nothing below is aspirational.

## Installing

There are two supported ways to run the control plane: `docker-compose.yml` on a
single Docker host, and the Helm chart in `helm/the-pleiades` on Kubernetes. This
section is about the second one. Everything below has been rendered and checked;
where something has not been run against a real cluster, it says so.

### Nothing is published, so you build the images first

There is no registry hosting a Pleiades image and no tagged release of this
repository. That is the single most important fact about installing it, because
every other instruction follows from it.

Build both images from the commit you intend to run:

```bash
VCS_REF=$(git rev-parse HEAD)
docker build -f Dockerfile.controller --build-arg VCS_REF="$VCS_REF" -t pleiades/controller:dev .
docker build -f Dockerfile.runner     --build-arg VCS_REF="$VCS_REF" -t pleiades/runner:dev .
```

`pleiades/controller:dev` and `pleiades/runner:dev` are the names the chart asks
for by default, and they are exactly what `docker compose build` produces, so a
locally built or side-loaded image is found with no extra flags. Push them to
your own registry and point `controller.image.repository` and
`runner.image.repository` there for anything beyond a first look.

If the images are missing, `helm install` still reports success. The failure
appears as `ImagePullBackOff` in `kubectl get pods` and, in more detail, in
`kubectl get events`. Check the pods, not the install command.

### Installing the chart

Three values have no default and the chart refuses to render without them:

```bash
helm install pleiades ./helm/the-pleiades \
  --set secrets.masterEncryptionKey="$(openssl rand -base64 32)" \
  --set secrets.jwtSecret="$(openssl rand -hex 32)" \
  --set postgresql.auth.password="$(openssl rand -hex 16)"
```

The chart will not generate them for you, and that is deliberate. Helm can
produce a random value, but it would produce a *different* one on the next
`helm upgrade`, because `helm template` has no cluster to read the previous value
back from. A rotated `JWT_SECRET` signs everybody out, which is annoying and
recoverable. A `MASTER_ENCRYPTION_KEY` replaced without
keeping the old one makes every stored credential and every encrypted device
property permanently undecryptable, with no error at upgrade time. Rotating it
properly, by keeping the old key in the previous slot and running a rotation
pass, is described under [Rotating the master key](#rotating-the-master-key)
below. Keep all three somewhere you can find again, or hand the chart a
Secret you manage yourself with `secrets.existingSecret`.

Then create the first administrator, which is the real next step and the one the
install notes print:

```bash
kubectl exec -it deploy/pleiades-the-pleiades-controller -- \
  /app/controller bootstrap-admin --email you@example.com
```

What a default install gives you: one controller, two runners, and a
single-instance PostgreSQL and NATS deployed as part of the release. That is a
trial shape. For anything real, set `postgresql.enabled=false` and
`nats.enabled=false` and point `externalDatabase.dsn` and `externalNats.url` at
services somebody already knows how to back up and restrict. The in-chart broker
in particular runs with no authorization at all, and a dispatch payload carries
the credentials a job runs with.

The chart has no subchart dependencies, on purpose. PostgreSQL and NATS are
templated into it rather than pulled from a chart repository, so there is no
`helm dependency build` step and the whole chart is one directory. That is what
makes the air-gapped install below work.

### What the chart refuses to install

Some configurations are refused at render time, with the reason in the error,
rather than installing cleanly and behaving wrongly later:

| Configuration | Why it is refused |
|---|---|
| `runner.playbookDir` or `runner.ansibleImage` | The legacy Ansible path runs a playbook by starting a **sibling container through a Docker daemon**. A pod has no Docker daemon. The two ways to give it one are mounting the node's container runtime socket (which hands the pod control of every container on that node) or a privileged Docker-in-Docker sidecar (the same authority in a different shape). Run unconverted playbooks on a Docker host with `docker-compose.yml` instead. |
| `controller.tls.mode=self-provisioned` with more than one replica **and `controller.persistence.enabled=false`** | Several self-provisioning controllers sharing one directory are fine: provisioning is lock-free, none of them waits on another, and the directory converges on one certificate. With persistence off there is no shared directory at all: each replica's `/data` is its own `emptyDir`, so each mints its own certificate and a new one on every restart, and there is nothing to converge on. Share a directory with `persistence.accessMode=ReadWriteMany`, or scale out with `mode=secret` or `mode=upstream`. |
| More than one controller replica over a `ReadWriteOnce` or `ReadWriteOncePod` volume | The chart creates one claim, not one per replica, because a Deployment has no `volumeClaimTemplates`. A second replica scheduled on another node would sit `Pending` forever. `ReadWriteMany` is the only access mode that serves more than one replica; the alternatives are turning persistence off or staying at one replica. |
| An image tagged `latest`, untagged, or pinned by digest | `latest` names a different image tomorrow. A digest does not resolve against a side-loaded image, which breaks exactly the air-gapped install below. |
| A missing encryption key, JWT secret, or database password | See above. |
| A mistyped values key | `values.schema.json` sets `additionalProperties: false`, so `controller.replicaCounts=3` is an error instead of a silently ignored setting that leaves the default in place. |
| An enabled `podDisruptionBudget` that sets both `minAvailable` and `maxUnavailable`, or neither | A PodDisruptionBudget carries one or the other, and the API server rejects an object with both. Setting neither used to render no budget at all, so an operator who believed disruption protection was on had none and nothing said so. Note that `0` counts as an answer on either field: `minAvailable: 0` permits every voluntary eviction and `maxUnavailable: 0` permits none. |
| An `ingress.hosts[].paths[]` entry with no `pathType` | `networking.k8s.io/v1` requires `pathType` on every path, so an entry without one produces an object the API server refuses. The schema requires the key and the template also defaults it to `Prefix`. |
| Installing over a retained database volume with different credentials | See [Reinstalling over a database that is still there](#reinstalling-over-a-database-that-is-still-there) below. This is the one refusal that reads the cluster, so it fires on a real `helm install` or `helm upgrade` and not during `helm template`. |

`make helm-lint` is the gate that keeps these true. It renders the chart in five
arrangements, checks that every rendered container has a numeric non-root uid, a
read-only root filesystem, no capabilities, the default seccomp profile, no
`hostPath` volume and no `latest` tag or digest, and then checks that each
refusal above really fires with its reason intact. It needs Helm 4 and no
cluster.

It also renders the chart at **release-name lengths 1, 20, 30, 31, 40, 44, 48,
49, 50, 52 and 53** (twice at each length, since a name containing
`the-pleiades` takes a different branch of the naming helper, and the two
branches cross these boundaries at different points) and requires every object
name to stay
distinct, to keep its component word, and to be legal **for its own kind**.
Neither half is hypothetical, and they were two different bugs.

A release name of 49 to 53 characters, every one of them legal to Helm, used to
truncate the controller, the runner, the PostgreSQL StatefulSet and the NATS
StatefulSet to **one identical name**, so the objects overwrote each other on
apply. Names are now built by cutting the release-scoped prefix first and
appending the component second, so a long release name loses characters from a
part nobody reads.

The fix for that held every kind to 63 characters, which is right for a Service
and 11 characters too generous for a StatefulSet. Kubernetes labels each
StatefulSet pod with `controller-revision-hash`, whose value is
`<statefulset name>-<hash>` and which may not exceed 63 bytes, so a longer
StatefulSet name is **created successfully and then produces no pods at all**:
`helm install` reports success, the object sits at `0/1` forever, and the only
evidence is a `FailedCreate` event. Verified against a real cluster in both
directions. The two StatefulSets therefore get a lower budget than the two
Deployments, and the linter now asserts the names Kubernetes *derives* from
each object name as well as the names the chart writes.

**One container is exempt from the probe rule, by name and with a written
reason: the runner.** It has no liveness or readiness probe because there is
nothing inside the pod to ask. `cmd/runner` binds no port, has no HTTP surface
and no `healthcheck` subcommand, and its image is distroless, so it carries no
shell and no `curl`. The one executable in the image is the runner binary
itself, and it treats any first argument it does not recognise as an ordinary
start, so an `exec` probe running it would launch a **second runner agent into
the consumer group every few seconds**. There is no probe a chart can express
that would help here.

Its real liveness question is whether it is still pulling from its durable NATS
consumer, which is a fact about a connection held inside the process. The
practical consequence for an operator: **Kubernetes cannot tell a wedged runner
from a working one.** A runner that is up but has stopped consuming looks
healthy. Watch for it on the controller side instead, as a job that stays queued
with no runner claiming it, or on the broker as a climbing pending count.

Closing that gap needs a `healthcheck` subcommand on the binary, in the shape
`cmd/controller/healthcheck.go` already has, not a chart change. The waiver
carries that condition rather than only the reason: `make helm-lint` re-reads
`cmd/runner` on every run and fails, with the chart edit spelled out, the day a
healthcheck subcommand appears there. Every other container is required to carry
both probes, on different endpoints, and a new one arriving without them fails
the build.

### Verifying what you are about to install

Be clear about what verification is available today, because it is less than a
mature project offers:

**There is no signed release.** No git tags exist, so there is nothing to sign,
no published checksums, no cosign signature, no SBOM, and no provenance
attestation. Anyone telling you they downloaded a Pleiades release verified it
against nothing.

What you can do instead:

1. **Build from a commit you chose.** The images carry OCI labels, so a built
   image records where it came from: `docker inspect -f '{{json .Config.Labels}}'
   pleiades/controller:dev` prints `org.opencontainers.image.revision`. A build
   that passed no `--build-arg VCS_REF` stamps `unknown`, which is honest and
   useless, so pass it.
2. **Record the image ID you built** (`docker image inspect -f '{{.Id}}'
   pleiades/controller:dev`) and compare it with what actually landed on the
   node (`crictl images --digests`, or `kubectl describe pod` for the resolved
   image). This is what catches a stale side-load, which is the realistic
   failure here rather than a supply-chain attack.
3. **Render before you install.** `helm template` needs no cluster:

   ```bash
   helm template pleiades ./helm/the-pleiades --set ... | kubectl apply --dry-run=client -f -
   ```

   That parses every object and catches a values mistake before anything runs.
   With a cluster you can reach, `--dry-run=server` is strictly better: the API
   server itself validates the manifests, so a required field the client-side
   parse cannot know about (an Ingress path with no `pathType`, a
   PodDisruptionBudget carrying two budget fields) is reported by the thing that
   would have rejected it.

### Reinstalling over a database that is still there

`helm uninstall` does **not** delete the PostgreSQL data volume, and that is
deliberate: the claim is created by a StatefulSet `volumeClaimTemplate`, which
Kubernetes leaves behind on purpose, and a chart that destroyed a database on
uninstall would be the more dangerous chart. So a reinstall under the same
release name lands on a volume that already holds an initialized database.

PostgreSQL applies `POSTGRES_USER`, `POSTGRES_DB` and `POSTGRES_PASSWORD` **only
when it initializes an empty data directory**. Against a volume that already
holds a database it ignores all three. A reinstall with a different password
therefore used to install cleanly, report every object created, bring the
database up healthy, and leave the controller failing authentication and
crash-looping forever with nothing anywhere naming the cause.

The chart now refuses that. The StatefulSet stamps a hash of the three
credentials onto its `volumeClaimTemplate`, Kubernetes copies it onto the claim,
and the claim outlives the release, so the next install compares and stops. The
refusal names the claim and both real choices:

- **Keep the data.** Install with the `postgresql.auth` values that volume was
  created with. Nothing is destroyed.
- **Start over.** `kubectl delete pvc <claim> --namespace <namespace>`, then
  install. **This destroys the database**: every user, device, template, job,
  credential and role binding in it. There is no undo and this chart takes no
  backups.

Two boundaries worth stating. The check needs a cluster to read, so it is silent
under `helm template` and `--dry-run=client` and fires on a real `helm install`,
`helm upgrade` or `--dry-run=server`. And it refuses only a **proven** mismatch:
a claim this chart did not create carries no stamp, and `secrets.existingSecret`
means the chart never sees the password, so in both cases it has no evidence and
does not guess. `tests/e2e/packaging_kind_test.go` proves the whole sequence
against a real cluster: install, uninstall, reinstall with a changed password
(refused, with the claim named), then reinstall with the original one
(accepted).

### Air-gapped install

The chart is built to install with no route to the internet. The sequence:

```bash
# On a connected machine.
docker build -f Dockerfile.controller -t pleiades/controller:dev .
docker build -f Dockerfile.runner     -t pleiades/runner:dev .
docker save pleiades/controller:dev pleiades/runner:dev -o pleiades-images.tar
helm package ./helm/the-pleiades          # produces the-pleiades-0.1.0.tgz

# Move pleiades-images.tar and the-pleiades-0.1.0.tgz across.

# On each node that will run a Pleiades pod (containerd):
ctr -n k8s.io images import pleiades-images.tar
# or, for a kind cluster:
kind load image-archive pleiades-images.tar

helm install pleiades ./the-pleiades-0.1.0.tgz \
  --set secrets.masterEncryptionKey="$(openssl rand -base64 32)" \
  --set secrets.jwtSecret="$(openssl rand -hex 32)" \
  --set postgresql.auth.password="$(openssl rand -hex 16)"
```

Three things make that work, and each of them is a decision that can be undone
by accident:

- **The images are referenced by tag, never by digest.** A digest-pinned
  reference does not resolve against a side-loaded image, even when the local
  daemon reports that exact digest. Digest pinning is normally the stronger
  practice, and here it breaks every air-gapped, side-loaded and kind-based
  install. The chart refuses a digest for that reason.
- **`imagePullPolicy` is `IfNotPresent`.** Setting it to `Always` forces a pull
  that cannot succeed, no matter what is already on the node.
- **The chart has no dependencies**, so nothing needs a chart repository.

You also need the two backend images (`postgres:15.19-alpine` and
`nats:2.14.4-alpine`) in the same archive if you are using the in-chart
database and broker, since they are pulled the same way everything else is.

**Verified against a real cluster, with a stated boundary.**
`tests/e2e/packaging_kind_test.go` performs this whole sequence on every
integration run: it creates a single-node kind cluster at a pinned Kubernetes
version, side-loads both locally built images into the node's containerd store,
installs this chart with `imagePullPolicy=Never` so a missing side-load fails
loudly instead of being quietly pulled, and then proves the controller
Deployment reaches `Available`, that `/readyz` answers
`{"status":"ready","checks":{"database":"ok","nats":"ok"}}` over the certificate
the controller provisioned for itself, and that `bootstrap-admin` works through
`kubectl exec`.

What that does NOT cover, so nobody reads more into it than it carries: one
node, so nothing about scheduling across nodes, `ReadWriteMany` volumes or a
real StorageClass is exercised; the two backend images are pulled from the
registry rather than side-loaded, because a multi-architecture image cannot be
side-loaded into a kind node, so a genuinely air-gapped install of PostgreSQL
and NATS is still the carefully constructed sequence above rather than a
transcript; and no Ingress, no cert-manager and no external database are
installed. The chart also renders in five arrangements under `make helm-lint`,
passes `helm lint --strict`, and every object it produces is accepted by
`kubectl apply --dry-run=server`, which is the API server's own validation
rather than a client-side parse.

## Failure semantics and safety

### A command is never retried once sent

The Crawl-tier CLI's real SSH transport (`internal/transport/ssh`) retries with
backoff and jitter, and short-circuits through a per-target circuit breaker, but
**only during the dial phase**: establishing the TCP connection and completing the
SSH handshake. The moment a command has actually been sent to the remote side, it
is never retried, for a specific reason: a command may have partially executed
remotely already, and blindly retrying it could re-apply an unknown side effect
(delete a file twice, restart a service twice, and so on). A connection lost
mid-session is reported as a genuine error, never silently retried and never folded
into a fabricated exit code.

Practically, this means: a dial failure (device unreachable, port closed, auth
rejected) is retried automatically up to a bounded limit before it is reported as an
error. A failure *after* the command was sent (the connection drops mid-run, the
device reboots unexpectedly) is reported as an error immediately, and it is the
caller's job to determine what state the device was left in and re-run explicitly
once that is known, not Pleiades' job to guess.

### Every task execution is recorded in a run journal

Every node a run executes leaves a durable record: what ran, against which device,
in what order, how long it took, whether it changed anything, and if it failed, at
which stage. On the Crawl tier that is one append-only JSON Lines file per run at
`<project>/.pleiades/journal/<run-id>.jsonl`, written `0600` inside a `0700`
directory. On the Walk tier the Runner publishes each level onto the job's own
subject and the Controller stores it in the `journal_entries` table, keyed by the
job, the device, the delivery attempt and the graph node, so a redelivered dispatch
reads as a retry rather than as two unrelated runs.

**The journal stores no value that came back from a device.** It is not masked,
because there is nothing in it to mask: it holds identifiers the platform generated,
method names resolved through the collection registry at write time, the parameter
and return key NAMES a method's own documentation declares, closed status values,
a content hash of the compiled runbook, the labels the runbook author wrote, and
counts. It records that a task produced a `stdout`; it does not record what the
device wrote there. Two consequences follow and both matter in practice:

- It is an audit and control-flow artifact, not a diagnostic one. It cannot answer
  what the device actually said. `--verbose` still can.
- It needs no key and nothing decrypts it, on either tier.

The one channel that does carry human-written text is the labels: a task's `name:`,
its `register:` and the runbook's `id:` are stored as written. So the honest
guarantee is "no value the platform obtained", not "no secret a person could type
into a task name".

### What one journal record contains

A Crawl-tier journal file is JSON Lines: one record per line, one line per node
the run executed. The order is the graph's, not the clock's: level by level, and
within a level in graph position. Nodes in a level really do run at the same time
and can finish in any order, so if you need wall-clock order, sort on
`finished_at`; `sequence` gives you run order. Any tool that reads JSON will read
the file, so `jq` is usually enough:

```bash
# every task that changed something, across every run on disk
jq -r 'select(.outcome == "changed") | "\(.task_name)\t\(.fqcn)\t\(.device_id)"' \
  .pleiades/journal/*.jsonl
```

The Walk tier stores the same fields as columns in `journal_entries`.

**Identifiers**

| Field | Meaning |
|---|---|
| `run_id` | One `Executor.Run` call. Names the Crawl-tier file. |
| `sequence` | Order within that run. A level runs concurrently, so two records can share an instant; this cannot tie. |
| `node_id` | Position in the compiled graph, such as `tasks[0]`. Not the task's `register:` name. |
| `device_id` | The inventory item's stored id. Never a device property. |
| `job_id`, `attempt` | The Walk-tier dispatch and its redelivery count. Both empty or zero on the Crawl tier, which has no dispatch. |
| `started_at`, `finished_at` | UTC, bounding this one execution. |

**What ran**

| Field | Meaning |
|---|---|
| `fqcn` | The method, resolved through the collection registry when the record was written. Never the raw text from the runbook. |
| `fqcn_unresolved` | True when the registry knew no such method, in which case `fqcn` reads `unregistered`. |
| `dag_id` | The runbook's own `id:`, as written. |
| `dag_version` | A `sha256:` hash of the compiled runbook, for detecting drift between what ran and what is on disk now. It cannot recover the runbook. |
| `task_name`, `register` | The author's own `name:` and `register:`, as written. These are the only fields carrying free text a person typed. |

**How it ended**

| Field | Meaning |
|---|---|
| `outcome` | One of `ran`, `changed`, `skipped`, `failed`, `not_reached`. |
| `failure_stage` | Where a failure happened, read off control flow rather than parsed from an error: `workflow_read`, `condition_eval`, `secret_mask`, `resolve_target`, `lock_all`, `lock_device`, `action`, `register_mask`, `record`. Empty unless `outcome` is `failed`. |
| `skip_kind` | Which gate skipped the task: `when`, `when_or`, `when_cel` or `lifecycle`. Empty unless `outcome` is `skipped`. |
| `skip_ordinal`, `skip_total` | Which condition of how many decided a `when` skip. The ordinal is zero for `when_or`, where every condition had to be false and none is the actionable one. |

Note what is absent: there is no error message field. A failure records the stage it
happened at and nothing the device or the platform said about it, because that text
is the one place a device's own output reliably ends up.

**Key names and counts**

These record which keys a task produced or consumed, never their values.

| Field | Meaning |
|---|---|
| `stat_keys` | Return key names the method's own documentation declares, plus the two platform keys `inverse` and `diff`. |
| `param_keys` | Parameter names the method declares. |
| `undeclared_stat_count`, `undeclared_param_count` | How many keys were rejected because the registry does not declare them. Counted rather than named, so an undeclared key cannot smuggle text in through its own name. |
| `inverse_fqcn`, `inverse_param_keys` | The method that would reverse this task, and the names of the parameters such a call takes. Not their values, which is why nothing replays automatically. |
| `inverse_fqcn_unresolved` | True when that reversing method is not in the registry. |
| `undeclared_inverse_param_count` | The same counter, for the reversing call. |
| `diff_recorded` | Whether the task recorded a before and after. Not the before or the after. |

### Rollback is authored, not inferred

The journal records the concrete instruction that would reverse a task that changed
something: the method to call and the names of the parameters such a call takes. It
does not record their values, so nothing can replay it automatically, and nothing
in Pleiades performs a rollback today. Undoing a partial run is an authored
runbook you write and run deliberately, with the journal as the record of what
actually happened and therefore of what needs undoing.

### The message bus survives a link outage of any length

**This section is about the control plane only, and the distinction is the whole
point of reading it.** Pleiades has two independent network planes:

- The **control plane** is Controller to NATS to Runner: dispatches out, logs and
  results back, device leases held. Everything below is about that link.
- The **execution plane** is Runner to device: SSH, serial, WinRM. Its resilience
  rules are different and are described in the section above. A command already
  sent to a device is never retried, whatever the network does.

Nothing here makes a session to a device survive anything. What it makes survivable
is a Runner losing contact with the Controller, which in an edge deployment (the
Runner at the far end of a satellite or radio link, the Controller at the teleport)
is the link most likely to disappear. If your Runner sits in the datacentre and
reaches devices over the bad link, this section does not help you; the execution
plane's dial-phase retry and circuit breaker are what apply.

Every connection the Controller and Runner open to NATS is configured from one
place, and that configuration is built for a link that disappears rather than a
datacentre LAN. Three properties matter operationally.

**Reconnection is unlimited.** A connection that drops keeps trying to come back
for as long as the process is running, with exponential backoff between attempts
(250ms growing to a 30s ceiling). There is no outage length at which a process
gives up and needs a manual restart. This was not always true: before this
behaviour was added, an outage longer than roughly two minutes closed the
connection permanently, and the process stayed running and looked healthy while
doing no work at all.

**Startup does not require the broker to exist yet.** A Controller or Runner
started before NATS is reachable waits up to ten seconds for it rather than
exiting immediately. Past that it does exit, and its supervisor restarts it
(Kubernetes `restartPolicy`, compose `restart`), so a slow broker still converges
without you ordering the three services, it just converges by restart. The bound
is what keeps a wrong `NATS_URL` a startup failure with a clear error rather than
a hang.

**Connection events are logged.** A lost connection logs at WARN, a recovery logs
at INFO with the reconnect count, and asynchronous errors log at ERROR, all tagged
with the component that owns the connection (`event-bus`, `lock-manager`,
`runner-dispatch`, `controller-logstream`). A Runner opens three connections and
they are named separately on the server, so `nats server report connections`
distinguishes them.

What this does **not** change: a job already running on a device when the link
drops is still subject to the device lease expiring, and a command already sent is
still never retried (above). Surviving the outage means the Runner comes back and
keeps pulling work, not that in-flight work is resumed where it stopped.

### One number sets how long an outage may last

You state one thing, and everything retention-shaped is derived from it:

```
PLEIADES_MAX_OUTAGE=30m          # env, on the controller AND the runner
mesh.maxOutageSeconds: 1800      # the Helm equivalent
```

Thirty minutes is the default. The accepted range is one minute to twelve hours.
Set the same value on every service: they each derive the stream's retention from
it, and a service whose value differs logs a warning naming the mismatch on every
start.

What it derives: how long the stream keeps a message, how long the broker
remembers a message identity for duplicate suppression, and how long a runner
remembers that it already executed a piece of work. Before this, those were three
unrelated numbers that happened to sit near each other, and two of them sat three
seconds apart by pure coincidence.

**Raising it is safe. Lowering it is not always.** Retention is derived from the
budget, so shortening the budget shortens retention, and shortening retention
deletes every message already older than the new value. The controller refuses to
do that. It tells you how many messages it would delete and how old the oldest is,
and you either raise the budget or set
`PLEIADES_MAX_OUTAGE_ALLOW_DISCARD=true` to accept the deletion. Note also that a
dispatch message carries the credentials its job runs with, so a longer retention
means those sit on the broker for longer: raising the budget is free in terms of
message loss and is not free in terms of exposure.

**The Helm chart now refuses to install a budget it would cancel.** The runner's
liveness probe restarts the pod when its heartbeat goes stale, and that restart
abandons in-flight work, so a staleness limit shorter than the budget means
Kubernetes kills the runner partway through the outage the budget claims to
survive. `runner.heartbeat.livenessStaleAfterSeconds` now defaults to the same
1800 seconds, and the chart fails the install if you make it shorter than
`mesh.maxOutageSeconds`. The cost of that is real and worth knowing: a runner
wedged for a reason reconnection cannot fix now takes up to the budget to be
noticed and restarted, where it used to take about a minute.

**What the budget does not fix.** Two things still cut an outage shorter than the
budget, and neither is a retention setting. A runner cannot begin new work during
an outage at all, because starting a job publishes a log event first. And a job
already running on a device is abandoned about a minute into an outage, when the
device lease heartbeat fails. So the honest reading is that the budget governs how
long the fleet can be out of contact and still pick up where it left off, not how
long work already in progress keeps running.

**The bus is not authenticated.** Reconnection resilience is not security: any
client that can reach the broker can publish and subscribe, and a dispatch message
carries the credentials its job runs with. Restrict network access to the broker
accordingly, and prefer an external, access-controlled NATS over the in-chart one
for anything real.

### Only the controller changes the message stream's shape

The controller, the runners and the demo binary all connect to the same JetStream
stream and the same key-value bucket for device locks. Only the **controller** ever
changes their configuration. Every other service binds to whatever is already
there.

This matters the moment you tune anything. Retention, the duplicate window and the
replica count used to be re-applied by every service on every start from values
compiled into that particular binary, so a setting you changed by hand was silently
reverted by the next runner restart, with nothing logged. A runner has no code path
that rewrites a live stream any more, so it cannot do that.

**You still do not need to order your services.** A service that finds the stream or
the bucket missing creates it, so a fresh install works whichever process starts
first, and a stream lost to a disk failure comes back on its own without waiting for
a controller restart. What changed is only the ability to *reshape* something that
already exists, not the ability to create what is absent.

**A mismatch is a warning, not a refusal.** During a rolling upgrade you will
briefly have services whose builds expect different settings. Any service that finds
a live shape different from the one its build declares logs a warning naming each
setting that differs, and carries on. That warning is worth reading: it means a
service is running against settings it does not expect, which during an upgrade is
normal and afterwards is not.

One reshape is refused rather than performed. Narrowing the set of subjects the
stream captures would orphan anything already published under a removed subject,
with no error from the server, so the controller refuses that change and says so
instead of making it.

### Reaching the broker through somebody else's network, and encrypting the wire

Two separate things, often confused, and the confusion matters enough to state
plainly before either.

**WebSocket is path traversal. It is not link resilience.** A `wss://` connection
gets you to a broker on port 443, through an HTTP proxy, a corporate egress filter,
or a CDN that terminates TLS. That is genuinely valuable for a runner on a network
you do not control. What it does **not** do is tolerate a link that drops. WebSocket
runs over TCP: a reset kills it exactly as it kills `nats://`, and reconnecting
costs a TLS handshake plus an HTTP upgrade, which is strictly more work than plain
NATS. Surviving an outage is the reconnection behaviour described above, and that
applies to every transport equally. Choose a WebSocket transport for reachability,
never for resilience.

QUIC is the thing that actually survives a path change, because its connection
identifiers outlive the address and port tuple. NATS does not speak it, on either
side: there is no QUIC dialer in the client and no QUIC listener in the server.
Riding QUIC means an external tunnel process, which is a deployment choice rather
than something this software does.

**Accepted URL schemes.** `NATS_URL` is now validated at startup, and anything
outside this list is refused rather than guessed at:

| Scheme | Transport | Encrypted |
|---|---|---|
| `nats://` | TCP | no |
| `tls://` | TCP | yes |
| `ws://` | WebSocket | no |
| `wss://` | WebSocket | yes |

Two mistakes it exists to catch. A bare `host:port` with no scheme used to be
accepted and treated as plaintext, so an omitted scheme silently meant
unencrypted; it is now refused with the two spellings you probably meant. And a
comma separated list mixing encrypted and plaintext entries is refused, because
which member a client picks is not something you control, so the plaintext one
decides what an attacker sees.

**Encrypting the wire.** Point `NATS_URL` at `tls://` (or `wss://`) and, if your
broker uses a private authority, set `NATS_CA_FILE` to the certificate that signed
it. Leaving it unset verifies against the system pool, which is right for a
publicly signed broker. Setting `NATS_CA_FILE` while the URL is plaintext is a
startup error rather than a warning: that pair reads as a protected connection and
is not one.

For the in-chart broker, `nats.tls.enabled` with `nats.tls.secretName` naming a
`kubernetes.io/tls` Secret serves TLS, and `nats.websocket.enabled` adds a
WebSocket listener (`nats.websocket.tls` serves it over TLS using the same
material). Enabling either writes the broker's first configuration file; everything
that was already a command line flag stays one.

**This encrypts. It does not authenticate.** The broker still accepts any client
that completes a handshake, and a dispatch message carries the credentials its job
runs with. TLS stops a network observer reading your traffic and lets a client
verify it is talking to your broker. It does nothing about which clients may
connect, or what subjects they may read. Restrict network access to the broker
regardless of whether TLS is on.

Two things worth knowing because they fail quietly. The chart's readiness probe for
the broker is a plain TCP connect, so it keeps passing even if the TLS
configuration is wrong, and the first sign of a bad certificate will be clients
failing rather than the pod. And the compose stack's broker healthcheck connects
anonymously and without TLS, so turning on broker TLS there needs that probe
changed too.

### Blast radius is always computed, never authored

Before `pleiades run` executes a runbook, it prints the blast radius: the number of
distinct inventory devices the runbook targets (deduplicated, across every
`pretasks`/`tasks`/`posttasks` task and every `block`/`rescue`/`always` descendant),
and the distinct set of `tier` property values those devices declare. This is
computed fresh from the runbook and the current inventory state at call time; a
runbook author never writes a `blast_radius:` field by hand, and a stale one can
never exist. There is no defined tier ranking today (no built-in notion that `prod`
outranks `lab`), so blast radius reports the raw set of tiers touched, not a single
worst-case severity score.

### Only an active device accepts work

Every inventory item carries one of eight lifecycle states
(`discovered`, `quarantined`, `onboarding`, `active`, `simulate-locked`,
`unreachable`, `decommissioning`, `archived`). Only `active` accepts real work.
`pleiades validate` rejects any task whose target resolves to a non-active device,
by name, before anything runs; the Crawl-tier executor and the Walk-tier dispatcher
both re-check the same rule at their own layer as well, so a device is never
executed against by a path that happened to skip validation.

### Locking

**The Crawl-tier CLI's locking is in-process only. Two `pleiades run` invocations do
not exclude each other.** `cmd/pleiades/run.go` wires `lock.NewInProcessManager`,
whose own doc comment says it "only guards against concurrent access within this
process." Every `pleiades run` is a separate process that starts with its own empty
lock table, so a lock one run holds is invisible to every other run. Two runs on the
same machine can configure the same device at the same second. This lock manager holds
a plain in-memory map and touches neither the network nor the filesystem, so two
machines cannot exclude each other either.
This is measured, not inferred: two `pleiades run` processes started one second apart
against the same device were both connecting to it at the same instant, each with its
own socket, and neither reported any contention.

A real distributed lock manager does exist (`lock.NewNatsLockManager`, backed by NATS
JetStream), and only the `controller` and `runner` binaries construct it. Since Phase
16 the distributed execution plane does reach real devices, so that tier both runs for
real and holds a per-device lease while it does. The gap is now narrower and lives
entirely on this side: the Crawl-tier CLI still has no distributed locking, so two
concurrent `pleiades run` invocations against one device do not coordinate.

**What to do instead:** serialize device access outside Pleiades. Run one
`pleiades run` at a time per device set. If more than one person or scheduler can start
a run, gate it with your own mutual exclusion: a CI concurrency group, a change window,
or a `flock` on a shared path. Do not rely on Pleiades to stop two operators from
touching one router.

`lock_acquisition:` controls *when* a task takes its locks, and its scope is a single
task, not the whole run. `per_device_as_reached` (the default) locks each device only
as execution reaches it, so one contended device does not stop that task's other
devices. `all_at_plan_time` locks every device that *that one task* targets up front,
all-or-nothing: if any one of them is contended, that task runs against none of them.
It does not lock the run's whole device set before the first task, so the name oversells
it. A later task set to `all_at_plan_time` still fails only once execution reaches it,
after earlier tasks have already changed earlier devices. Both strategies lock per
device, never a whole job, so two tasks targeting disjoint device sets never block each
other.

### Safety versus dry-run

`pleiades validate` is the closest thing to a dry-run today: it loads the inventory
and runbook, runs every registered validation rule (lifecycle
gating, collection reachability, conditional compilation, and more), and reports every finding without
executing anything. Capability matching is the one rule that barely runs: it covers
only the two legacy action names `ssh_exec` and `ios_backup`, never a catalog FQCN,
so `validate` passes a task pointed at a device that cannot run it. See
[Start here](01-start-here.md#implementation-status). There is no separate `--dry-run` or `--check` flag on `run`
itself, and no mechanism yet that reports *what would change* without actually
changing it (Ansible's `--check` mode has no Pleiades equivalent). `pleiades run`
always validates first and refuses to execute if validation reports any error.

## Security and credentials

### Operator accounts and sign-in

Create the first administrator on the host, before anybody can sign in:

```bash
controller bootstrap-admin --email you@example.com
```

It prompts for a password with echo disabled, creates the user, puts it in a team,
and grants that team system-scope admin. Re-running it is safe: the identity is left
alone and an existing password is refused rather than replaced, unless you pass
`--force`. For automation, `--password-stdin` reads the password as one line on
standard input:

```bash
echo "$PASSWORD" | controller bootstrap-admin --email you@example.com --password-stdin
```

A password is never accepted as a flag value on any path, because a flag is visible
in shell history and in the process argument list to every other user on the machine
for as long as it runs.

The other two commands are the recovery paths:

| Command | What it does |
|---|---|
| `controller reset-password --email <address>` | Replaces the password and revokes every session that account holds. Marks the password as needing to be changed at next sign-in. |
| `controller unlock --email <address>` | Clears a lockout without touching the password. |

**There is no password reset by email, deliberately.** Adding one would make the
first sign-in on a fresh machine depend on outbound mail working, which is the wrong
thing to put between an operator and their own control plane. Both commands run over
the shell access you already have.

**How a password is stored.** Argon2id, 19 MiB of memory, two passes, one lane, with
a 16-byte random salt per password, encoded as a PHC string so the parameters travel
with the hash and a later cost increase needs no migration. A password verified at
older parameters is transparently re-hashed at the current ones on the next
successful sign-in. It is a one-way hash and not encryption: nothing in the system
can recover a password, including you.

**Lockout.** Ten consecutive failures lock an account for fifteen minutes. The
counter is a row in the shared database rather than per-process state, so it is not
reset by a restart and cannot be multiplied by running more replicas. The expiry does
not reset the counter, so the next burst re-locks immediately. Nothing exempts an
account from lockout, including the first administrator, which is why `unlock` exists
and runs off the network.

**What a sign-in failure tells an attacker: nothing.** A wrong password, an unknown
address, an account with no local password and a locked account all produce the same
response, and all cost the same amount of time. The real reason is written to the
controller's log, where an operator can read it and a caller cannot.

**Signing in with a token still works** and is the only route for a deployment that
federates against an external issuer and holds no local passwords at all.

### Threat model, briefly

Pleiades stores two classes of durable secret.

The first is device credentials: a username plus a password or an SSH private key,
keyed by device name, encrypted at rest in a local file. This is the Crawl tier's
store and it is still what the control plane falls back to when a template binds no
machine credential.

The second is control-plane credentials: a credential of a declared type, holding
whatever inputs that type declares, encrypted at rest in the database. These are the
ones bound to templates, and they are what an AWX migration brings with it.

Two external secrets manager integrations are implemented.

The first is a file: a credential input names one, resolved at the moment a job
dispatches rather than when it was created. That covers a Kubernetes projected
volume, a Vault Agent sidecar and the External Secrets Operator, which is how
secrets arrive in a large fraction of deployments. It needs no client and no
network call on the dispatch path.

The second is **HashiCorp Vault**, reading a key/value secret directly over its
HTTP API, on either a v1 or a v2 mount. The certificate chain is always verified
and there is no option anywhere to skip it; a Vault with a private authority is
reached by pasting that authority into the source credential. Configure it as an
input source, described below, rather than as a reference string: a Vault needs an
address and a token, and a reference string has nowhere to put either.

Seven further sources are named after their AWX equivalents and return an explicit
"declared but not implemented" error: HashiCorp Vault signed SSH, AWS Secrets
Manager, Azure Key Vault, CyberArk Conjur, Centrify, and the two Thycotic products.
Read that as a real constraint when deciding whether Pleiades fits an environment
that mandates one of them, not as a gap to work around.

### Input sources: an input supplied by another credential

There are two ways a credential says an input lives somewhere else, and they exist
side by side.

The first is a reference string, described above: the input names a source and a
path, and the source itself is configured once for the whole controller. That is
right for a file, because a projected volume is a property of the deployment rather
than of any one credential.

The second is an **input source**: the input is bound to another credential, an
external-kind one holding that secret manager's own address and token, plus the
metadata saying where in it to look. Bind them through
`PUT /api/v1/credentials/{id}/input-sources`, or in the same request that creates
the credential. This is the model AWX uses, and it exists because a vault address
and a vault token are themselves credentials: they need rotating, an audit trail
and RBAC, and a string in a column is none of those.

Rotating the source is the point. Every target that reads through it picks up the
new value on its next run, with no edit to any of them, because resolution happens
when a job dispatches rather than when the binding was written.

Four things are refused when you write a binding, rather than when a job later
trips over them: an input the credential's type does not declare, a source in
another organization, a source that is not an external-kind credential, and a set
that would make resolution return to the credential it started from.

A source credential's own inputs may themselves be bound to a further source, and
that chain is bounded at **four hops**. Past that the resolution is refused by name
rather than followed, because every link is an ordinary row that anyone who can
write credentials can add, and an unbounded walk would be a denial of service
against the controller reachable from ordinary data. A chain that returns to where
it started is reported as a cycle rather than as depth, since the two need
different fixes.

The source credential's type is what decides how a binding is resolved, so the set
of sources you can bind to is the set of external-kind types this release ships.
Today that is HashiCorp Vault. A binding whose source type nothing can build fails
with an explicit error naming that source and listing the ones this controller has,
in the same way a declared-but-unimplemented source does, so the set shrinks
honestly as real sources land rather than a binding quietly resolving to nothing.

For a Vault source, the binding's metadata carries AWX's own field names, so an AWX
`CredentialInputSource` row maps across without translation: `secret_backend` (the
mount, defaulting to `secret`), `secret_path`, `secret_key`, and optionally
`secret_version`. A path element that would address something other than the secret
it names, such as a `..`, is refused rather than cleaned.

### Rotating the master key

`MASTER_ENCRYPTION_KEY` can be replaced without downtime, and without losing
anything, as long as the old key stays available while the change is in flight.

Set the new key as the current one and the old key as the previous one. Every
read tries the current key and falls back to the previous, so nothing breaks the
moment the process restarts. Then run a rotation pass, which re-encrypts every
row under the new key. There is one pass per entity that stores a secret:
credentials, devices and saved launch configurations.

**Do not remove the old key until every pass reports that it has converted every
row.** A row that has not been re-encrypted yet can only be opened with the old
key, so taking it away early strands that row permanently. Each pass returns the
number of rows it converted, which is how you tell it has finished.

The passes also do a second job. Devices and saved launch configurations used to
be encrypted without binding the ciphertext to the row it belongs to, which meant
a value copied from one row to another would still decrypt. That is closed now,
and a rotation pass is what converts an older row to the new form.

A device converts itself as a side effect of ordinary use, because anything that
writes its properties rewrites them bound. **A saved launch configuration does
not**: nothing in this platform ever rewrites its answers, so a rotation pass is
the only thing that will ever migrate one. Survey answers are the one path by
which a password reaches a stored row, so that pass is worth running even if you
are not changing keys.

### Credential storage

`pleiades add-credential <device> --username <user>` prompts for a password or a
private-key path (never accepting a secret as a bare CLI flag value, since that
would be visible in shell history and to any other user on the same machine for as
long as the process runs) and writes it to `<project>/.pleiades/credentials.yaml`,
encrypted field-by-field with AES-256-GCM.

The 32-byte master key is resolved in this order, and the choice is never silent:

1. The `PLEIADES_MASTER_KEY` environment variable, if set (base64-encoded). A
   malformed value is a hard error; a deliberately set env var is never silently
   ignored in favor of the file.
2. `<project>/.pleiades/master.key`, if it already exists. A corrupt or
   wrong-length file is a hard error, never silently regenerated (regenerating
   would produce a key that cannot decrypt anything already encrypted under the
   old one).
3. Otherwise, a fresh key is generated with `crypto/rand` and written to
   `<project>/.pleiades/master.key` with mode `0600`, inside a `0700` directory.

Neither a runbook file nor `inventory.yaml` ever contains a password: the only
place a device's own connection detail (host, port, and anything credential-shaped
an inventory source adds) is stored is `Device.properties`, itself encrypted at
rest with the same AES-256-GCM envelope mechanism in the control plane's database.

### Credential types and the injector engine

A credential type is data: an input schema saying what a credential of that type
holds, and an injector document saying where those inputs go at run time. An
administrator defines one over the API and an AWX export decodes into it directly.

Six types ship with the platform and are installed on every controller start, under
the same namespaces AWX uses: `ssh` (Machine), `vault`, `net` (Network), `aws`,
`controller` (Red Hat Ansible Automation Platform) and `hcp_terraform`. They cannot
be edited or deleted, which is what lets an import reuse them rather than recreating
them as custom copies that silently stop tracking the shipped ones at the next
upgrade. Installing them is a reconcile keyed on namespace rather than a migration,
so a later release that adds a type or corrects one reaches a deployment that already
started.

Sixteen further AWX types are recognised and not implemented. `pleiades import
awx-credential-types <export.json>` reports which is which for your own export, with
the specific reason for each, before you commit to a migration window.

Four things about this are security-relevant in production.

**An injector document is executable.** It decides which environment variables the
customer's playbook runs with, and the playbook runs inside the same container the
credential was injected into. A type that could set `LD_PRELOAD`, `PYTHONPATH`,
`ANSIBLE_CONFIG`, `PATH` or a `BASH_FUNC_` variable would be arbitrary code execution
inside the run it was meant to authenticate, so those names and their relatives are
refused when the type is saved and again when a dispatch reaches it. The container
being single use does not help: the code would run before the container is destroyed.
For the same reason the web UI lists and tests credential types but does not author
them; that stays on the API, where a caller had to construct the request
deliberately.

**Binding is a higher privilege than editing.** Changing what a template runs is
`template:write`. Changing what it runs *as* is `credential:write`, granted
separately, because whoever binds a credential decides which identity the automation
acts under. The UI follows the same split: the control that binds credentials to a
template is gated by the credential scope, not by the template form's own.

**Secrets are never readable back.** Every read path returns a projection with no
field a plaintext value could occupy: a secret input reads back as `$encrypted$` and
there is no endpoint, parameter or header that returns the real thing. It is
decrypted only at dispatch, inside the process that injects it. Submitting the marker
back on an update leaves the stored value alone, so editing an unrelated field does
not destroy a secret.

**A prompted input is never stored.** A type may declare an input asked at launch
rather than saved. The answer travels with that one job and is written nowhere, which
also means a job launched with one cannot be relaunched: the platform says so rather
than silently repeating the run without it.

The residual to plan around is the message bus. Injected material rides the same
JetStream stream every dispatch uses, whose retention is seven days, so a rendered
secret can sit there for that long. This phase makes that worse in volume and
identical in kind: where a dispatch previously carried one flattened SSH credential,
a template binding a cloud credential and two file-generating ones puts several more
on the same message, whole PEM bodies included. The real fix is reference passing,
where the payload carries a handle and the runner fetches it over a
mutually-authenticated short-lived connection.

The runner identity half of that now exists, and the rest does not, so be precise
about what has changed. Pleiades can mint a short-lived, subject-scoped credential
for a runner and a broker can be configured to require one, which is the
authentication this was waiting on. What has NOT changed is the payload: a dispatch
still carries the resolved credential itself, and authentication changes who may read
the stream rather than what is written to it. Reference passing is the separate piece
of work that removes the secret from the message.

Until it lands, treat the stream as holding secrets and size its retention
accordingly.

### Host key verification, and where a container gets its known_hosts

Every SSH connection Pleiades makes verifies the device's host key against an
OpenSSH-format `known_hosts` file, and fails closed when it cannot. There is no
trust on first use: a device with no entry is refused, and so is a device whose key
stopped matching the entry it has. That is the behavior of `ssh` with
`StrictHostKeyChecking=yes`, and it is deliberate, because the alternative is that a
machine in the middle can answer for a device and collect the credential you were
about to send it.

The file is resolved from the first of three sources that names one:

1. whatever the caller passed for that one connection;
2. the `PLEIADES_KNOWN_HOSTS` environment variable, which is how a deployment
   configures the whole process;
3. `$HOME/.ssh/known_hosts`, which is where you already keep yours.

Most specific wins, the same order `ssh` itself uses. On the Crawl tier the third
entry means the CLI simply reuses the file you have, and there is nothing to
configure.

**In a container there is no third entry, so this is a required deployment step.**
The runner image is distroless and has no home directory, so it sets
`PLEIADES_KNOWN_HOSTS=/app/ssh/known_hosts` and ships that directory empty. Mount a
real file over it or every SSH task refuses, naming that path. The chart takes a
ConfigMap or a Secret:

```bash
ssh-keyscan -H device-a.example.com device-b.example.com > known_hosts
kubectl create configmap pleiades-known-hosts --from-file=known_hosts
helm upgrade pleiades ./helm/the-pleiades \
  --set runner.knownHosts.configMapName=pleiades-known-hosts
```

The key inside the object has to be named `known_hosts`; the chart mounts that name
specifically, so a wrongly-keyed object leaves the pod unable to start rather than
running with no host keys. Compose has the same mount commented into
`docker-compose.yml` with both shapes.

Nothing is baked into the image on purpose. Host keys in an image mean rebuilding it
to add a device, and an empty file would be worse than none: it parses and matches
nothing, so every connection would fail with a per-device "no entry" message instead
of one clear message about a mount nobody made.

**The escape hatch, and what it costs.** A task may set
`insecure_skip_host_key_verify: true`, which skips verification for that task only.
It is a per-task parameter rather than a setting precisely so it appears in the
runbook next to the command it applies to, where review can see it; there is
deliberately no environment variable or chart value that turns verification off
fleet-wide. Use it for a throwaway lab and treat it as a finding anywhere else. Note
that until recently this was the only thing that worked inside the shipped runner
image, so a runbook inherited from that period may be carrying it for a reason that
no longer exists.

### Bastions and hop chains

A device that is only reachable through a jump host does not need a second
transport or a special task parameter. Configure a `route` on whichever level of
the hierarchy the bastion actually applies to: a single device, a group of
devices, or a whole inventory. Most specific wins, the same rule every other
layered setting in Pleiades follows (a group-level bastion with a per-device
override behaves exactly like a group-level anything else with a per-device
override). The value is an ordered list of device names, nearest hop first:

```yaml
properties:
  route:
    - jump-host-1
    - jump-host-2
```

Each name in that list must be a real, separately inventoried device with its
own SSH capability and its own stored credential. That is not a convenience
default, it is deliberate: a bastion's account and the production device's
account are almost never the same, so a hop's credential is looked up
independently under its own device name, never inherited from the target and
never falling back to it. A hop with no stored credential of its own fails the
task, naming that hop, rather than silently trying the target's password
against it. If you see a task fail with an error naming a device you did not
expect, that device is a hop in the resolved route, and the fix is to store a
credential for it like any other device.

**Every hop gets its own host key check.** The tunneled connection to hop two
is a second, fully independent SSH handshake carried inside the encrypted
channel hop one already established, and it is verified against `known_hosts`
exactly like a direct connection would be: an unrecognized or mismatched key
at any hop refuses the connection and names that hop specifically. This is
what actually defends against a compromised bastion. A jump host that can see
your traffic sees ciphertext only, past its own hop, and a bastion that tried
to redirect the tunneled connection somewhere else would be caught by that
next hop's own key check, not by anything the bastion itself could suppress.

A route may name at most sixteen hops. Real bastion topologies are one or two
layers deep; the bound exists so a misconfigured or attacker-influenced
`route` value fails immediately, before any inventory or credential lookup for
any of its entries, rather than resolving into a chain long enough to make a
single task pay for dozens of failed dials one at a time.

**A `route` reaches a non-SSH endpoint too.** `serial_exec`, `serialtcp_exec`,
and `telnet_exec` (below) tunnel through the identical hop chain: every hop is
still a real, independent SSH connection with its own host key check and its
own credential, and only the final leg speaks the target protocol instead of
SSH. A console server on a management network behind a jump host needs
nothing beyond the same `route` property this section already describes.

### Serial, console servers, Telnet and TFTP

These transports reach a target that is not "a device with SSH on it": a
directly attached serial line, a console or terminal server proxying one over
TCP, genuinely old gear with nothing but Telnet, or a file moved by TFTP
instead of a command run over a session. Each one trades away something SSH
gives you for free — authentication, encryption, or both — and each one says
so loudly rather than quietly, through a task parameter that has to be set on
purpose next to the command it applies to.

**First, the device type that carries these capabilities: `console_device`.**
None of the tasks below can run against a `linux_server` or a `cisco_router`,
and that is deliberate rather than an omission. A Linux server reached over
SSH is not cabled to a console; claiming otherwise would let a runbook pass
validation and then dial nothing. Console-reachable gear is its own inventory
type:

```yaml
hosts:
  # A switch on a terminal server port, staged before it has a management
  # address. Reachable by serialtcp_exec, and by nothing else.
  - name: sw-staging-01
    type: console_device
    properties:
      raw_passthrough_host: ts1.mgmt.example.net
      raw_passthrough_port: 2003

  # A channel bank cabled to this host's own USB-serial adapter.
  - name: pbx-annex
    type: console_device
    properties:
      serial_device: /dev/ttyUSB0
      serial_baud: 9600
      serial_data_bits: 8
      serial_parity: none
      serial_stop_bits: "1"

  # Genuinely old gear with nothing but Telnet.
  - name: rtr-1994
    type: console_device
    properties:
      telnet_host: 10.20.30.40
```

The properties are what decide which capability the device declares, one
reach path at a time:

| Property | Declares | Notes |
|---|---|---|
| `serial_device` | `SerialCapable` | The operating system's own name for the port. Opaque: `COM3` is as valid as `/dev/ttyUSB0`. |
| `serial_baud`, `serial_data_bits`, `serial_parity`, `serial_stop_bits` | *(line settings only)* | Optional. Default to 9600 8-N-1, the console setting mainstream gear ships with. |
| `raw_passthrough_host` + `raw_passthrough_port` | `RawPassthroughCapable` | Both required. There is no default port: per-line numbering is vendor specific (Digi from 2001, Opengear and Lantronix from 3001), so guessing would dial somebody else's line on the same unit. |
| `rfc2217_host` + `rfc2217_port` | `RFC2217Capable` | Both required, same reason. `rfc2217_baud` and its siblings configure this line independently of `serial_*`. |
| `telnet_host` | `TelnetCapable` | `telnet_port` defaults to 23, which is a real convention rather than a guess. |

A device configured for one path does not claim the others, so
`pleiades validate` rejects a `serial_exec` task aimed at a Telnet-only
device before anything runs. A line setting that does not parse is refused
when the inventory is read, not defaulted past: a wrong parity or baud rate
does not fail a serial line, it silently corrupts every byte crossing it.

**Local serial (`serial_exec`).** A device declaring `SerialCapable`
advertises a serial port identifier (`/dev/ttyUSB0` on Linux,
`/dev/tty.usbserial-*` on macOS, `COM3` on Windows) and a line configuration
— baud rate, data bits, parity, stop bits. That identifier is opaque: it is
never parsed, joined, or validated as a filesystem path, because `COM3` is
not one and even the POSIX names are an identifier the operating system
assigns, not a path Pleiades constructs. This is the one transport in this
section requiring no opt-in, because a directly attached serial line has the
same physical-access trust model a local console does.

**Console servers: two genuinely different claims about the same wire.** A
console or terminal server (Digi, Opengear, Lantronix, Perle,
Avocent/Cyclades) proxies a serial line as a TCP port, and it does so in one
of two ways that Pleiades treats as separate capabilities rather than a
flag, because they are different claims about what the target can do:

| Capability | Task fqcn | What it offers | Opt-in |
|---|---|---|---|
| `RawPassthroughCapable` | `serialtcp_exec` | A bare byte pipe: zero framing, zero authentication, zero encryption at the protocol level. No line control at all — no baud rate, no DTR/RTS, no break. | `insecure_raw_passthrough: true` |
| `RFC2217Capable` | *(none yet — see below)* | A real control channel (RFC 2217, the Telnet Com Port Control Option) negotiating baud rate, data bits, parity, stop bits, and asserting DTR/RTS/break, layered onto a Telnet session. | *(not yet reachable from a runbook)* |

`serialtcp_exec` is refused outright without `insecure_raw_passthrough: true`
set as a task parameter, checked before any network I/O — the same "explicit,
loud opt-in, never a fallback silently taken" shape
`insecure_skip_host_key_verify` already established above. There is nothing
this transport can do to make the connection itself safer; the opt-in exists
so that decision is visible in the runbook, not buried in a device property
nobody reviews.

**RFC 2217 is a real, tested client with no runbook task yet.** The
negotiation, line-setting, and modem-control logic lives in `pkg/rfc2217` and
is proven against a real `ser2net` access server, including a genuine
observed baud change and a genuine observed break condition on the far side
— the one thing that actually distinguishes RFC 2217 from raw passthrough.
What does not exist yet is an `engine.TransportBinding` for it: "assert DTR"
and "send a break" are not command strings, so this capability is reachable
today only by a future Collection method built directly against the library,
not by a task fqcn in a runbook. If you are looking for that fqcn, it is not
missing by oversight — it is not built yet.

**Telnet (`telnet_exec`).** Genuinely ancient gear with no SSH at all still
exists, and Ansible ships `ansible.netcommon.telnet` for exactly the reason
stated in its own documentation: to enable SSH on a device that only has
Telnet enabled by default. A device declaring `TelnetCapable` is reachable
the same way, behind its own `insecure_telnet: true` opt-in — Telnet sends
everything, credentials included, in cleartext, with no encryption at any
layer. Use it to bootstrap SSH onto a device and stop using it once that is
done.

**Every one of these three reports `exit_status_unknown: true`.** A serial
console, a raw byte pipe, and a bare Telnet session have no concept of a
process exit code — only a real shell session does, and none of these is
one. `stdout`/`stderr` are captured and recorded either way, but nothing in
Pleiades infers success from a non-zero code that was never there in the
first place; a `when` or `when_cel` assertion against the captured text is
how a runbook judges whether a serial command actually succeeded.

**TFTP.** `pkg/tftpxfer` moves a file to or from a TFTP server (RFC 1350,
plus RFC 2347/2348 negotiated options). Say the same thing about it that this
section says about raw passthrough: **no authentication and no encryption at
the protocol level, ever** — any host that can reach the server's UDP port
can read or write any file the server's own filesystem mapping allows, and
nothing in this package can fix that. A remote filename containing `..`, an
absolute path, or a Windows drive letter is refused before a request is ever
sent, which defends against an accidentally or maliciously constructed
traversal on the *remote* filename; it says nothing about the *local* side,
since this package only ever writes to a caller-supplied `io.Writer` and
never constructs a local path itself. Like RFC 2217, this is a library
(`FileTransferCapable`) with no runbook task wired to it yet.

**Docker exec is different from all of the above, deliberately.**
`container.docker.exec` (see the [module reference](reference/modules/container/docker/exec.md))
is a real fqcn with a real Manifest, reached over the Docker daemon's own
control socket. Unlike the byte-stream transports above, it reports a
genuine process exit code every time, because Docker's own exec-inspect
endpoint provides one. It is also, by construction rather than convention,
incapable of anything past running one command inside one already-running
container: every request it can send is checked against a fixed, three-entry
allowlist (create an exec instance, start it, inspect its result) before a
byte reaches the socket, and there is no method anywhere in the package that
could be widened into a general passthrough. This matters because the socket
itself is root-equivalent — whoever can reach it can, in general, ask the
daemon to create a privileged container with the host's root filesystem
bind-mounted in — and neither a read-only socket mount nor running the
calling process as non-root actually restricts that; the daemon's own
privilege is what matters, not the caller's. The allowlist is the only real
defense, and it is enforced in one function every request funnels through.

**Digi RealPort is not, and will not be, a protocol Pleiades speaks.**
RealPort is not a wire protocol in the sense RFC 2217 is — it is an
operating-system driver product. Install Digi's own RealPort driver on the
host, and the port it creates behaves like an ordinary local serial device:
reach it with `serial_exec` exactly as you would a directly attached
USB-serial adapter. Pleiades deliberately implements no RealPort client of
its own; doing so would mean re-implementing, in Go, a job the vendor's
driver already does correctly for the operating system.

### PKI and TLS

Two different things, at different stages, and it is worth not confusing them.

**Serving TLS is real and unavoidable.** The Controller terminates TLS from
`TLS_CERT_FILE` and `TLS_KEY_FILE` (TLS 1.2 floor), or serves plain HTTP only when
`PLEIADES_TLS_TERMINATED_UPSTREAM=1` states that an ingress in front of it already
terminated TLS. With neither configured it generates a self-signed certificate,
stores it, and serves that. Setting one of the two files without the other, or both
arrangements at once, is a startup error. This is deliberate rather than strict for
its own sake, because the browser session cookie carries `Secure` and the `__Host-`
prefix unconditionally, and a browser refuses such a cookie on a plain-HTTP origin
without explaining why. Nothing reads `X-Forwarded-Proto` or any other forwarded
header; which arrangement is in use is a setting an operator states, not something
the process infers per request. See
[The web UI](12-web-ui.md#serving-over-tls) for the full table, and note that
certificate rotation still means restarting the process.

**In production, set `TLS_CERT_FILE` and `TLS_KEY_FILE`, or terminate upstream.** The
self-signed certificate is a convenience for a local stack that has to work as one
command. It encrypts the connection and it does not authenticate the server: nothing
your clients already trust vouches for it, so a browser warns and an operator who
clicks through cannot distinguish the Controller from anything else answering on that
address. Treating a warning as routine is itself the risk, because it is the same
warning an interception would produce. A configured certificate always wins over the
generated one, so the production fix is to set the two variables and restart. If you
do run on the generated certificate anyway, note where it lives
(`PLEIADES_TLS_AUTOCERT_DIR`, default `tls` relative to the working directory, which
is `/data` in the container image), that the key is written `0600` inside a `0700`
directory, and that renewal happens only at startup: a process left running past the
certificate's one-year validity serves an expired certificate until it is restarted.

**In Kubernetes, the chart states which arrangement you chose.** The Controller
cannot infer it, for the reason above: a header a client can set is a claim a client
can forge. So `controller.tls.mode` is a value, and each setting produces one of the
three arrangements:

| `controller.tls.mode` | What the pod does | When to use it |
|---|---|---|
| `self-provisioned` (default) | Generates a self-signed certificate into its data volume, reuses it on every later start, serves HTTPS. | A first install. Browsers warn, and clicking through is exactly as weak as it sounds. |
| `secret` | Serves `tls.crt` and `tls.key` from a `kubernetes.io/tls` Secret, which is the shape cert-manager writes. | Production, when TLS terminates in the pod. |
| `upstream` | Serves plain HTTP, because an ingress or a mesh in front of it already terminated TLS. | Production, when TLS terminates at the edge. Only true if it really does: a browser silently drops the session cookie on a plain-HTTP origin, and sign-in then fails with a message about credentials. |

Two details are easy to get wrong and hard to diagnose. The chart sets the liveness
and readiness probe **scheme** from the same value, because a kubelet probing
`http://` at an HTTPS listener never gets a valid answer and the pod would fail its
probes forever. And when the Controller serves its own certificate, the ingress
controller has to speak HTTPS to the backend
(`nginx.ingress.kubernetes.io/backend-protocol: "HTTPS"` on ingress-nginx); getting
that wrong shows up as a 502 with a protocol error in the ingress controller's log
and nothing at all in the Controller's.

**Self-provisioning does not survive a second replica, and the chart refuses to
render one.** Two Controllers generating their own certificates either share one
volume, which is a read-modify-write race over the same key pair, or hold separate
certificates, so a client is handed a different untrusted certificate depending on
which pod answered. Scaling out means `mode: secret` or `mode: upstream`, where every
replica presents the same certificate or none at all. Autoscaling is refused for the
same reason, since its whole purpose is a second replica.

**An mTLS mesh between the Controller and Runners is not.** It is designed
(`internal/pki` declares the interface: sign a CSR, issue a 72-hour certificate) but
not implemented; there is no certificate issuance, rotation, or mTLS enforcement
today. Treat that half as `design`, not `beta`. Nothing in the Helm chart changes
that: the chart configures how a *client* reaches the Controller, and the
Controller-to-Runner path still carries no certificates at all. Traffic between the
Controller, the Runners, the database and the broker is unencrypted inside the
cluster unless you put a service mesh there yourself.

## Data handling disclosure

Two different things get called "secret" in this codebase, and they are protected
differently. Knowing which one applies to a given value matters for a real
deployment.

### What is encrypted at rest

`Device.properties` (the JSONB bag holding connection details and any
credential-shaped value an inventory source adds) is encrypted transparently by an
ent hook the moment it is written, and decrypted transparently on read. `Fact.payload`
(what a device reports about itself, telemetry) is a deliberate exception: it is
**not** encrypted. This is a data-classification decision made in code, not an
oversight: connection and credential material is treated as secret by default,
while gathered facts are treated as operational telemetry. If a Collection method
ever emits a genuinely sensitive value as a fact, `register_mask`/`secret_mask`
(below) is the mechanism to keep it out of printed and streamed output, but it does
not encrypt the stored fact itself.

`journal_entries` is the second deliberate exception, and it is a different kind of
exception from the first. `Fact.payload` is unencrypted because gathered facts are
classified as operational telemetry, which is a judgement about the value. A journal
entry is unencrypted because it holds no value at all: what it stores is identifiers,
registry-resolved method names, declared key names, status values and counts, and
two architecture tests refuse any field able to carry anything else. There is
nothing there to encrypt rather than a decision not to.

One related disclosure while you are reading this section: `Revision` rows record
inventory changes over time, and what they hold about a device follows the same
rule `Device.properties` does.

### `register_mask` and `secret_mask` are a security contract, not a convenience

`register_mask:` on a task masks one or more fields of that task's own registered
result the instant it registers, before anything downstream can see it unmasked;
dotted paths are supported (`register_mask: response.body.token`). `secret_mask:`
retroactively marks an earlier, already-registered task's top-level fields as
secret; it does not support dotted paths, an intentional asymmetry, not an
oversight. Both require the masked value to be a string at least 8 bytes long:
substring-masking a shorter value risks scrubbing an unrelated, coincidentally
matching substring out of unrelated output, which would be worse than not masking
at all.

**Where masking applies, precisely:**

- Every printed line of the run's own final output (`cmd/pleiades/run.go`) is
  scrubbed using the complete set of values discovered as secret by the time the
  run finished.
- Every live progress event published during the run (the same event stream a
  Walk-tier job log or SSE viewer would read) is scrubbed **best-effort, in
  flight**, using only the secrets known at the moment that specific event is
  published. An event published before a later task marks something secret cannot
  be retroactively scrubbed. This is a real, load-bearing limitation, not a
  hypothetical edge case: a runbook that discovers a secret late should not assume
  every earlier live event was masked.
- `when_cel` conditions always see the real, unmasked value. Masking a value from
  the conditional engine would silently break branching logic that depends on it.

**Where masking applies on the Walk tier:** the distributed execution path (a job
dispatched to a `runner` over NATS) does run real tasks against real devices, and
has since Phase 16, so the paragraph that used to stand here saying otherwise was
out of date. What that path masks today is its own streamed output: the Runner
builds the complete secret set from the run's own `register_mask`/`secret_mask`
discoveries plus every value the Controller attached to the dispatch plus every
value a bound credential injected, and masks through it. Two limits are worth
stating plainly. That set is complete only once the run has finished, so an event
published early in a run is masked against whatever was known at the time. And the
run journal is outside this question entirely rather than covered by it: it stores
no device output to mask.

### Telemetry

OpenTelemetry tracing exists in the control plane (a "Run" click ties to every step
across every device via one trace). What exactly a span attribute carries per
Collection method has not been separately audited against the masking rules above;
treat this as an open item for a future security review, not a settled guarantee.

## Configuration reference

Not yet generated. Each of the four binaries (`pleiades`, `controller`, `runner`,
and the web UI's own server) reads its configuration differently today, and no
single source of truth exists yet to generate a reference from. This page will
follow the same "read from the real source, never hand-maintained" discipline as
the module catalog once one does.

## Backup and restore, data retention and purge

Not documented yet. The control plane's data layer (ent-backed) has real schema
migrations (`internal/ent/migrate`), but no documented backup/restore procedure or
retention/purge policy exists today.

## Observability and troubleshooting

Prometheus metrics are exposed at `/metrics`. Structured logging exists throughout
the control plane and the runner agent. A generated metrics reference, an alerting
guide, and a troubleshooting/diagnostics-collection guide do not exist yet.

## HA and leader failover

Distributed leader election is real and tested (`internal/election`), used for
control-plane components that must run as a single active instance. A full
operational guide to configuring and verifying HA in a real multi-node deployment
does not exist yet.

## Multi-tenancy

None today. RBAC has three roles (`viewer`, `operator`, `admin`) and a scope model,
but there is no tenant isolation concept above that.

## Sizing

**Still not written, and the reason has changed.** It used to be that there was no
deployable artifact to size at all, since the reference chart was unmodified
`helm create` output that deployed nginx. That is fixed: `helm/the-pleiades` is a
real chart. What is still missing is a measurement. No Pleiades deployment has been
run under sustained load, so any table of "N devices needs M runners" here would be
arithmetic invented to fill a section.

What the chart does ship is a starting point, and it is worth reading as exactly
that:

| Workload | CPU request | Memory request | Memory limit | Default replicas |
|---|---|---|---|---|
| controller | 100m | 256Mi | 1Gi | 1 |
| runner | 100m | 256Mi | 1Gi | 2 |
| PostgreSQL (in-chart) | 250m | 256Mi | 1Gi | 1 |
| NATS (in-chart) | 100m | 128Mi | 512Mi | 1 |

Those numbers are sized to start on a laptop-scale cluster, not measured under load.
Two things about the shape of them are deliberate and do carry over:

- **Requests are set and CPU limits are not.** A CPU limit is enforced by CFS quota,
  so a process that reaches it is throttled for the remainder of the period, which
  arrives as latency on a request that did nothing wrong. Memory has no equivalent
  graceful degradation, so memory carries a limit and CPU does not. If your cluster
  applies a `LimitRange` that adds CPU limits anyway, that is worth knowing before
  you conclude Pleiades is slow.
- **The runner scales with the number of concurrent device conversations, not with
  request rate.** Runners pull from one durable NATS consumer group, so adding
  replicas adds parallelism and never duplicates work. There is deliberately no
  autoscaler on them: their load arrives as queue depth, and a CPU-driven autoscaler
  would scale *down* a fleet that is blocked waiting on slow devices, which is
  exactly backwards. Scale them by hand, or on a queue-depth metric if you already
  run an external metrics adapter.

One measured number is worth planning around in the meantime: a task that calls a
Collection method pays roughly 10 ms of process-isolation overhead on top of whatever
the device work itself costs, because each such task runs in its own child process
(see [Start here](01-start-here.md)). Tasks using `ssh_exec` do not pay it. Beyond
that, writing a sizing guide would still be a guess dressed up as guidance.
