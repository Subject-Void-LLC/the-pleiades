{{/*
Every configuration this chart refuses to render, in one place, with the reason
attached to the refusal.

WHY THESE ARE `fail` AND NOT COMMENTS. Helm has no warning channel: a rendered
manifest either installs or it does not, and a bad combination here does not
produce an error at install time, it produces a pod that comes up and behaves
wrongly. A controller that races itself over one certificate file, a runner
told to run Ansible in a pod with no Docker daemon, and a release whose
encryption key was never set all install cleanly and fail later, somewhere
noisier and further from the cause. Refusing to render puts the error next to
the value that caused it.

WHY IT IS AN INCLUDE RATHER THAN ITS OWN MANIFEST FILE. Helm renders template
files in sorted order and stops at the first error, so a validation living in
validations.yaml would be reached AFTER controller-deployment.yaml, and a
missing value would surface as whatever error that file hit first. Every
template in this chart includes this partial on its first line, so whichever
file renders first runs these checks first, and the operator sees the real
reason.
*/}}
{{- define "the-pleiades.refuse" -}}
{{/*
One place every refusal is worded from, so each message says where the check
really lives.

Helm reports a `fail` at the position it was invoked, which for this chart is
the first template that happened to include the validations, usually
serviceaccount.yaml. Reading that literally sends an operator to edit a file
with nothing wrong in it, so the prefix below says so before the reason
starts. The leading blank lines separate the message from Helm's own
"execution error at (...)" preamble, which otherwise runs into the first
sentence.
*/}}
{{- fail (printf "\n\nThe the-pleiades chart refuses this configuration. The check is in helm/the-pleiades/templates/_validations.tpl; the template named above is only the first file that included it.\n\n%s\n" .) -}}
{{- end }}

{{- define "the-pleiades.validate" -}}
{{- include "the-pleiades.validate.ansible" . -}}
{{- include "the-pleiades.validate.tls" . -}}
{{- include "the-pleiades.validate.secrets" . -}}
{{- include "the-pleiades.validate.backends" . -}}
{{- include "the-pleiades.validate.images" . -}}
{{- include "the-pleiades.validate.workloads" . -}}
{{- include "the-pleiades.validate.retaineddata" . -}}
{{- end }}

