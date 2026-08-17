// The invariants every rendered container has to satisfy.
//
// These are pure functions over already-parsed manifests: nothing here runs
// helm, reads a file, or touches the network, which is what makes them
// testable without a chart and without a cluster (checks_test.go does exactly
// that). objects.go holds the shapes they read, profiles.go holds the
// configurations they are run over, and main.go does the rendering.
package main

import (
	"fmt"
	"strings"
)

// checkManifests runs every invariant over one profile's rendered objects.
//
// wantScheme is the probe scheme the controller's HTTP probes must carry for
// this profile ("HTTPS" when the controller terminates TLS itself, "HTTP" when
// an ingress terminated it upstream). It is checked because getting it wrong
// is invisible in a diff and total at run time: a kubelet probing http:// at an
// HTTPS listener never gets a valid response, so every controller pod would
// fail its startup probe forever.
func checkManifests(profile string, objects []manifest, wantScheme string) []finding {
	var findings []finding
	sawController := false

	for _, obj := range objects {
		if !workloadKinds[obj.Kind] {
			continue
		}
		name := fmt.Sprintf("%s/%s", obj.Kind, obj.Metadata.Name)
		pod := obj.Spec.Template.Spec

		for _, vol := range pod.Volumes {
			if vol.HostPath != nil {
				findings = append(findings, finding{
					profile: profile, object: name,
					message: fmt.Sprintf("volume %q mounts hostPath %q. A path from the node is how a pod reaches the container runtime socket, which is the escape this chart refuses by design.", vol.Name, vol.HostPath.Path),
				})
			}
		}

		findings = append(findings, checkVolumeMounts(profile, name, pod, obj.Spec.VolumeClaimTemplates)...)

		all := append(append([]container{}, pod.InitContainers...), pod.Containers...)
		if len(all) == 0 {
			findings = append(findings, finding{
				profile: profile, object: name,
				message: "workload has no containers, which means this tool parsed it wrongly and is checking nothing",
			})
			continue
		}

		for _, c := range all {
			findings = append(findings, checkImage(profile, name, c)...)
			findings = append(findings, checkProbes(profile, name, c)...)
			findings = append(findings, checkSecurity(profile, name, c, pod.SecurityContext)...)

			if strings.HasSuffix(obj.Metadata.Name, "-controller") && c.Name == "controller" {
				sawController = true
				findings = append(findings, checkProbeScheme(profile, name, c, wantScheme)...)
			}
		}
	}

	// A render that contained no controller at all would pass every check
	// above by having nothing to check, which is the failure mode a linter is
	// most likely to develop silently.
	if !sawController {
		findings = append(findings, finding{
			profile: profile, object: "(render)",
			message: "no controller container was found in this profile's output, so the checks above proved nothing",
		})
	}
	return findings
}

// checkImage refuses the three image references that have already broken this
// chart or its images once each.
func checkImage(profile, object string, c container) []finding {
	var findings []finding
	add := func(msg string) {
		findings = append(findings, finding{profile: profile, object: object, container: c.Name, message: msg})
	}

	if strings.Contains(c.Image, "@") {
		add(fmt.Sprintf("image %q is pinned by digest. A digest-pinned reference does not resolve against a side-loaded image even when the local daemon reports that exact digest, which breaks every air-gapped and kind-based install.", c.Image))
		return findings
	}

	repo, tag, ok := splitImage(c.Image)
	if !ok || repo == "" {
		add(fmt.Sprintf("image %q has no tag. An untagged reference means :latest to every runtime that resolves it.", c.Image))
		return findings
	}
	if tag == "latest" {
		add(fmt.Sprintf("image %q is tagged latest, which names a different image tomorrow than it does today and makes a rollback unrepeatable.", c.Image))
	}
	return findings
}

// splitImage separates an image reference into its repository and tag.
//
// The colon that separates a tag is the last one AFTER the last slash, because
// a registry may carry a port: in "registry.example.com:5000/pleiades/runner",
// the first colon belongs to the host and the reference has no tag at all.
func splitImage(ref string) (repository, tag string, ok bool) {
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon <= lastSlash {
		return ref, "", false
	}
	return ref[:lastColon], ref[lastColon+1:], true
}

