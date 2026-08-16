// The invariants that need more than one render, or that read a kind other
// than a workload: object names at every legal release-name length, Ingress
// paths, PodDisruptionBudgets, and the stamp the retained-data refusal reads
// back.
//
// WHY THIS IS ITS OWN FILE AND ITS OWN RENDER PASS. Every other rule in this
// tool reads one render of one values profile. These rules are about what the
// naming helpers do at the EDGES of a release name, and the edge is where they
// have been wrong twice.
//
// The first time, a release name of 49 to 53 characters, every one of them
// legal to Helm (whose own limit is 53), made templates/_helpers.tpl truncate
// the controller, the runner, the PostgreSQL StatefulSet and the NATS
// StatefulSet to the same string. Four different workloads, one name. The API
// server accepts that, because each object is a different KIND until two of
// them are not, and `helm install` then applies a Deployment named X twice:
// the runner silently replaced the controller.
//
// The second time was the FIX for the first, which gave every kind the same
// 63-character budget. That is right for a Service and 11 characters too
// generous for a StatefulSet, whose pods carry a label built from its name
// (see revisionHashReserve). A release name of 40 characters was enough:
// `helm install` reported success, every object was created, and both
// StatefulSets produced zero pods for as long as the release existed. Proven
// by really installing the pre-fix chart into a real cluster, not derived.
//
// The lesson those two share is what this file is shaped by: a rule that reads
// only the names the chart WRITES misses everything Kubernetes DERIVES from
// them, and a rule that reads only Deployment names misses the kind with the
// tightest limit. So this file renders the chart at a set of release-name
// lengths chosen to straddle both boundaries, and asserts on the derived names
// as well as the written ones.
package main

import (
	"fmt"
	"sort"
	"strings"
)

// helmMaxReleaseName is Helm's own limit on a release name, and therefore the
// longest name this chart can ever be installed under.
//
// Helm refuses anything longer itself, so a length past this proves nothing
// about the chart. Everything up to it has to work.
const helmMaxReleaseName = 53

// dnsLabelMax is the length limit of one DNS label, and therefore of any
// Kubernetes name or label value that has to be one.
const dnsLabelMax = 63

// dnsSubdomainMax is the limit every other object name is held to.
//
// Most Kubernetes names are DNS subdomains, where 253 is the ceiling. Holding
// a PersistentVolumeClaim to 63 would be this tool inventing a rule Kubernetes
// does not have, which is its own kind of wrong: a linter that fails a legal
// configuration teaches people to work around the linter.
const dnsSubdomainMax = 253

// revisionHashReserve is what a StatefulSet's name loses to the revision hash
// Kubernetes appends to it, and it is why a StatefulSet's ceiling is lower
// than a Deployment's.
//
// The StatefulSet controller labels every pod it creates with
// controller-revision-hash, whose value is "<statefulset name>-<hash>". A
// label value may not exceed 63 bytes, and the API server refuses a pod that
// breaks that, so a StatefulSet whose name is too long is CREATED and then
// never produces a single pod. Nothing fails at install time: the object
// exists, reports 0 ready forever, and the only evidence is a FailedCreate
// event.
//
// Measured against a real cluster rather than reasoned about. A 54-character
// StatefulSet name produced:
//
//	Pod "<name>-0" is invalid: metadata.labels: Invalid value:
//	"<name>-5f6f5d6649": must be no more than 63 bytes
//
// One dash plus 10 characters of hash. The hash is a uint32 printed in decimal
// and re-encoded character for character, so its length is 1 to 10 and changes
// with the pod template: a 53-character name drew a 9-character hash in the
// same experiment and worked. Reserving the maximum is what stops that being a
// coin flip.
const revisionHashReserve = 11

// statefulSetOrdinalReserve is what a StatefulSet's name loses to the ordinal
// on its pods, which are named "<statefulset name>-<ordinal>".
//
// That string has to be a DNS label twice over: it becomes the pod's
// spec.hostname, which the API server validates as one, and the
// statefulset.kubernetes.io/pod-name label value, which is capped at 63 bytes
// like any other. Two digits of ordinal covers a hundred replicas, which is
// more than this chart can ask for (both StatefulSets are pinned at one) and
// is the number a future replica count would be measured against.
//
// It is looser than revisionHashReserve, so it never decides the ceiling. It
// is kept and asserted separately because it is the limit people expect to be
// binding, and a check that quietly folded it into the other one would stop
// saying which limit a failure broke.
const statefulSetOrdinalReserve = 3