{{/*
The legacy Ansible refusal, checked before anything else because it is the one
an operator is most likely to reach for and the one with the longest reason.
*/}}
{{- define "the-pleiades.validate.ansible" -}}
{{- if or .Values.runner.playbookDir .Values.runner.ansibleImage -}}
{{- include "the-pleiades.refuse" "runner.playbookDir and runner.ansibleImage are not supported by this chart, and the reason is not a missing feature.\n\nThe legacy Ansible path executes a playbook by starting a SIBLING CONTAINER through a Docker daemon (internal/adapters/legacy's DockerOrchestrator). A pod has no Docker daemon, and there are exactly two ways to give it one:\n\n  1. Mount the node's /var/run/docker.sock into the runner. That hands this container full control of the container runtime on that node: it can start a privileged container, mount the host filesystem, and read the secrets of every other pod scheduled there. It is a node escape with extra steps, and this chart runs other people's automation.\n  2. Run a Docker-in-Docker sidecar. That is a privileged container sharing the pod, which is the same authority in a different shape.\n\nBoth are refused. Run unconverted playbooks on a Docker host with docker-compose.yml, which is where that adapter is supported, and use this chart for the native execution path. A daemonless container backend for the runner would remove this restriction; it does not exist yet." -}}
{{- end -}}
{{- range .Values.runner.extraEnv -}}
{{- if eq .name "RUNNER_WAL_DIR" -}}
{{- include "the-pleiades.refuse" "runner.extraEnv sets RUNNER_WAL_DIR, which this chart cannot support safely.\n\nThe result write-ahead log needs a durable volume PER REPLICA, and the runner is a Deployment, which has no volumeClaimTemplates. Every replica would share one claim (concurrent writers on one log) or write into the pod's own filesystem, which is read-only here, so the runner fails closed at startup. Either way the log does not do the job it exists to do, which is to survive the process that wrote it." -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
The three TLS arrangements, and the one combination that cannot be made safe.
*/}}
{{- define "the-pleiades.validate.tls" -}}
{{- $mode := .Values.controller.tls.mode -}}
{{- if not (has $mode (list "self-provisioned" "secret" "upstream")) -}}
{{- include "the-pleiades.refuse" (printf "controller.tls.mode is %q. It must be one of: self-provisioned (the controller generates and reuses its own certificate), secret (it serves controller.tls.secretName), upstream (an ingress in front of it already terminated TLS, so it serves plain HTTP)." $mode) -}}
{{- end -}}
{{- if and (eq $mode "secret") (not .Values.controller.tls.secretName) -}}
{{- include "the-pleiades.refuse" "controller.tls.mode is \"secret\" but controller.tls.secretName is empty. Name a kubernetes.io/tls Secret holding tls.crt and tls.key, which is the shape cert-manager writes." -}}
{{- end -}}
{{- /*
SEVERAL SELF-PROVISIONING CONTROLLERS ARE NOW ALLOWED, AS LONG AS THEY SHARE A
DIRECTORY. This refusal used to cover every replica count above one, and half
its reason has since been deleted from the product: internal/tlscert's Ensure
is lock-free, so N controllers over one directory all reach a serving state
without waiting on each other. The read-modify-write race this used to cite no
longer exists, and a refusal that cites a deleted race blocks a configuration
that works.

WHAT IS LEFT, AND WHY IT IS A NOTE RATHER THAN A REFUSAL. Ensure's own doc
comment records one residual: after a CONTENDED cold start, a racer whose
re-read lands before a later racer's rename serves the certificate it read for
the life of that process, so two replicas can present different self-signed
certificates until they next restart. That is a convergence delay, not a
divergence: nothing rewrites a bundle that can still be served, so the last
rename stands and every replica adopts it on its next start. It is worth
telling an operator about (NOTES.txt does) and not worth refusing over, because
these certificates encrypt and do not authenticate. A client can only trust one
by being handed that exact certificate, so it is pinning per-pod material
either way, and the container healthcheck already accepts a replica serving any
certificate this deployment provisioned.

WHAT IS STILL REFUSED is the case where there is no shared directory at all,
which is the one thing convergence cannot survive.
*/}}
{{- if eq $mode "self-provisioned" -}}
{{- if or (gt (int .Values.controller.replicaCount) 1) .Values.controller.autoscaling.enabled -}}
{{- if not .Values.controller.persistence.enabled -}}
{{- include "the-pleiades.refuse" "controller.tls.mode is \"self-provisioned\" with controller.persistence.enabled=false, while more than one controller replica can exist (controller.replicaCount > 1, or controller.autoscaling.enabled).\n\nWith persistence off, each replica's /data is an emptyDir: its own directory, gone with the pod. So every replica generates its OWN certificate, and generates a NEW one every time it restarts. There is nothing for them to converge on, ever. A client that was handed one pod's certificate to trust gets a name it does not recognise from the next pod that answers, and from the same pod after a restart.\n\nSeveral self-provisioning replicas ARE supported now, sharing one directory, which is what makes them converge on one certificate:\n\n  --set controller.persistence.enabled=true --set controller.persistence.accessMode=ReadWriteMany\n\nThat needs storage that can serve a volume to more than one node at once. Without it, use a certificate every replica can serve identically:\n\n  --set controller.tls.mode=secret --set controller.tls.secretName=<your kubernetes.io/tls secret>\n\nor terminate TLS in front of the controller and tell it so:\n\n  --set controller.tls.mode=upstream" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
The three values with no default. Each message carries the command that
generates one, because "required" without that is a puzzle.
*/}}
{{- define "the-pleiades.validate.secrets" -}}
{{- if not .Values.secrets.existingSecret -}}
{{- if not .Values.secrets.masterEncryptionKey -}}
{{- include "the-pleiades.refuse" "secrets.masterEncryptionKey is not set, and this chart will not invent one.\n\nIt is base64 of exactly 32 random bytes:\n\n  --set secrets.masterEncryptionKey=\"$(openssl rand -base64 32)\"\n\nEvery credential and every encrypted device property in the database is encrypted under this key. A chart that generated it would generate a DIFFERENT one on the next `helm upgrade`, because `helm template` has no cluster to read the old value back from, and everything already stored would become permanently undecryptable with no error at upgrade time. Set it once, keep it, or point secrets.existingSecret at a Secret you manage." -}}
{{- end -}}
{{- if not .Values.secrets.jwtSecret -}}
{{- include "the-pleiades.refuse" "secrets.jwtSecret is not set, and this chart will not invent one (a regenerated value on the next upgrade would sign every operator out).\n\nAt least 32 bytes:\n\n  --set secrets.jwtSecret=\"$(openssl rand -hex 32)\"" -}}
{{- end -}}
{{- if and .Values.postgresql.enabled (not .Values.postgresql.auth.password) -}}
{{- include "the-pleiades.refuse" "postgresql.auth.password is not set and the in-chart database is enabled.\n\n  --set postgresql.auth.password=\"$(openssl rand -hex 16)\"\n\nThere is no default because a default here would be the same database password on every installation of this chart. Set postgresql.enabled=false and use externalDatabase if you would rather point at a database you already run." -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
The two backends, each of which must be either deployed here or pointed at
somewhere else. Neither has a usable fallback: the controller exits at startup
on an unreachable broker, and a missing DSN would send it to its own relative
SQLite default inside a container, which the next restart discards.
*/}}
{{- define "the-pleiades.validate.backends" -}}
{{- if not .Values.postgresql.enabled -}}
{{- if and (not .Values.externalDatabase.dsn) (not .Values.externalDatabase.existingSecret) (not .Values.secrets.existingSecret) -}}
{{- include "the-pleiades.refuse" "postgresql.enabled is false but no external database is configured. Set externalDatabase.dsn, or externalDatabase.existingSecret to name a Secret carrying the DSN, or secrets.existingSecret to name one carrying every key including DB_DSN." -}}
{{- end -}}
{{- end -}}
{{- if and (not .Values.nats.enabled) (not .Values.externalNats.url) -}}
{{- include "the-pleiades.refuse" "nats.enabled is false but externalNats.url is empty. The controller and every runner dial the broker at startup and exit when it is unreachable, so there is no configuration in which this is left blank." -}}
{{- end -}}
{{- end }}

