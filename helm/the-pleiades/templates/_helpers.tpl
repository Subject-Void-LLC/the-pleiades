{{/*
Naming, labelling and connection-string helpers shared by every template in
this chart.

Everything that more than one template has to agree on lives here: the four
workload names, the label sets that join a pod to its service, the name of the
Secret the controller reads its keys from, and the two connection strings that
change shape depending on whether the in-chart database and broker are enabled.
A second copy of any of these in a template is a place for the two to drift.
*/}}

{{/*
the-pleiades.name is the chart's own name, overridable, and is what
app.kubernetes.io/name carries on every object.
*/}}
{{- define "the-pleiades.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
the-pleiades.fullname is the release-scoped prefix every object name is built
from.

Truncated at 63 characters because that is the DNS label limit several
Kubernetes name fields impose. This is the prefix ONLY: the per-component
helpers below cut it further, to leave room for the component they append, and
the reason that order matters is written at the-pleiades.componentName.
*/}}
{{- define "the-pleiades.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
the-pleiades.chart is the chart name and version as one label value.
*/}}
{{- define "the-pleiades.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
the-pleiades.dnsLabelMax is 63, the length limit of a single DNS label, and
therefore of a Kubernetes name or label value that has to be one.

Every ceiling below is derived from this number by subtracting whatever
Kubernetes itself appends to the name before something has to fit in a label.
*/}}
{{- define "the-pleiades.dnsLabelMax" -}}63{{- end }}

{{/*
the-pleiades.revisionHashReserve is what a StatefulSet's name loses to the
revision hash Kubernetes stamps on every pod it creates.

WHAT THE RESERVE IS FOR. The StatefulSet controller labels each of its pods
with controller-revision-hash, whose VALUE is the ControllerRevision name:
"<statefulset name>-<hash>". A label value may not exceed 63 bytes, so a
StatefulSet whose name leaves no room for that hash produces pods the API
server REFUSES. The StatefulSet itself is accepted (its own name is only held
to the 253-character subdomain limit), so nothing fails at install time: the
object is created, reports 0/1 ready forever, and the only evidence is a
FailedCreate event nobody is watching for.

That was measured against a real cluster rather than reasoned about. A
54-character StatefulSet name produced:

  Pod "<name>-0" is invalid: metadata.labels: Invalid value:
  "<name>-5f6f5d6649": must be no more than 63 bytes

11 is one dash plus 10 characters of hash. The hash is a uint32 printed in
decimal and then re-encoded character for character, so it is 1 to 10
characters long and its length changes with the pod template. A 53-character
name happened to draw a 9-character hash and worked; the same name with a
different pod template would not. Reserving the maximum is the only way this
is not a coin flip.

TWO OTHER LIMITS SIT INSIDE THIS ONE, so reserving 11 satisfies them too, and
they are worth naming because they are the ones people expect to be binding:
a StatefulSet's pods are named "<statefulset name>-<ordinal>", and that string
is both the pod's spec.hostname (validated as a DNS label, so 63) and the
statefulset.kubernetes.io/pod-name label value (63 again). Those allow a
61-character name at one digit of ordinal. The revision hash is 9 characters
stricter, so it decides.
*/}}
{{- define "the-pleiades.revisionHashReserve" -}}11{{- end }}

{{/*
the-pleiades.componentSuffixReserve is what every workload name gives up to
the component word appended to it: one dash plus "controller", the longest
component this chart has.

ONE reserve for all four, rather than one per suffix, so that the names of a
release differ only in the component and share a prefix a reader can match up.
Sizing each suffix separately would fit a few more characters and produce four
different prefixes, which is harder to read and no more correct.
*/}}
{{- define "the-pleiades.componentSuffixReserve" -}}11{{- end }}