// checkProbes requires both probes on every container, and refuses to let one
// endpoint answer both questions.
func checkProbes(profile, object string, c container) []finding {
	var findings []finding
	add := func(msg string) {
		findings = append(findings, finding{profile: profile, object: object, container: c.Name, message: msg})
	}

	if waiver, waived := probeWaivers[c.Name]; waived {
		if c.LivenessProbe != nil || c.ReadinessProbe != nil {
			add("container is in probeWaivers but now has a probe. Delete its waiver entry rather than keeping an exemption that no longer describes it. Recorded reason: " + waiver.reason)
		}
		return findings
	}

	if c.LivenessProbe == nil {
		add("no livenessProbe. Without one, a process that is running but wedged is never restarted, and nothing in the cluster can tell it apart from a healthy one.")
	}
	if c.ReadinessProbe == nil {
		add("no readinessProbe. Without one, the Service sends traffic to a pod the moment its process starts, including during a schema migration that has not opened a listener yet.")
	}
	if c.LivenessProbe == nil || c.ReadinessProbe == nil {
		return findings
	}

	live, ready := c.LivenessProbe.HTTPGet, c.ReadinessProbe.HTTPGet
	if live != nil && ready != nil && live.Path == ready.Path {
		add(fmt.Sprintf("livenessProbe and readinessProbe both target %q. Sharing one path turns a readiness failure into a pod kill: a dependency blip then restarts every replica at once, exactly when that dependency is already struggling.", live.Path))
	}
	return findings
}

// checkProbeScheme asserts the controller's HTTP probes speak the scheme the
// controller is actually serving in this profile.
func checkProbeScheme(profile, object string, c container, want string) []finding {
	var findings []finding
	for name, p := range map[string]*probe{"livenessProbe": c.LivenessProbe, "readinessProbe": c.ReadinessProbe} {
		if p == nil || p.HTTPGet == nil {
			continue
		}
		if p.HTTPGet.Scheme != want {
			findings = append(findings, finding{
				profile: profile, object: object, container: c.Name,
				message: fmt.Sprintf("%s uses scheme %q, but this profile serves %s. A kubelet probing the wrong scheme never gets a valid response, so the pod fails its probes permanently.", name, p.HTTPGet.Scheme, want),
			})
		}
	}
	return findings
}

// checkSecurity asserts the whole hardened baseline on one container,
// resolving each setting the way the kubelet does: a container-level value
// wins, a pod-level value applies when the container states none, and nothing
// at either level is a finding.
func checkSecurity(profile, object string, c container, pod *securityContext) []finding {
	var findings []finding
	add := func(msg string) {
		findings = append(findings, finding{profile: profile, object: object, container: c.Name, message: msg})
	}

	if boolAt(c.SecurityContext, pod, func(s *securityContext) *bool { return s.Privileged }) {
		add("runs privileged. Nothing in this chart needs it, and a privileged container is the node.")
	}

	nonRoot := lookupBool(c.SecurityContext, pod, func(s *securityContext) *bool { return s.RunAsNonRoot })
	if nonRoot == nil || !*nonRoot {
		add("runAsNonRoot is not true at either the container or the pod level. Without it, an image whose USER is unset runs as root.")
	}

	uid := lookupUser(c.SecurityContext, pod)
	switch {
	case uid == nil:
		add("runAsUser is set at neither level. It has to be NUMERIC and stated here: the kubelet cannot verify a non-numeric image USER, and a pod with runAsNonRoot against such an image fails with CreateContainerConfigError.")
	case *uid == 0:
		add("runAsUser is 0.")
	}

	if ro := lookupBool(c.SecurityContext, nil, func(s *securityContext) *bool { return s.ReadOnlyRootFilesystem }); ro == nil || !*ro {
		add("readOnlyRootFilesystem is not true. It is a container-level field with no pod-level counterpart, so it has to be stated on every container.")
	}

	if esc := lookupBool(c.SecurityContext, nil, func(s *securityContext) *bool { return s.AllowPrivilegeEscalation }); esc == nil || *esc {
		add("allowPrivilegeEscalation is not false. Without it a setuid binary inside the image can gain privileges the pod never asked for.")
	}

	if c.SecurityContext == nil || c.SecurityContext.Capabilities == nil || !contains(c.SecurityContext.Capabilities.Drop, "ALL") {
		add("does not drop ALL capabilities.")
	} else if len(c.SecurityContext.Capabilities.Add) > 0 {
		add(fmt.Sprintf("adds capabilities back: %s. Nothing in this chart needs one; a container that binds a port below 1024 should move the port instead.", strings.Join(c.SecurityContext.Capabilities.Add, ", ")))
	}

	profileType := ""
	if c.SecurityContext != nil && c.SecurityContext.SeccompProfile != nil {
		profileType = c.SecurityContext.SeccompProfile.Type
	} else if pod != nil && pod.SeccompProfile != nil {
		profileType = pod.SeccompProfile.Type
	}
	if profileType != "RuntimeDefault" {
		add(fmt.Sprintf("seccompProfile is %q, not RuntimeDefault. Unconfined leaves the whole syscall surface reachable.", profileType))
	}

	return findings
}