{{/*
Image references, checked for the two ways this chart has already been broken
once each.
*/}}
{{- define "the-pleiades.validate.images" -}}
{{- include "the-pleiades.validate.image" (dict "key" "controller.image" "image" .Values.controller.image) -}}
{{- include "the-pleiades.validate.image" (dict "key" "runner.image" "image" .Values.runner.image) -}}
{{- if .Values.postgresql.enabled -}}
{{- include "the-pleiades.validate.image" (dict "key" "postgresql.image" "image" .Values.postgresql.image) -}}
{{- end -}}
{{- if .Values.nats.enabled -}}
{{- include "the-pleiades.validate.image" (dict "key" "nats.image" "image" .Values.nats.image) -}}
{{- end -}}
{{- end }}

{{- define "the-pleiades.validate.image" -}}
{{- $key := .key -}}
{{- $image := .image -}}
{{- if not $image.repository -}}
{{- include "the-pleiades.refuse" (printf "%s.repository is empty." $key) -}}
{{- end -}}
{{- if not $image.tag -}}
{{- include "the-pleiades.refuse" (printf "%s.tag is empty. It is stated explicitly and never derived from .Chart.AppVersion: an empty tag falling back to the chart's appVersion is exactly how this chart shipped deploying nginx:1.16.0." $key) -}}
{{- end -}}
{{- if eq $image.tag "latest" -}}
{{- include "the-pleiades.refuse" (printf "%s.tag is \"latest\", which names a different image tomorrow than it does today and makes a rollback unrepeatable. Name the version you tested." $key) -}}
{{- end -}}
{{- if or (contains "@" $image.repository) (contains "@" $image.tag) -}}
{{- include "the-pleiades.refuse" (printf "%s is pinned by digest. Use a tag.\n\nA digest-pinned reference does NOT resolve against a side-loaded image, even when the local daemon reports that exact digest, so digest pinning breaks every air-gapped install, every `kind load docker-image` and every `docker save`/`ctr images import` workflow. Those are the installs this chart exists to support, and nothing published anywhere has a digest to pin to yet." $key) -}}
{{- end -}}
{{- end }}