{{/*
the-pleiades.componentNameCeiling is the longest an object name may be for the
KINDS that component's name is rendered as. It takes the component string
itself, not the usual dict.

ONE NUMBER FOR ALL FOUR WORKLOADS WAS WRONG, and this helper is that fix. The
budget used to be 63 for everything, which is right for a Service and for a
Deployment and 11 characters too generous for a StatefulSet, for the reason
written at the-pleiades.revisionHashReserve. A test that only read Deployment
names passed the whole time.

  controller  Deployment, Service, PersistentVolumeClaim,
              HorizontalPodAutoscaler, PodDisruptionBudget. The Service is the
              binding one: a Service name is a DNS label and the API server
              rejects a longer one outright, which is at least a loud failure.
              63.

  runner      Deployment, PodDisruptionBudget. Neither is held to 63 (both
              names are DNS subdomains, 253), and a Deployment's generated
              names cannot overflow either: the pod-name generator truncates
              its base at 58 characters before appending 5 random ones, so a
              Deployment of any legal length still produces pods. It shares
              the controller's ceiling anyway, so the two Deployments of one
              release share a prefix.

  postgres    StatefulSet plus its governing Service. 63 minus the revision
  nats        hash reserve.
*/}}
{{- define "the-pleiades.componentNameCeiling" -}}
{{- $label := include "the-pleiades.dnsLabelMax" . | int -}}
{{- if has . (list "postgres" "nats") -}}
{{- sub $label (include "the-pleiades.revisionHashReserve" . | int) -}}
{{- else if has . (list "controller" "runner") -}}
{{- $label -}}
{{- else -}}
{{- fail (printf "\n\nthe-pleiades.componentNameCeiling was called with component %q, which is not one of the four this chart names. A new component needs a decision recorded here first: the ceiling depends on the KINDS its name is rendered as, and a StatefulSet's ceiling is 11 characters lower than a Deployment's. Add it to helm/the-pleiades/templates/_helpers.tpl next to the component whose kinds it matches.\n" .) -}}
{{- end -}}
{{- end }}

{{/*
the-pleiades.componentName is one workload's object name: the release-scoped
prefix, cut to that component's own budget, then the component appended.

TRUNCATE FIRST, APPEND SECOND. That order is half the fix, and the bug it
replaces was not theoretical. The old helpers appended the component and then
truncated the RESULT to 63, so any release name that pushed the prefix to 62 or
63 characters (49 to 53 characters of release name, all of them legal to Helm,
whose own limit is 53) had its entire suffix cut away. The controller, the
runner, the PostgreSQL StatefulSet and the NATS StatefulSet then rendered under
ONE name: four different workloads, four objects claiming the same name, and
whichever the API server applied last silently replaced the others. Cutting the
prefix instead means a long release name loses characters from a part nobody
reads, and every workload keeps the word that says what it is.

CUT TO THE RIGHT LIMIT is the other half, and it was missing. Truncating every
component to the same 63-character ceiling gave the two StatefulSets a ceiling
11 characters above the only limit that binds them, so a release name of 31
characters or more traded four workloads sharing one name for two StatefulSets
whose pods the API server refuses to create. Both failures are silent at
install time; this one is quieter, because every object exists and only the
pods are missing.

Takes a dict of "root" (the top-level context) and "component", matching the
label helpers below, so the component string in a name and the component label
on the same object come from one argument.
*/}}
{{- define "the-pleiades.componentName" -}}
{{- $ceiling := include "the-pleiades.componentNameCeiling" .component | int -}}
{{- $reserve := include "the-pleiades.componentSuffixReserve" . | int -}}
{{- /* The reserve is sized for the longest component that exists today, so a
     longer one added later would push a name past its ceiling and silently
     reintroduce the truncation this helper was written to remove. Saying so at
     render time is the whole difference between a fixed bug and a fixed
     instance of it. */}}
{{- if gt (add (len .component) 1) $reserve -}}
{{- fail (printf "\n\nthe-pleiades.componentName was called with component %q, which is longer than the-pleiades.componentSuffixReserve leaves room for. The reserve (%d) is one dash plus the longest component this chart has. Raise it to %d in helm/the-pleiades/templates/_helpers.tpl, which shortens every workload name by the same amount, or use a shorter component.\n" .component $reserve (add (len .component) 1)) -}}
{{- end -}}
{{- $budget := sub $ceiling $reserve | int -}}
{{- $name := printf "%s-%s" (include "the-pleiades.fullname" .root | trunc $budget | trimSuffix "-") .component -}}
{{- /* The arithmetic above cannot produce an over-long name, which is exactly
     why this is worth checking: the next edit to it can, and a name that is
     one character over its ceiling fails in a cluster and nowhere else. */}}
{{- if gt (len $name) $ceiling -}}
{{- fail (printf "\n\nthe-pleiades.componentName built %q for component %q, which is %d characters and past the %d that component's kinds are held to. This is an arithmetic fault in helm/the-pleiades/templates/_helpers.tpl, not a configuration error.\n" $name .component (len $name) $ceiling) -}}
{{- end -}}
{{- $name -}}
{{- end }}

