// The Kubernetes object model this tool decodes, and the one written waiver
// that goes with it.
//
// Split out of checks.go so that the RULES and the SHAPES they read are two
// files: a change to what Kubernetes fields matter lands here, and a change to
// what is required of them lands there.
package main

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// manifest is one rendered Kubernetes object, decoded only as far as this tool
// needs to see.
//
// Fields absent from a given kind simply stay zero: a Service decodes into
// this struct with an empty pod template, and collectPods below skips it by
// kind rather than by guessing from emptiness.
type manifest struct {
	Kind     string       `yaml:"kind"`
	Metadata objectMeta   `yaml:"metadata"`
	Spec     manifestSpec `yaml:"spec"`
}

// objectMeta carries the metadata a finding needs to name an object, plus the
// annotations one rule reads.
type objectMeta struct {
	Name        string            `yaml:"name"`
	Annotations map[string]string `yaml:"annotations"`
}

// manifestSpec is every spec shape this tool reads, in one struct.
//
// One struct rather than one per kind, because YAML decoding leaves an absent
// key at its zero value: a Deployment fills Template and leaves Rules empty, an
// Ingress does the reverse, and each check selects by kind before reading the
// half it cares about.
type manifestSpec struct {
	Template podTemplate `yaml:"template"`

	// Rules are an Ingress's rules.
	Rules []ingressRule `yaml:"rules"`

	// MinAvailable and MaxUnavailable are a PodDisruptionBudget's two budget
	// fields.
	MinAvailable   *budgetField `yaml:"minAvailable"`
	MaxUnavailable *budgetField `yaml:"maxUnavailable"`

	// VolumeClaimTemplates are a StatefulSet's own claims, read only for the
	// annotation the retained-data refusal stamps on them.
	VolumeClaimTemplates []claimTemplate `yaml:"volumeClaimTemplates"`

	// ServiceName is a StatefulSet's governing Service, which is what its
	// pods' DNS records are published under. Read so the name rules can
	// check that record really fits, and that the Service it names was
	// rendered at all.
	ServiceName string `yaml:"serviceName"`

	// Replicas is how many pods the workload asks for, read only to know
	// which ordinals a StatefulSet will name its pods with. A pointer
	// because an absent field means one to Kubernetes, and this chart's
	// controller Deployment deliberately omits it under an autoscaler.
	Replicas *int `yaml:"replicas"`
}

// claimTemplate is one entry of a StatefulSet's volumeClaimTemplates.
//
// Only the metadata is decoded. The spec matters to Kubernetes and not to any
// rule here: what this tool checks is that the claim Kubernetes creates from
// this template will carry the credential stamp, and annotations are what it
// copies across.
type claimTemplate struct {
	Metadata objectMeta `yaml:"metadata"`
}

// budgetField is one PodDisruptionBudget budget value, decoded for its
// PRESENCE first and its text second.
//
// A type of its own because either field may be an integer (2) or a percentage
// string ("50%"), so no Go scalar decodes both, and because the question the
// check asks is whether the object carries the key at all. A pointer to this
// type answers that: nil when the key was absent, non-nil for any value the
// object stated, including 0, which the truthiness test this tool exists to
// replace read as absent.
type budgetField struct {
	// Text is the value as written, kept so a finding can quote it.
	Text string
}

// UnmarshalYAML records the scalar as text, whatever its YAML type.
func (b *budgetField) UnmarshalYAML(value *yaml.Node) error {
	b.Text = value.Value
	return nil
}

// ingressRule is one host's routing rule on an Ingress.
type ingressRule struct {
	Host string          `yaml:"host"`
	HTTP ingressHTTPRule `yaml:"http"`
}

// ingressHTTPRule is the HTTP half of a rule, which is the only half
// networking.k8s.io/v1 defines.
type ingressHTTPRule struct {
	Paths []ingressPath `yaml:"paths"`
}

// ingressPath is one path inside a rule.
//
// PathType is a plain string because its absence and its empty value mean the
// same thing to the API server: an Ingress it refuses.
type ingressPath struct {
	Path     string `yaml:"path"`
	PathType string `yaml:"pathType"`
}

// podTemplate is a workload's pod template.
type podTemplate struct {
	Metadata objectMeta `yaml:"metadata"`
	Spec     podSpec    `yaml:"spec"`
}

// podSpec holds the pod-level settings a container can inherit, plus the
// containers themselves.
type podSpec struct {
	SecurityContext *securityContext `yaml:"securityContext"`
	Containers      []container      `yaml:"containers"`
	InitContainers  []container      `yaml:"initContainers"`
	Volumes         []volume         `yaml:"volumes"`
}

// container is one container in a pod template.
type container struct {
	Name            string           `yaml:"name"`
	Image           string           `yaml:"image"`
	SecurityContext *securityContext `yaml:"securityContext"`
	LivenessProbe   *probe           `yaml:"livenessProbe"`
	ReadinessProbe  *probe           `yaml:"readinessProbe"`
	VolumeMounts    []volumeMount    `yaml:"volumeMounts"`
	Env             []envVar         `yaml:"env"`
}

// envVar is one environment variable a container is given, either as a
// literal or from a Secret key. Decoded so the setup profile can check that
// every Secret key the chart reads is one the Secret setup emits carries.
type envVar struct {
	Name      string        `yaml:"name"`
	Value     string        `yaml:"value"`
	ValueFrom *envVarSource `yaml:"valueFrom"`
}

// envVarSource is where a variable's value comes from.
type envVarSource struct {
	SecretKeyRef *secretKeyRef `yaml:"secretKeyRef"`
}