{{/*
The remaining shape checks: a volume more replicas than it can serve, budgets
Kubernetes itself would reject, and a runbook source given twice.
*/}}
{{- define "the-pleiades.validate.workloads" -}}
{{- /* ReadWriteMany is the ONLY mode that serves more than one replica, so the
     test is against that rather than against ReadWriteOnce by name.
     ReadWriteOncePod is the third value the schema allows and it is stricter
     still (one POD, not one node), so naming only ReadWriteOnce here let the
     tightest of the three render a release whose extra replicas can never
     schedule. */}}
{{- if and .Values.controller.persistence.enabled (ne .Values.controller.persistence.accessMode "ReadWriteMany") -}}
{{- if or (gt (int .Values.controller.replicaCount) 1) .Values.controller.autoscaling.enabled -}}
{{- include "the-pleiades.refuse" (printf "controller.persistence is a %s volume while more than one controller replica can exist.\n\nThis chart creates ONE claim for the controller, not one per replica: a Deployment has no volumeClaimTemplates. ReadWriteOnce means one node may mount it and ReadWriteOncePod means one pod may, so a second replica sits Pending forever on a volume it cannot attach.\n\nThree ways out, and which is right depends on what you keep in /data:\n\n  --set controller.persistence.accessMode=ReadWriteMany   (a shared filesystem, if your storage offers one; this is also what lets several self-provisioning controllers converge on one certificate)\n  --set controller.persistence.enabled=false              (nothing durable is kept; correct when TLS comes from a Secret or an ingress and no device credential file is in use)\n  --set controller.replicaCount=1" .Values.controller.persistence.accessMode) -}}
{{- end -}}
{{- end -}}
{{- include "the-pleiades.validate.heartbeat" . -}}
{{- include "the-pleiades.validate.outagebudget" . -}}
{{- include "the-pleiades.validate.natstls" . -}}
{{- include "the-pleiades.validate.pdb" (dict "key" "controller.podDisruptionBudget" "pdb" .Values.controller.podDisruptionBudget "example" "2") -}}
{{- include "the-pleiades.validate.pdb" (dict "key" "runner.podDisruptionBudget" "pdb" .Values.runner.podDisruptionBudget "example" "1") -}}
{{- if and .Values.runbooks.configMapName .Values.runbooks.existingClaim -}}
{{- include "the-pleiades.refuse" "runbooks.configMapName and runbooks.existingClaim both name a runbook source. Set one. The controller and the runners must mount the SAME source, so there is exactly one to choose." -}}
{{- end -}}
{{- if and .Values.runner.knownHosts.configMapName .Values.runner.knownHosts.secretName -}}
{{- include "the-pleiades.refuse" "runner.knownHosts.configMapName and runner.knownHosts.secretName both name a host key source. Set one. Both mount at the same path, so the chart would have to pick one silently, and the one it picked would decide which device keys every SSH task trusts." -}}
{{- end -}}
{{- end }}