{{/*
The four workload names, each one the shared helper above with its own
component. Nothing else in the chart builds a workload name by hand.
*/}}
{{- define "the-pleiades.controller.fullname" -}}
{{- include "the-pleiades.componentName" (dict "root" . "component" "controller") }}
{{- end }}

{{- define "the-pleiades.runner.fullname" -}}
{{- include "the-pleiades.componentName" (dict "root" . "component" "runner") }}
{{- end }}

{{- define "the-pleiades.postgres.fullname" -}}
{{- include "the-pleiades.componentName" (dict "root" . "component" "postgres") }}
{{- end }}

{{- define "the-pleiades.nats.fullname" -}}
{{- include "the-pleiades.componentName" (dict "root" . "component" "nats") }}
{{- end }}

{{/*
the-pleiades.isSet answers "did the operator state this value", and returns
the string "set" when they did and the empty string when they did not.

WHY THIS EXISTS. A Helm template's `if` asks whether a value is TRUTHY, and
truthy is the wrong question for any setting where 0 is a legal answer. The
PodDisruptionBudget templates asked it anyway: `if .minAvailable` was false for
this chart's own default of "" AND false for 0, so an operator who cleared
maxUnavailable and set minAvailable to 0 got a budget with no field at all, and
an operator who cleared maxUnavailable and set nothing else got NO BUDGET, with
no error, while believing they had configured disruption protection.

The three cases, and why each answers the way it does:

  nil            not set. `kindIs "invalid"` is how a Helm template asks
                 whether a value is nil; a plain comparison against nil is not
                 available.
  "" (string)    not set. This chart uses the empty string as "leave it alone"
                 in values.yaml, because YAML has no way to write "absent" in a
                 file whose whole job is to list the keys.
  anything else  set, INCLUDING the integer 0 and the string "0", which are
                 both meaningful budgets: minAvailable 0 permits every
                 eviction, maxUnavailable 0 permits none.

Returning a string rather than a boolean is not a preference: an `include`
always yields a string, so the caller compares with `eq ... "set"`.
*/}}
{{- define "the-pleiades.isSet" -}}
{{- if kindIs "invalid" . -}}
{{- else if kindIs "string" . -}}
{{- if . }}set{{ end -}}
{{- else -}}
set
{{- end -}}
{{- end }}