// deploymentPodNameBase is how much of a Deployment's pod name survives
// generation, and it is not a limit so much as a truncation point.
//
// A Deployment's pods are named by Kubernetes' own generator, which cuts the
// base ("<replicaset name>-") to 58 characters and appends 5 random ones. So a
// Deployment of ANY legal length still produces pods, which is why this kind
// has no 63-character ceiling: names up to the 253-character subdomain limit
// work, verified on a real cluster at 64, 100, 200 and 240 characters, each of
// which produced a running pod named exactly 63 characters.
//
// What it costs is readability. Past this point the component word is cut out
// of the pod name, so `kubectl get pods` shows two workloads whose names agree
// as far as the eye can follow. checkDeploymentPodNames asserts the two stay
// distinguishable rather than asserting a length.
const deploymentPodNameBase = 58

// nameLimits is the longest name each kind may carry, with the reason.
//
// PER KIND, not one number for all of them, and that is the fix this table
// is: holding every kind to 63 is right for a Service, harmless for a
// Deployment, and 11 characters too generous for a StatefulSet. A rule that
// read Deployment names alone passed the entire time the two StatefulSets were
// over their real limit.
//
// A kind absent from this table is a DNS subdomain, which is 253.
var nameLimits = map[string]int{
	// The name IS a DNS label in service discovery and the API server
	// rejects a longer one outright. Verified: a 64-character Service is
	// refused with "must be no more than 63 characters", a 63-character one
	// is accepted.
	"Service": dnsLabelMax,

	// Not the StatefulSet's own name limit, which is 253 like any other
	// subdomain, but the limit past which it stops producing pods. See
	// revisionHashReserve.
	"StatefulSet": dnsLabelMax - revisionHashReserve,
}

// limitFor reports the longest name a kind may carry, falling back to the DNS
// subdomain limit for any kind nameLimits does not hold to a tighter one.
func limitFor(kind string) int {
	if limit, ok := nameLimits[kind]; ok {
		return limit
	}
	return dnsSubdomainMax
}

// mandatoryWorkloadSuffixes are the workloads every render must contain.
//
// The controller and the runner are the product. PostgreSQL and NATS are
// optional (externalDatabase and externalNats replace them), so requiring all
// four would fail the profile that points at an external database rather than
// catching anything.
var mandatoryWorkloadSuffixes = []string{"-controller", "-runner"}

// nameProbeLengths are the release-name lengths every render below is
// repeated at.
//
// Chosen, not sampled, and each one is a boundary something crossed. Both
// release-name forms are rendered at every length (releaseNamesOfLength), and
// they cross these boundaries at different points, which is why some lengths
// below name one form.
//
//	1, 20   ordinary names, where nothing is truncated at all.
//	30, 31  the StatefulSet ceiling under the old single 63-character budget,
//	        for the release-name form that does not already contain the chart
//	        name and so has "-the-pleiades" appended first. 30 was the last
//	        length whose PostgreSQL StatefulSet still fit; at 31 it went one
//	        character over and the StatefulSet stopped producing pods.
//	40      well past that, and the length at which the defect was first
//	        reproduced end to end against a real cluster.
//	44      where the OTHER release-name form crosses the same ceiling, so
//	        neither form can hide a regression the other would catch.
//	48, 49  48 is the last length that worked before the truncation ORDER was
//	        fixed and 49 is the first that did not, so a regression that
//	        re-introduces it fails on 49 while still passing 48, which is
//	        exactly the shape that made the original defect survive review.
//	50, 52  the top of the Deployment and Service budget, where the
//	        controller name reaches 63 characters exactly.
//	53      Helm's own maximum release name.
var nameProbeLengths = []int{1, 20, 30, 31, 40, 44, 48, 49, 50, 52, helmMaxReleaseName}

// workloadNameSuffixes are the four component suffixes the chart appends, in
// the order a reader would list the workloads.
//
// They are what makes two names different once the prefix has been truncated,
// so a render where two of these four are missing from the names is the defect
// this file exists to catch.
var workloadNameSuffixes = []string{"-controller", "-runner", "-postgres", "-nats"}