// secretKeyRef names one key of one Secret.
type secretKeyRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// volumeMount is one container's use of a pod volume, decoded far enough to
// check that the volume it names is really there.
type volumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
}

// securityContext is the subset of the Kubernetes security context this tool
// asserts on.
//
// Every boolean is a pointer, deliberately. An absent field and an explicit
// false decode to the same value otherwise, and those mean different things
// here: absent means nobody stated the setting, which is the failure this tool
// exists to catch, while false is a stated (and refused) intent.
type securityContext struct {
	RunAsNonRoot             *bool         `yaml:"runAsNonRoot"`
	RunAsUser                *int64        `yaml:"runAsUser"`
	ReadOnlyRootFilesystem   *bool         `yaml:"readOnlyRootFilesystem"`
	AllowPrivilegeEscalation *bool         `yaml:"allowPrivilegeEscalation"`
	Privileged               *bool         `yaml:"privileged"`
	Capabilities             *capabilities `yaml:"capabilities"`
	SeccompProfile           *seccomp      `yaml:"seccompProfile"`
}

// capabilities is the Linux capability set a container asks for.
type capabilities struct {
	Drop []string `yaml:"drop"`
	Add  []string `yaml:"add"`
}

// seccomp names which seccomp profile the container runs under.
type seccomp struct {
	Type string `yaml:"type"`
}

// probe is one liveness or readiness probe, decoded far enough to tell two
// probes apart.
type probe struct {
	HTTPGet   *httpGetAction `yaml:"httpGet"`
	TCPSocket *tcpAction     `yaml:"tcpSocket"`
	Exec      *execAction    `yaml:"exec"`
}

// httpGetAction is an HTTP probe target.
type httpGetAction struct {
	Path   string `yaml:"path"`
	Scheme string `yaml:"scheme"`
}

// tcpAction is a TCP probe target. The port is not read: this tool only needs
// to know the probe exists and is not an HTTP probe sharing a path.
type tcpAction struct{}

// execAction is a command probe.
type execAction struct {
	Command []string `yaml:"command"`
}

// volume is one pod volume, decoded only far enough to spot a host mount.
type volume struct {
	Name     string      `yaml:"name"`
	HostPath *hostPathes `yaml:"hostPath"`
}

// hostPathes is a mount of a path from the node itself.
type hostPathes struct {
	Path string `yaml:"path"`
}

// probeWaiver is one container allowed to ship without probes: the written
// reason, and the fact that reason depends on.
//
// CARRYING THE DEPENDENCY IS THE POINT. A waiver is a claim about the world
// ("there is nothing in this image to probe"), and the world changes. Written
// on its own, the claim goes stale silently: the day somebody gives the binary
// a healthcheck subcommand, the chart keeps shipping no probes and every test
// in the repository stays green, because nothing connects the waiver to the
// thing that made it true. So the waiver names a condition this tool can check
// on every run, and fails when it stops holding.
type probeWaiver struct {
	// reason is why this container ships without probes, written out, in the
	// same shape gosec-waivers.json requires of every accepted finding: no
	// blanket exemption, no exemption by kind, and no exemption without a
	// sentence saying why.
	reason string

	// sourceDir is the package whose contents the reason depends on,
	// relative to the repository root.
	sourceDir string

	// endedBy is the literal whose ARRIVAL in that package ends the waiver.
	// It is matched as text against non-test .go files, which is enough for
	// what it has to decide: whether a subcommand by that name now exists.
	// A false positive here costs a build failure that names the chart edit
	// somebody was about to make anyway.
	endedBy string

	// remedy is what to do when the waiver ends, written now, because the
	// person who lands that code will not be the person who read this file.
	remedy string
}

// probeWaivers names every container allowed to ship without probes.
//
// The map is keyed on container name because that is what a reader of the
// rendered manifest sees. IT IS EMPTY, and that is the state this mechanism
// was built to reach rather than a sign it is unused: every container in
// every rendered configuration now carries both probes, and a new one
// arriving without them fails checkProbes rather than quietly joining an
// allowlist.
//
// It had exactly one entry for its whole life, the runner, whose written
// reason was that cmd/runner had no healthcheck subcommand and nothing else
// in its distroless image could answer a probe. That reason ENDED rather than
// being deleted: Phase 20 gave the binary a `runner healthcheck` subcommand
// reading a liveness heartbeat the agent writes only while its durable NATS
// consumer is genuinely reachable (FAILURE_PATTERNS.md #119, now fixed), and
// templates/runner-deployment.yaml wires both probes to it. The waiver named
// that literal as its expiry condition and checkProbeWaiversStillHold would
// have failed `make ci` on the day it landed, which is precisely what the
// mechanism is for.
//
// The type, the check and this table are all kept. A waiver mechanism with no
// current entries costs one empty map and is the only thing standing between
// "a container needs a written reason to ship unprobed" and "somebody deletes
// a probe".
var probeWaivers = map[string]probeWaiver{}

// finding is one violated invariant, located precisely enough to fix without
// re-reading the render.
type finding struct {
	profile   string
	object    string
	container string
	message   string
}

// String renders a finding as one line.
func (f finding) String() string {
	if f.container == "" {
		return fmt.Sprintf("[%s] %s: %s", f.profile, f.object, f.message)
	}
	return fmt.Sprintf("[%s] %s/%s: %s", f.profile, f.object, f.container, f.message)
}

// workloadKinds is every kind this tool reaches into for pod templates.
// Anything else in the render (Service, Secret, Ingress, PVC) carries no
// containers and is skipped.
var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"Job":         true,
	"CronJob":     true,
}