{{/*
The three heartbeat numbers, which are one setting in three places and are
only correct in relation to each other.

WHY THIS IS A REFUSAL AND NOT A DEFAULT. The runner touches its heartbeat file
once per intervalSeconds, so the file's age sits anywhere between zero and one
interval even when everything is perfect. A staleness limit at or below the
interval therefore reports a perfectly healthy runner unhealthy the first time
a round trip is slow. On the liveness probe that means the kubelet kills a
working pod, abandoning whatever it was executing, and then kills its
replacement, forever. The whole fleet crash-loops and the cause is three
numbers nobody looks at again after writing them.

The multiple is TWO rather than one, so a single missed beat can never be
enough. Below that the check would permit a configuration that is arithmetically
legal and practically a flap.

LIVENESS MUST NOT BE STRICTER THAN READINESS for a plainer reason: a pod that
gets restarted before it is ever marked not-ready makes the readiness probe
dead weight, and a rollout would replace working runners with restarting ones
while reporting progress.
*/}}
{{- define "the-pleiades.validate.heartbeat" -}}
{{- $hb := .Values.runner.heartbeat -}}
{{- $interval := int $hb.intervalSeconds -}}
{{- $ready := int $hb.readinessStaleAfterSeconds -}}
{{- $live := int $hb.livenessStaleAfterSeconds -}}
{{- if lt $ready (mul $interval 2) -}}
{{- include "the-pleiades.refuse" (printf "runner.heartbeat.readinessStaleAfterSeconds is %d, which is less than twice runner.heartbeat.intervalSeconds (%d).\n\nThe runner touches its heartbeat once per interval, so the file's age is somewhere between zero and one interval even when the broker is perfectly healthy. A limit this tight reports a working runner unhealthy the first time one round trip is slow, and every runner in the fleet flaps together.\n\nGive it room for at least two missed beats:\n\n  --set runner.heartbeat.readinessStaleAfterSeconds=%d" $ready $interval (mul $interval 3)) -}}
{{- end -}}
{{- if lt $live (mul $interval 2) -}}
{{- include "the-pleiades.refuse" (printf "runner.heartbeat.livenessStaleAfterSeconds is %d, which is less than twice runner.heartbeat.intervalSeconds (%d).\n\nThis one is the more dangerous of the two, because crossing it RESTARTS the pod. A limit inside one heartbeat interval means the kubelet kills a working runner on the first slow round trip, abandons the job it was executing, and does the same to its replacement.\n\nGive it room for at least two missed beats:\n\n  --set runner.heartbeat.livenessStaleAfterSeconds=%d" $live $interval (mul $interval 6)) -}}
{{- end -}}
{{- if lt $live $ready -}}
{{- include "the-pleiades.refuse" (printf "runner.heartbeat.livenessStaleAfterSeconds (%d) is lower than runner.heartbeat.readinessStaleAfterSeconds (%d), so a runner would be RESTARTED before it was ever marked not ready.\n\nThat makes the readiness probe dead weight and turns a broker blip into a fleet-wide restart instead of a paused rollout. Liveness is meant to be the more forgiving of the two: readiness costs a rollout, liveness costs a running job." $live $ready) -}}
{{- end -}}
{{- end }}