// releaseNamesOfLength builds the release names to render at one length.
//
// TWO of them, because the fullname helper has two branches and they truncate
// differently. A release name that already contains the chart name is used
// as-is; one that does not gets "-the-pleiades" appended first, which is the
// branch that overflowed. Testing only the first would have missed the bug
// entirely.
//
// Both are valid DNS-1123 names (lowercase alphanumeric and dashes, starting
// and ending alphanumeric), because Helm refuses anything else and this tool
// should be testing the chart rather than Helm's own validation.
func releaseNamesOfLength(length int) []string {
	plain := "r" + strings.Repeat("a", length-1)

	names := []string{plain}
	// "the-pleiades" is 12 characters, so the second form only exists once
	// there is room for it plus at least one more character.
	const chartName = "the-pleiades"
	if length > len(chartName) {
		names = append(names, chartName+"-"+strings.Repeat("b", length-len(chartName)-1))
	}
	return names
}

// checkNames asserts everything that has to be true of one render's object
// names.
//
// Four separate claims, each of which has its own failure shape:
//
//  1. No two objects of the same kind share a name. This is the collision
//     itself, stated in the terms the API server would see it in.
//  2. The four workload names are pairwise distinct AND each still carries the
//     component word. A name that lost "-controller" to truncation is a name
//     nobody can read, even in the releases where it does not collide.
//  3. Every name fits the limit ITS OWN kind is held to (nameLimits). Checking
//     every kind against 63 would fail a legal PersistentVolumeClaim, and a
//     linter that fails legal configurations teaches people to work around the
//     linter. Checking every kind against 63 is also too LOOSE for the two
//     StatefulSets, which is the defect this table exists for.
//  4. The names Kubernetes DERIVES from these fit too: a StatefulSet's pod
//     names, their revision-hash label, and their DNS records; and a
//     Deployment's pods stay distinguishable from each other. An object name
//     that is legal on its own and produces an illegal pod is the worst shape
//     available, because the install succeeds.
func checkNames(profile string, objects []manifest) []finding {
	var findings []finding
	add := func(object, msg string) {
		findings = append(findings, finding{profile: profile, object: object, message: msg})
	}

	// The kind+name pairs already seen, so a duplicate can name the object it
	// duplicates rather than merely reporting a count.
	seen := map[string]bool{}
	for _, obj := range objects {
		key := obj.Kind + "/" + obj.Metadata.Name
		if seen[key] {
			add(key, "two objects in one release carry this exact kind and name. The API server keeps whichever is applied last, so one of these workloads silently replaces the other.")
		}
		seen[key] = true

		if limit := limitFor(obj.Kind); len(obj.Metadata.Name) > limit {
			add(key, fmt.Sprintf("the name is %d characters, past the %d this kind is held to. A name that only fails on apply is a name that fails in front of the operator.", len(obj.Metadata.Name), limit))
		}
	}

	findings = append(findings, checkWorkloadNamesDistinct(profile, objects)...)
	findings = append(findings, checkStatefulSetPodNames(profile, objects)...)
	findings = append(findings, checkDeploymentPodNames(profile, objects)...)
	return findings
}

// worstCaseNamespace is the longest namespace a release could be installed
// into, used when checking a fully qualified pod DNS name.
//
// The worst case rather than the namespace the render happened to use, because
// the namespace is the operator's choice and `helm template` defaults it to
// "default". A check that measured seven characters would pass here and fail
// for somebody who installed into a real one.
const worstCaseNamespace = dnsLabelMax

// worstCaseClusterDomain is the cluster DNS domain the FQDN check assumes.
//
// "cluster.local" is this chart's own default. An operator with a different
// one sets clusterDomain, and this tool renders with the default, so what the
// FQDN check below is really doing is stating a BOUND rather than catching a
// live risk: with every label already held to 63 by the checks above, the
// longest fully qualified pod name this chart can produce is
//
//	63 pod + 1 + 63 service + 1 + 63 namespace + 1 + "svc" + 1 + 13 domain = 209
//
// which leaves 44 characters of headroom on the 253 a DNS name may be. Two
// things would spend it: a much longer clusterDomain, which is a value this
// tool cannot see, and a future object whose name is built from two of these
// rather than one. The check is kept, and TestCheckStatefulSetPodNames proves
// it fires, because an assertion whose margin is stated is worth more than an
// arithmetic claim in a comment that nothing re-checks.
const worstCaseClusterDomain = "cluster.local"