// lookupBool resolves one boolean setting from the container's context, then
// the pod's. A nil pod context means the field has no pod-level counterpart
// and only the container is consulted.
func lookupBool(container, pod *securityContext, field func(*securityContext) *bool) *bool {
	if container != nil {
		if v := field(container); v != nil {
			return v
		}
	}
	if pod != nil {
		if v := field(pod); v != nil {
			return v
		}
	}
	return nil
}

// boolAt is lookupBool for a setting whose absence means false, which is only
// true of the ones Kubernetes itself defaults to false.
func boolAt(container, pod *securityContext, field func(*securityContext) *bool) bool {
	v := lookupBool(container, pod, field)
	return v != nil && *v
}

// lookupUser resolves runAsUser the same way, kept separate because it is an
// integer rather than a boolean.
func lookupUser(container, pod *securityContext) *int64 {
	if container != nil && container.RunAsUser != nil {
		return container.RunAsUser
	}
	if pod != nil && pod.RunAsUser != nil {
		return pod.RunAsUser
	}
	return nil
}

// contains reports whether a string slice holds an exact value.
func contains(haystack []string, needle string) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}

// checkVolumeMounts requires every volumeMount to name a volume the pod
// actually declares.
//
// This is the one class of chart defect that renders perfectly and then does
// not run. A volumeMount naming a volume that is not there is valid YAML and
// a well formed object, so `helm template` prints it and `helm lint` accepts
// it; the API server is what refuses it, which means the first thing that
// notices is an install. Nothing before this looked, and a mount and its
// volume are usually written far enough apart in a template that renaming one
// and not the other is an ordinary mistake rather than a careless one.
//
// It matters more in this chart than in most, because several mounts here are
// conditional. Two independent {{- if }} blocks, one around the mount and one
// around the volume, are one edit away from disagreeing about when the volume
// exists, and the arrangement where they disagree may not be the arrangement
// anybody renders by hand.
//
// The check runs the direction that fails: an unused volume is wasteful and
// legal, while a mount with no volume cannot start.
//
// claims are a StatefulSet's volumeClaimTemplates, which are the second legal
// way to declare a volume and are named in a different part of the object
// entirely. Leaving them out is not a small omission: the first run of this
// check reported both database StatefulSets as broken, because each mounts a
// claim template rather than a pod volume. A checker that is wrong about
// correct charts gets switched off.
func checkVolumeMounts(profile, object string, pod podSpec, claims []claimTemplate) []finding {
	declared := make(map[string]bool, len(pod.Volumes)+len(claims))
	for _, vol := range pod.Volumes {
		declared[vol.Name] = true
	}
	for _, claim := range claims {
		declared[claim.Metadata.Name] = true
	}

	var findings []finding
	for _, c := range append(append([]container{}, pod.InitContainers...), pod.Containers...) {
		for _, mount := range c.VolumeMounts {
			if declared[mount.Name] {
				continue
			}
			findings = append(findings, finding{
				profile: profile, object: object, container: c.Name,
				message: fmt.Sprintf("volumeMount %q at %q names a volume this pod does not declare. This renders and lints cleanly and is rejected by the API server, so the first thing to notice would be an install.", mount.Name, mount.MountPath),
			})
		}
	}
	return findings
}