{{/*
One enabled PodDisruptionBudget, checked for the two shapes that cannot be
rendered into a working object.

BOTH SET is refused because the API server refuses it: a budget carries
minAvailable or maxUnavailable, never both.

NEITHER SET is refused because the alternative is silence. The template used to
treat maxUnavailable as the else branch of `if minAvailable`, so an operator who
cleared maxUnavailable (in order to set minAvailable, and then set it to
something the old check read as empty) rendered a budget with no field, or with
the `enabled` flag on, rendered nothing at all. Either way `kubectl get pdb`
showed nothing and the operator believed a drain was constrained when it was
not. A budget that quietly does not exist is worse than no budget, because
nobody goes looking for it.

"Set" here means the-pleiades.isSet, so 0 counts as an answer on both fields.
minAvailable 0 permits every eviction and maxUnavailable 0 permits none; both
are real configurations somebody may want, and neither is truthy.
*/}}
{{- define "the-pleiades.validate.pdb" -}}
{{- $key := .key -}}
{{- $pdb := .pdb -}}
{{- if $pdb.enabled -}}
{{- $min := eq (include "the-pleiades.isSet" $pdb.minAvailable) "set" -}}
{{- $max := eq (include "the-pleiades.isSet" $pdb.maxUnavailable) "set" -}}
{{- if and $min $max -}}
{{- include "the-pleiades.refuse" (printf "%s sets both minAvailable and maxUnavailable. A PodDisruptionBudget carries one or the other and the API server rejects an object with both. maxUnavailable is this chart's default, so choosing minAvailable means clearing it:\n\n  --set %s.minAvailable=%s --set %s.maxUnavailable=\"\"" $key $key .example $key) -}}
{{- end -}}
{{- if not (or $min $max) -}}
{{- include "the-pleiades.refuse" (printf "%s.enabled is true but neither minAvailable nor maxUnavailable is set, and a PodDisruptionBudget has to carry one of the two.\n\nThis refusal exists because the alternative was silent. Rendering nothing here would leave `kubectl get pdb` empty in a release whose values say disruption protection is on, and nobody rechecks a setting they already wrote down.\n\nState the one you meant:\n\n  --set %s.minAvailable=%s --set %s.maxUnavailable=\"\"    (keep at least this many pods up)\n  --set %s.maxUnavailable=1                              (allow at most this many to go down)\n\nNote that 0 counts as an answer on both: minAvailable=0 permits every voluntary eviction and maxUnavailable=0 permits none." $key $key .example $key $key) -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
The one refusal that reads the cluster: a release whose database credentials
cannot open the data volume that is already there.

THE FAILURE THIS REPLACES, exactly. `helm uninstall` leaves the PostgreSQL
claim behind, on purpose, because a chart that deleted a database on uninstall
would be a worse chart than one that leaves a volume to clean up. A later
`helm install` under the same release name therefore lands on a volume that
already holds an initialized database. PostgreSQL applies POSTGRES_PASSWORD,
POSTGRES_USER and POSTGRES_DB only when it initializes an EMPTY directory, so a
different password on the second install is simply ignored by the database and
believed by the controller. Everything installs. Every object reports created.
The database comes up healthy. The controller then fails authentication, exits,
gets restarted, and does that forever, and nothing anywhere says the word
"password". The runners do the same behind it.

HOW IT IS DETECTED. The StatefulSet stamps a fingerprint of the three
credentials onto its volumeClaimTemplate, Kubernetes copies that onto the claim
it creates, and the claim outlives the release. This check looks the claim up
and compares. It refuses ONLY on a proven mismatch: a claim with no stamp (a
volume this chart did not create) and a release whose credentials come from
secrets.existingSecret (a password this chart never sees) are both cases where
the chart has no evidence, and inventing a refusal from no evidence would block
correct installs.

WHY IT IS SILENT UNDER `helm template`. `lookup` needs an API server and
returns nothing without one, so this check does nothing during a plain
`helm template` or `helm install --dry-run=client`, and everything during a
real install, upgrade or `--dry-run=server`. That is a real limit: tools/helm-lint
cannot exercise it, so the Kubernetes half of the release gate does, against a
real cluster, by really uninstalling and really reinstalling with a changed
password.

WHAT IT NEEDS FROM THE INSTALLER'S CREDENTIALS. Read access to
PersistentVolumeClaims in the release namespace, which the built-in `edit` and
`admin` roles both carry. A missing claim is not an error (that is the ordinary
first install), but a FORBIDDEN answer is one helm reports rather than
swallowing, so an installer restricted below that would have to be granted the
read or use postgresql.persistence.existingClaim, which skips this check.
*/}}
{{- define "the-pleiades.validate.retaineddata" -}}
{{- if and .Values.postgresql.enabled .Values.postgresql.persistence.enabled (not .Values.postgresql.persistence.existingClaim) -}}
{{- $want := include "the-pleiades.postgres.credentialFingerprint" . -}}
{{- if $want -}}
{{- $claim := include "the-pleiades.postgres.claimName" . -}}
{{- $existing := lookup "v1" "PersistentVolumeClaim" .Release.Namespace $claim -}}
{{- if $existing -}}
{{- $stamped := "" -}}
{{- if $existing.metadata.annotations -}}
{{- $stamped = index $existing.metadata.annotations (include "the-pleiades.postgres.fingerprintAnnotation" .) | default "" -}}
{{- end -}}
{{- if and $stamped (ne $stamped $want) -}}
{{- include "the-pleiades.refuse" (printf "The database credentials in these values do not match the ones the retained data volume was initialized with, so this release would install cleanly and then crash-loop forever.\n\nTHE VOLUME: PersistentVolumeClaim %s in namespace %s. It is still there because `helm uninstall` does not delete it, which is deliberate: a chart that destroyed a database on uninstall would be the more dangerous chart. It holds an initialized PostgreSQL database.\n\nWHAT WOULD HAPPEN. PostgreSQL sets the username, the database name and the password ONLY when it initializes an empty directory. On a volume that already holds a database it ignores all three, so the database keeps the old ones and the controller connects with the new ones. Authentication fails, the controller exits, Kubernetes restarts it, and that repeats with nothing in the output naming the cause. The runners do the same behind it.\n\nYOUR TWO CHOICES, and only one of them destroys anything:\n\n  1. KEEP THE DATA (nothing is destroyed). Install with the postgresql.auth values this volume was created with: the same username, the same database name and the same password. If the password is lost, the data in that volume is not reachable by this chart and choice 2 is the only one left.\n\n  2. START OVER (THIS DESTROYS THE DATABASE). Delete the claim, then install again:\n\n       kubectl delete pvc %s --namespace %s\n\n     Everything in it goes: every user, device, template, job, credential and role binding. There is no undo and this chart takes no backups.\n\nTwo other ways out, if this release should not be touching that volume at all: point postgresql.persistence.existingClaim at the claim you meant, or set postgresql.enabled=false and use externalDatabase for a database you manage." $claim .Release.Namespace $claim .Release.Namespace) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
The chart must not claim an outage budget its own probes cancel.

The runner's liveness probe reads a heartbeat that only advances when the
broker answers, so crossing livenessStaleAfterSeconds RESTARTS the pod and
abandons whatever it was executing. If that limit is shorter than
mesh.maxOutageSeconds, Kubernetes kills the runner partway through the
very outage the budget promises to survive, and the promise is false of
the product while being true of the binary.
*/}}
{{- define "the-pleiades.validate.outagebudget" -}}
{{- if gt (int .Values.mesh.maxOutageSeconds) (int .Values.runner.heartbeat.livenessStaleAfterSeconds) -}}
{{- fail (printf "mesh.maxOutageSeconds is %d but runner.heartbeat.livenessStaleAfterSeconds is %d. The liveness probe restarts the runner and abandons its work once the heartbeat is that stale, so an outage budget longer than it cannot be survived on Kubernetes. Raise runner.heartbeat.livenessStaleAfterSeconds to at least the budget, or lower the budget." (int .Values.mesh.maxOutageSeconds) (int .Values.runner.heartbeat.livenessStaleAfterSeconds)) -}}
{{- end -}}
{{- end -}}

{{/*
Broker TLS needs certificate material, and a websocket listener asked to
serve TLS needs the same. Enabling either without a Secret produces a
broker that fails to start with a message about a file it cannot open,
which is a worse way to learn this than the install refusing.
*/}}
{{- define "the-pleiades.validate.natstls" -}}
{{- if and .Values.nats.tls.enabled (not .Values.nats.tls.secretName) -}}
{{- include "the-pleiades.refuse" "nats.tls.enabled is true but nats.tls.secretName is empty. Name an existing kubernetes.io/tls Secret holding tls.crt and tls.key; this chart does not mint broker certificates." -}}
{{- end -}}
{{- if and .Values.nats.websocket.tls (not .Values.nats.tls.secretName) -}}
{{- include "the-pleiades.refuse" "nats.websocket.tls is true but nats.tls.secretName is empty. The websocket listener serves the same certificate material as the client listener, so it needs the same Secret." -}}
{{- end -}}
{{- if and .Values.nats.websocket.tls (not .Values.nats.websocket.enabled) -}}
{{- include "the-pleiades.refuse" "nats.websocket.tls is true but nats.websocket.enabled is false, so there is no websocket listener for it to apply to. Enable the listener or unset its tls flag." -}}
{{- end -}}
{{- end -}}