// checkStatefulSetPodNames asserts that the pods a StatefulSet will create can
// actually be created, and that their DNS records fit.
//
// THIS IS THE RULE THE OLD ONE DID NOT HAVE. A StatefulSet's own name is a DNS
// subdomain, so the API server accepts a long one happily; what breaks is
// every pod it then tries to create. The object exists, `helm install` reports
// success, and the StatefulSet sits at 0 ready with a FailedCreate event as
// the only evidence. Asserting the OBJECT name and stopping there is exactly
// how that reaches a cluster.
//
// Four derived names are checked, in the order they would fail:
//
//  1. the controller-revision-hash label, "<name>-<up to 10 characters>",
//     which is what actually binds;
//  2. the pod name, "<name>-<ordinal>", which is also the pod's spec.hostname
//     and its statefulset.kubernetes.io/pod-name label;
//  3. that pod name as one DNS label in the record published under the
//     governing Service;
//  4. the whole fully qualified name, "<pod>.<service>.<namespace>.svc.<domain>".
func checkStatefulSetPodNames(profile string, objects []manifest) []finding {
	var findings []finding
	for _, obj := range objects {
		if obj.Kind != "StatefulSet" {
			continue
		}
		name := obj.Metadata.Name
		object := obj.Kind + "/" + name
		add := func(msg string) {
			findings = append(findings, finding{profile: profile, object: object, message: msg})
		}

		if got := len(name) + revisionHashReserve; got > dnsLabelMax {
			add(fmt.Sprintf("the controller-revision-hash label on this StatefulSet's pods is %q plus up to %d characters of hash, which is %d bytes and past the %d a label value may carry. The API server refuses every pod, so the StatefulSet installs cleanly and stays at zero replicas forever.", name, revisionHashReserve-1, got, dnsLabelMax))
		}

		// The highest ordinal this StatefulSet will name a pod with. An
		// absent replicas field means one to Kubernetes, so the highest
		// ordinal is zero.
		highest := 0
		if obj.Spec.Replicas != nil && *obj.Spec.Replicas > 1 {
			highest = *obj.Spec.Replicas - 1
		}
		pod := fmt.Sprintf("%s-%d", name, highest)
		if len(pod) > dnsLabelMax {
			add(fmt.Sprintf("its highest-ordinal pod would be named %q, which is %d characters. That string is the pod's spec.hostname, which the API server validates as a DNS label, and its statefulset.kubernetes.io/pod-name label value, so the pod is refused.", pod, len(pod)))
		}
		// Asserted against the reserve as well as against the ordinal this
		// release happens to use, so raising replicas later cannot quietly
		// walk past a limit nothing re-checks.
		if len(name)+statefulSetOrdinalReserve > dnsLabelMax {
			add(fmt.Sprintf("the name is %d characters, which leaves no room for the %d this chart reserves for a pod ordinal. It fits today only because there is one replica.", len(name), statefulSetOrdinalReserve))
		}

		if obj.Spec.ServiceName == "" {
			add("has no spec.serviceName, so its pods get no DNS records at all and the connection strings this chart builds resolve nothing.")
			continue
		}
		if len(obj.Spec.ServiceName) > dnsLabelMax {
			add(fmt.Sprintf("its governing Service is named %q, which is %d characters. That name becomes each pod's spec.subdomain, which the API server validates as a DNS label.", obj.Spec.ServiceName, len(obj.Spec.ServiceName)))
		}
		fqdn := fmt.Sprintf("%s.%s.%s.svc.%s", pod, obj.Spec.ServiceName, strings.Repeat("n", worstCaseNamespace), worstCaseClusterDomain)
		if len(fqdn) > dnsSubdomainMax {
			add(fmt.Sprintf("the fully qualified DNS name of its highest-ordinal pod is %d characters in the longest namespace a cluster allows, past the %d a DNS name may be.", len(fqdn), dnsSubdomainMax))
		}
	}
	return findings
}