{{/*
the-pleiades.labels is the common label set every object carries.

Takes a dict of "root" (the top-level context) and "component" (controller,
runner, postgres or nats), because a chart that deploys four different
workloads needs app.kubernetes.io/component to tell them apart. Without it a
`kubectl get pods -l app.kubernetes.io/instance=<release>` cannot distinguish a
controller from a runner.
*/}}
{{- define "the-pleiades.labels" -}}
{{- $root := .root -}}
helm.sh/chart: {{ include "the-pleiades.chart" $root }}
{{ include "the-pleiades.selectorLabels" . }}
{{- if $root.Chart.AppVersion }}
app.kubernetes.io/version: {{ $root.Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: {{ include "the-pleiades.name" $root }}
app.kubernetes.io/managed-by: {{ $root.Release.Service }}
{{- end }}

{{/*
the-pleiades.selectorLabels is the immutable subset: the labels a Deployment's
or StatefulSet's selector matches on, and the labels its Service selects with.

Deliberately smaller than the label set above. A selector is immutable after
creation, so anything that changes between chart versions (helm.sh/chart,
app.kubernetes.io/version) must stay out of it or the next `helm upgrade` fails
with "field is immutable".
*/}}
{{- define "the-pleiades.selectorLabels" -}}
{{- $root := .root -}}
app.kubernetes.io/name: {{ include "the-pleiades.name" $root }}
app.kubernetes.io/instance: {{ $root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
the-pleiades.pdbBudget is the one budget field a PodDisruptionBudget carries,
rendered from whichever of minAvailable and maxUnavailable the operator set.

Takes one podDisruptionBudget block (the controller's or the runner's). The
"exactly one is set" rule is enforced in _validations.tpl rather than here,
because a refusal has to name the value and the key that caused it, and this
helper is reached from two different places that would each have to reword it.
By the time this runs, exactly one of the two is set, so the else branch is the
maxUnavailable case and not a fallback: with neither set the chart has already
refused to render.

The value is passed through unquoted, deliberately. A PodDisruptionBudget's
budget field takes an integer OR a percentage string ("50%"), and quoting an
integer would send the API server a string where it wants a number.
*/}}
{{- define "the-pleiades.pdbBudget" -}}
{{- if eq (include "the-pleiades.isSet" .minAvailable) "set" -}}
minAvailable: {{ .minAvailable }}
{{- else -}}
maxUnavailable: {{ .maxUnavailable }}
{{- end -}}
{{- end }}

{{/*
the-pleiades.serviceAccountName is the account every pod in this release runs
under.

One account for all four workloads, and that is a deliberate simplification
rather than an oversight: nothing in this chart talks to the Kubernetes API, so
the account holds no RBAC of its own and there is nothing to separate. It
exists so an operator can attach annotations (an IRSA role, a Workload Identity
binding) in one place, and so automountServiceAccountToken can be turned off,
which values.yaml does by default.
*/}}
{{- define "the-pleiades.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "the-pleiades.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
the-pleiades.secretName is the Secret every credential in this release is read
from: the master encryption key, the JWT signing secret, the database DSN and
(when the in-chart database is enabled) its password.

One name, resolved in one place, because the controller and the PostgreSQL
StatefulSet both read from it and a chart where those two disagreed would come
up with a database nobody can log into.
*/}}
{{- define "the-pleiades.secretName" -}}
{{- if .Values.secrets.existingSecret }}
{{- .Values.secrets.existingSecret }}
{{- else }}
{{- include "the-pleiades.fullname" . }}
{{- end }}
{{- end }}

{{/*
the-pleiades.databaseDSN is the connection string the controller's DB_DSN
carries.

sslmode=disable on the in-chart path, stated rather than hidden: the PostgreSQL
StatefulSet in this chart serves no TLS, because configuring one would mean
issuing it a certificate, which is the problem this chart already refuses to
solve for the controller. Traffic between the controller and the in-chart
database is therefore unencrypted inside the cluster. A deployment that cares
sets postgresql.enabled=false and points externalDatabase.dsn at a managed
database with sslmode=require or sslmode=verify-full, which is the arrangement
that phrase belongs in anyway.

This template is only reached when the chart is building the Secret itself.
When externalDatabase.existingSecret names an operator's own Secret, no DSN is
composed here at all: the controller reads theirs.
*/}}
{{- define "the-pleiades.databaseDSN" -}}
{{- if .Values.postgresql.enabled -}}
postgres://{{ .Values.postgresql.auth.username }}:{{ .Values.postgresql.auth.password }}@{{ include "the-pleiades.postgres.fullname" . }}:{{ .Values.postgresql.service.port }}/{{ .Values.postgresql.auth.database }}?sslmode=disable
{{- else -}}
{{ .Values.externalDatabase.dsn }}
{{- end -}}
{{- end }}

{{/*
the-pleiades.postgres.claimName is the PersistentVolumeClaim the in-chart
PostgreSQL StatefulSet creates for itself.

The name is not this chart's choice: the StatefulSet controller builds it as
<volumeClaimTemplate name>-<statefulset name>-<ordinal>, and there is exactly
one replica, so the ordinal is always 0. It is written down here because the
validation below has to look that claim up by name, and a refusal that named
the wrong object would send an operator to delete the wrong volume.
*/}}
{{- define "the-pleiades.postgres.claimName" -}}
{{- printf "data-%s-0" (include "the-pleiades.postgres.fullname" .) -}}
{{- end }}

{{/*
the-pleiades.postgres.credentialFingerprint identifies the credentials a
PostgreSQL data volume was initialized with, without carrying them.

WHAT IT COVERS AND WHY ALL THREE. PostgreSQL applies POSTGRES_USER,
POSTGRES_DB and POSTGRES_PASSWORD only when it initializes an EMPTY data
directory. Against a volume that already holds a database, all three are
ignored: a changed password authenticates against nothing, a changed username
names a role that does not exist, and a changed database name names a database
that was never created. All three produce the same symptom, which is a
controller that starts, fails to connect, exits, and is restarted forever with
nothing in the chart's own output saying why. One fingerprint over all three
answers the one question that matters: can this release open that volume.

The master encryption key is deliberately NOT in it. Rotating that key is a
supported operation (the controller reads MASTER_ENCRYPTION_KEY_PREVIOUS to
decrypt what the old key wrote), so folding it in here would refuse a rotation
this platform is built to perform.

sha256 rather than the value itself, because this ends up as an annotation on
a PersistentVolumeClaim, which is readable by anyone who can read claims in the
namespace. That is the same shape as the `checksum/secret` annotation charts
everywhere put on a pod template, and it is worth stating rather than assuming:
the annotation identifies a credential set, it does not carry one.

Empty when secrets.existingSecret is set, because then the chart never sees the
password at all: it is in a Secret an operator manages, and a fingerprint of
values this chart does not have would be a fingerprint of the empty string.
*/}}
{{- define "the-pleiades.postgres.credentialFingerprint" -}}
{{- if not .Values.secrets.existingSecret -}}
{{- printf "%s:%s:%s" .Values.postgresql.auth.username .Values.postgresql.auth.database .Values.postgresql.auth.password | sha256sum | trunc 16 -}}
{{- end -}}
{{- end }}

{{/*
the-pleiades.postgres.fingerprintAnnotation is the annotation key the
fingerprint above is stamped under, in one place so the writer (the
volumeClaimTemplate) and the reader (the validation) cannot disagree about it.

The prefix is the chart name rather than a domain because this repository owns
no DNS name to claim. A prefix with no dot is a legal annotation prefix.
*/}}
{{- define "the-pleiades.postgres.fingerprintAnnotation" -}}the-pleiades/postgres-credentials{{- end }}

{{/*
the-pleiades.natsURL is the broker address both the controller and every runner
dial.

Unlike the DSN above it is not a secret and carries no credential, because the
in-chart NATS runs with no authorization at all. That is a real property of
this chart and it is written down in values.yaml next to the switch that turns
the in-chart broker off.
*/}}
{{- define "the-pleiades.natsURL" -}}
{{- if .Values.nats.enabled -}}
nats://{{ include "the-pleiades.nats.fullname" . }}:{{ .Values.nats.service.clientPort }}
{{- else -}}
{{ .Values.externalNats.url }}
{{- end -}}
{{- end }}

{{/*
the-pleiades.controller.autocertHosts is every name a self-provisioned
certificate has to carry, as one comma separated string for
PLEIADES_TLS_AUTOCERT_HOSTS.

The controller always puts localhost and the loopback addresses on the
certificate it generates, and those cover a `kubectl port-forward` and nothing
else. Every other way anybody reaches this controller is a name only the chart
knows: the Service's own DNS names inside the cluster, and whatever hostnames
the Ingress publishes. A name missing from the certificate is a browser
rejection for a name mismatch, which looks exactly like the untrusted-issuer
warning a self-signed certificate already produces, so it is the kind of
mistake that costs an afternoon to tell apart.

Empty when the controller is not self-provisioning, since the variable means
nothing in the other two modes.
*/}}
{{- define "the-pleiades.controller.autocertHosts" -}}
{{- $svc := include "the-pleiades.controller.fullname" . -}}
{{- $ns := .Release.Namespace -}}
{{- $names := list $svc (printf "%s.%s" $svc $ns) (printf "%s.%s.svc" $svc $ns) (printf "%s.%s.svc.%s" $svc $ns .Values.clusterDomain) -}}
{{- if .Values.ingress.enabled -}}
{{- range .Values.ingress.hosts -}}
{{- if .host -}}
{{- $names = append $names .host -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- range .Values.controller.tls.autocertHosts -}}
{{- $names = append $names . -}}
{{- end -}}
{{- join "," (uniq $names) -}}
{{- end }}