// checkDeploymentPodNames asserts that two Deployments in one release still
// produce pods a human can tell apart.
//
// A Deployment has no 63-character ceiling: Kubernetes' own name generator
// truncates the base ("<replicaset name>-") at deploymentPodNameBase and
// appends 5 random characters, so a Deployment of any legal length produces
// pods. Verified on a real cluster at 64, 100, 200 and 240 characters, each of
// which produced a running pod named exactly 63 characters.
//
// What truncation costs is the component word. Past the cut, the controller's
// pods and the runner's pods share every character a `kubectl get pods` line
// shows before the random suffix, and picking the wedged one out of the list
// stops being possible. That is not a Kubernetes error, so nothing but a rule
// like this one would ever report it.
func checkDeploymentPodNames(profile string, objects []manifest) []finding {
	// The surviving base of each Deployment's pod names, and which
	// Deployments produced it.
	bases := map[string][]string{}
	for _, obj := range objects {
		if obj.Kind != "Deployment" {
			continue
		}
		// The generator's input is the ReplicaSet name plus a dash, and the
		// ReplicaSet name is the Deployment name plus a dash and its own
		// hash. Only the leading part matters here, so the base measured is
		// the Deployment name plus the dash that always follows it.
		base := obj.Metadata.Name + "-"
		if len(base) > deploymentPodNameBase {
			base = base[:deploymentPodNameBase]
		}
		bases[base] = append(bases[base], obj.Metadata.Name)
	}

	var findings []finding
	for base, owners := range bases {
		if len(owners) < 2 {
			continue
		}
		sort.Strings(owners)
		findings = append(findings, finding{
			profile: profile, object: "(render)",
			message: fmt.Sprintf("these Deployments produce pod names that are identical up to their random suffix (%q): %s. Kubernetes cuts a generated pod name's base at %d characters, so the component word is gone from every pod in `kubectl get pods` and the two workloads cannot be told apart there.", base, strings.Join(owners, ", "), deploymentPodNameBase),
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].message < findings[j].message })
	return findings
}

// checkWorkloadNamesDistinct proves the four workloads still have four names.
//
// It works from the suffixes rather than from a list of expected names,
// because the prefix is exactly the part that varies with the release name and
// hard-coding it would make this a test of this tool's own arithmetic.
func checkWorkloadNamesDistinct(profile string, objects []manifest) []finding {
	var findings []finding

	// Every workload name in the render, deduplicated: a Deployment and its
	// Service and its PodDisruptionBudget legitimately share one name.
	names := map[string]bool{}
	for _, obj := range objects {
		if workloadKinds[obj.Kind] {
			names[obj.Metadata.Name] = true
		}
	}

	// Which suffix each workload name ends with. A name matching none of them
	// is a name that lost its component word to truncation.
	byComponent := map[string][]string{}
	var unlabeled []string
	for name := range names {
		matched := ""
		for _, suffix := range workloadNameSuffixes {
			if strings.HasSuffix(name, suffix) {
				matched = suffix
				break
			}
		}
		if matched == "" {
			unlabeled = append(unlabeled, name)
			continue
		}
		byComponent[matched] = append(byComponent[matched], name)
	}

	sort.Strings(unlabeled)
	for _, name := range unlabeled {
		findings = append(findings, finding{
			profile: profile, object: name,
			message: fmt.Sprintf("this workload name ends with none of %s, so the component was truncated away and the name no longer says which workload it is.", strings.Join(workloadNameSuffixes, ", ")),
		})
	}

	for _, suffix := range workloadNameSuffixes {
		matches := byComponent[suffix]
		if len(matches) == 0 {
			// Only the workloads that are always deployed are required to be
			// here. A profile pointing at an external database deliberately
			// renders no PostgreSQL, and a name truncated past its component
			// is reported by the unlabeled rule above rather than by silence
			// here.
			if contains(mandatoryWorkloadSuffixes, suffix) {
				findings = append(findings, finding{
					profile: profile, object: "(render)",
					message: fmt.Sprintf("no workload name ends with %q. Either that workload did not render, or its name was truncated past the component.", suffix),
				})
			}
			continue
		}
		if len(matches) > 1 {
			sort.Strings(matches)
			findings = append(findings, finding{
				profile: profile, object: "(render)",
				message: fmt.Sprintf("more than one distinct workload name ends with %q: %s", suffix, strings.Join(matches, ", ")),
			})
		}
	}
	return findings
}

// checkIngressPaths requires a pathType on every path of every rendered
// Ingress.
//
// networking.k8s.io/v1 makes the field REQUIRED, so an Ingress without one is
// a manifest `helm template` prints happily and the API server rejects. The
// chart emitted it only `with` a value and its schema asked only for `path`,
// so the documented default entry produced exactly that object. Rendering is
// the only place this can be caught before an operator's install fails.
func checkIngressPaths(profile string, objects []manifest) []finding {
	var findings []finding
	for _, obj := range objects {
		if obj.Kind != "Ingress" {
			continue
		}
		name := obj.Kind + "/" + obj.Metadata.Name
		paths := 0
		for _, rule := range obj.Spec.Rules {
			for _, p := range rule.HTTP.Paths {
				paths++
				if p.PathType == "" {
					findings = append(findings, finding{
						profile: profile, object: name,
						message: fmt.Sprintf("the rule for host %q has a path %q with no pathType. networking.k8s.io/v1 requires one, so the API server rejects this object.", rule.Host, p.Path),
					})
				}
			}
		}
		if paths == 0 {
			findings = append(findings, finding{
				profile: profile, object: name,
				message: "an Ingress rendered with no paths at all, so the check above proved nothing",
			})
		}
	}
	return findings
}

// checkDisruptionBudgets requires every rendered PodDisruptionBudget to carry
// exactly one budget field.
//
// The API server rejects an object with both. An object with NEITHER is the
// one this rule really exists for: the template used to choose between them by
// truthiness, so a budget could render with no field, or not render at all,
// leaving `kubectl get pdb` empty in a release whose values say disruption
// protection is on. Nobody rechecks a setting they already wrote down.
func checkDisruptionBudgets(profile string, objects []manifest) []finding {
	var findings []finding
	for _, obj := range objects {
		if obj.Kind != "PodDisruptionBudget" {
			continue
		}
		name := obj.Kind + "/" + obj.Metadata.Name
		min, max := obj.Spec.MinAvailable != nil, obj.Spec.MaxUnavailable != nil
		switch {
		case min && max:
			findings = append(findings, finding{
				profile: profile, object: name,
				message: "carries both minAvailable and maxUnavailable, which the API server rejects.",
			})
		case !min && !max:
			findings = append(findings, finding{
				profile: profile, object: name,
				message: "carries neither minAvailable nor maxUnavailable, so it constrains nothing while appearing to be configured.",
			})
		}
	}
	return findings
}

// postgresStampAnnotation is the annotation key the chart writes onto the
// PostgreSQL volumeClaimTemplate, and the one templates/_validations.tpl reads
// back on the next install. It is a key, not a value: what the chart stores
// under it is a hash, and the credentials themselves never appear in a
// manifest.
//
// Spelled out here rather than imported, because there is nothing to import
// from: the chart is YAML. The point of repeating it is that this tool fails
// when the two disagree, which is the only thing standing between a renamed
// annotation and a check that silently stops finding anything.
const postgresStampAnnotation = "the-pleiades/postgres-credentials"

// checkRetainedDataStamp requires the PostgreSQL claim template to carry the
// credential fingerprint.
//
// WHY A LINTER RULE GUARDS THIS ONE. The refusal it belongs to reads the
// cluster (`lookup`), so it cannot fire during any render and nothing offline
// exercises it. Deleting the stamp from the StatefulSet would therefore break
// the retained-data check while every test in the repository stayed green, and
// the failure would resurface as the crash loop the check exists to prevent.
// The rule below is the offline half: the writer must keep writing.
//
// A StatefulSet with no volumeClaimTemplates is skipped rather than failed,
// because postgresql.persistence.enabled=false is a supported arrangement (an
// emptyDir, for a throwaway demo) and there is no claim to stamp.
func checkRetainedDataStamp(profile string, objects []manifest) []finding {
	var findings []finding
	for _, obj := range objects {
		if obj.Kind != "StatefulSet" || !strings.HasSuffix(obj.Metadata.Name, "-postgres") {
			continue
		}
		for _, claim := range obj.Spec.VolumeClaimTemplates {
			if claim.Metadata.Annotations[postgresStampAnnotation] == "" {
				findings = append(findings, finding{
					profile: profile, object: obj.Kind + "/" + obj.Metadata.Name,
					message: fmt.Sprintf("volumeClaimTemplate %q carries no %s annotation. That stamp is what lets a later install tell whether its credentials can open the retained database; without it, a reinstall with a different password installs cleanly and crash-loops forever.", claim.Metadata.Name, postgresStampAnnotation),
				})
			}
		}
	}
	return findings
}

// requirePodDisruptionBudgets asserts that a profile which turned a budget on
// really produced one.
//
// The rule above only sees objects that exist. The defect that hid for a whole
// phase was a budget that did not render at all, and an absence is invisible
// to a check that iterates over what was rendered.
func requirePodDisruptionBudgets(profile string, objects []manifest, want int) []finding {
	got := 0
	for _, obj := range objects {
		if obj.Kind == "PodDisruptionBudget" {
			got++
		}
	}
	if got == want {
		return nil
	}
	return []finding{{
		profile: profile, object: "(render)",
		message: fmt.Sprintf("rendered %d PodDisruptionBudget(s), want %d. A budget that silently does not render is worse than no budget, because nobody goes looking for one they already configured.", got, want),
	}}
}
