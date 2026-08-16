// Tests for the name, ingress and budget invariants, run without helm and
// without a chart.
//
// Same reasoning as checks_test.go: main.go proves these rules pass against
// the real chart, and a rule that passes on everything looks exactly the same
// from there. These build the broken renders on purpose, including the exact
// output the chart used to produce, and require each rule to fire on it.
package main

import (
	"strings"
	"testing"
)

// workloads builds a render out of workload names, which is all the name
// rules read.
func workloads(names ...string) []manifest {
	objects := make([]manifest, 0, len(names))
	for _, name := range names {
		objects = append(objects, manifest{Kind: "Deployment", Metadata: objectMeta{Name: name}})
	}
	return objects
}

func TestReleaseNamesOfLength(t *testing.T) {
	for _, length := range nameProbeLengths {
		names := releaseNamesOfLength(length)
		if len(names) == 0 {
			t.Fatalf("no release name generated for length %d", length)
		}
		for _, name := range names {
			if len(name) != length {
				t.Fatalf("release name %q is %d characters, want %d", name, len(name), length)
			}
			if length > len("the-pleiades") && len(names) != 2 {
				t.Fatalf("length %d generated %d names, want both branches of the fullname helper", length, len(names))
			}
		}
	}

	// The second form has to really contain the chart name, because that is
	// the whole reason it exists: it takes the OTHER branch of
	// the-pleiades.fullname, which does not append the chart name again.
	names := releaseNamesOfLength(30)
	if len(names) != 2 || !strings.Contains(names[1], "the-pleiades") {
		t.Fatalf("releaseNamesOfLength(30) = %v, want a second name containing the chart name", names)
	}
}

func TestCheckNames(t *testing.T) {
	t.Run("four distinct workload names pass", func(t *testing.T) {
		objects := workloads("r-controller", "r-runner", "r-postgres", "r-nats")
		if findings := checkNames("p", objects); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("an external database and broker are not missing workloads", func(t *testing.T) {
		// postgresql.enabled=false and nats.enabled=false is a supported
		// arrangement, so two of the four suffixes being absent is correct
		// rather than a finding.
		objects := workloads("r-controller", "r-runner")
		if findings := checkNames("p", objects); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("the collision this rule exists for is caught", func(t *testing.T) {
		// Exactly what the chart rendered for a 49-character release name:
		// the suffix truncated away, so all four workloads claimed one name.
		collapsed := "r" + strings.Repeat("a", 48) + "-the-pleiades"
		objects := []manifest{
			{Kind: "Deployment", Metadata: objectMeta{Name: collapsed}},
			{Kind: "Deployment", Metadata: objectMeta{Name: collapsed}},
			{Kind: "StatefulSet", Metadata: objectMeta{Name: collapsed}},
			{Kind: "StatefulSet", Metadata: objectMeta{Name: collapsed}},
		}
		findings := checkNames("p", objects)
		if len(findings) == 0 {
			t.Fatal("reported nothing for four workloads sharing one name")
		}

		var sawDuplicate, sawUnlabeled bool
		for _, f := range findings {
			if strings.Contains(f.message, "exact kind and name") {
				sawDuplicate = true
			}
			if strings.Contains(f.message, "component was truncated away") {
				sawUnlabeled = true
			}
		}
		if !sawDuplicate || !sawUnlabeled {
			t.Fatalf("reported %v, want both the duplicate-name finding and the truncated-component finding", findings)
		}
	})

	t.Run("a component truncated to one letter is caught even when distinct", func(t *testing.T) {
		// The 48-character case: the four names still differ, so a rule that
		// only looked for duplicates would pass a release whose workloads are
		// called "...-c" and "...-r".
		objects := workloads("r-the-pleiades-c", "r-the-pleiades-r", "r-the-pleiades-p", "r-the-pleiades-n")
		findings := checkNames("p", objects)
		if len(findings) < 4 {
			t.Fatalf("reported %v, want a finding for each truncated component", findings)
		}
	})

	t.Run("a Service name past the DNS label limit is caught", func(t *testing.T) {
		long := strings.Repeat("a", 64)
		objects := []manifest{
			{Kind: "Service", Metadata: objectMeta{Name: long}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-controller"}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-runner"}},
		}
		findings := checkNames("p", objects)
		if len(findings) != 1 || !strings.Contains(findings[0].message, "63") {
			t.Fatalf("reported %v, want one finding about the DNS label limit", findings)
		}
	})

	t.Run("a StatefulSet name that is legal for a Deployment is still caught", func(t *testing.T) {
		// THE DEFECT THIS WHOLE PER-KIND TABLE EXISTS FOR. 57 characters is
		// under the 63 the old single budget held every kind to, so the old
		// rule passed it. On a real cluster it creates the StatefulSet and
		// then refuses every pod, forever.
		name := strings.Repeat("a", 48) + "-postgres"
		if len(name) != 57 {
			t.Fatalf("the fixture is %d characters, want 57", len(name))
		}
		objects := []manifest{
			{Kind: "StatefulSet", Metadata: objectMeta{Name: name}, Spec: manifestSpec{ServiceName: name}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-controller"}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-runner"}},
		}

		// The same name as a Deployment is legal, which is the asymmetry the
		// table encodes and the reason one number for all kinds was wrong in
		// both directions at once.
		asDeployment := []manifest{
			{Kind: "Deployment", Metadata: objectMeta{Name: name}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-controller"}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-runner"}},
		}
		if findings := checkNames("p", asDeployment); len(findings) != 0 {
			t.Fatalf("reported %v for a 57-character Deployment, want nothing: a Deployment has no 63-character limit", findings)
		}

		findings := checkNames("p", objects)
		var sawLimit, sawRevision bool
		for _, f := range findings {
			if strings.Contains(f.message, "past the 52 this kind is held to") {
				sawLimit = true
			}
			if strings.Contains(f.message, "controller-revision-hash") {
				sawRevision = true
			}
		}
		if !sawLimit || !sawRevision {
			t.Fatalf("reported %v, want both the per-kind limit finding and the revision-hash finding", findings)
		}
	})

	t.Run("a claim name past the DNS label limit is legal and not a finding", func(t *testing.T) {
		// A PersistentVolumeClaim name is a DNS subdomain, so 253 is the real
		// limit. A linter that failed a legal configuration would teach people
		// to work around the linter.
		objects := []manifest{
			{Kind: "PersistentVolumeClaim", Metadata: objectMeta{Name: strings.Repeat("a", 68)}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-controller"}},
			{Kind: "Deployment", Metadata: objectMeta{Name: "r-runner"}},
		}
		if findings := checkNames("p", objects); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})
}

func TestCheckStatefulSetPodNames(t *testing.T) {
	statefulSet := func(name string, replicas *int) manifest {
		return manifest{
			Kind:     "StatefulSet",
			Metadata: objectMeta{Name: name},
			Spec:     manifestSpec{ServiceName: name, Replicas: replicas},
		}
	}
	count := func(n int) *int { return &n }

	t.Run("a name inside every derived limit passes", func(t *testing.T) {
		// 52 is the ceiling exactly: 52 + 1 + 10 characters of revision hash
		// is 63, which a label value may carry.
		name := strings.Repeat("a", 43) + "-postgres"
		if len(name) != 52 {
			t.Fatalf("the fixture is %d characters, want 52", len(name))
		}
		if findings := checkStatefulSetPodNames("p", []manifest{statefulSet(name, count(1))}); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("one character past the ceiling is caught", func(t *testing.T) {
		name := strings.Repeat("a", 44) + "-postgres"
		findings := checkStatefulSetPodNames("p", []manifest{statefulSet(name, count(1))})
		if len(findings) != 1 || !strings.Contains(findings[0].message, "controller-revision-hash") {
			t.Fatalf("reported %v, want one finding about the revision-hash label", findings)
		}
	})

	t.Run("the pod name limit is reported separately from the revision hash", func(t *testing.T) {
		// 62 characters: the pod would be named "<name>-0", 64 characters,
		// which the API server refuses as a spec.hostname. Both limits are
		// broken here and each has to say which one it is, because they are
		// fixed by different amounts.
		name := strings.Repeat("a", 62)
		findings := checkStatefulSetPodNames("p", []manifest{statefulSet(name, count(1))})
		var sawPod, sawRevision bool
		for _, f := range findings {
			if strings.Contains(f.message, "spec.hostname") {
				sawPod = true
			}
			if strings.Contains(f.message, "controller-revision-hash") {
				sawRevision = true
			}
		}
		if !sawPod || !sawRevision {
			t.Fatalf("reported %v, want both the pod-name finding and the revision-hash finding", findings)
		}
	})

	t.Run("a higher replica count is measured at its highest ordinal", func(t *testing.T) {
		// The chart pins both StatefulSets at one replica. This is what
		// happens if that ever becomes a value: the ordinal grows and the
		// name has to have left room for it.
		name := strings.Repeat("a", 61)
		if findings := checkStatefulSetPodNames("p", []manifest{statefulSet(name, count(1))}); len(findings) == 0 {
			t.Fatal("reported nothing for a 61-character name, want the ordinal reserve finding")
		}
		findings := checkStatefulSetPodNames("p", []manifest{statefulSet(name, count(100))})
		var sawHighestOrdinal bool
		for _, f := range findings {
			if strings.Contains(f.message, "highest-ordinal pod would be named") && strings.Contains(f.message, "-99") {
				sawHighestOrdinal = true
			}
		}
		if !sawHighestOrdinal {
			t.Fatalf("reported %v, want the pod-name finding to be measured at ordinal 99", findings)
		}
	})

	t.Run("a StatefulSet with no governing Service is caught", func(t *testing.T) {
		obj := manifest{Kind: "StatefulSet", Metadata: objectMeta{Name: "r-postgres"}}
		findings := checkStatefulSetPodNames("p", []manifest{obj})
		if len(findings) != 1 || !strings.Contains(findings[0].message, "spec.serviceName") {
			t.Fatalf("reported %v, want one finding about the missing governing Service", findings)
		}
	})

	t.Run("an over-long fully qualified pod name is caught", func(t *testing.T) {
		// The chart cannot reach this: every label is held to 63 by the
		// checks above, which bounds the whole name at 209 characters. The
		// rule is exercised here anyway, because a bound that nothing can
		// demonstrate is an arithmetic claim rather than a check, and the
		// arithmetic is what a future budget change would move.
		name := strings.Repeat("a", 120)
		obj := manifest{
			Kind:     "StatefulSet",
			Metadata: objectMeta{Name: name},
			Spec:     manifestSpec{ServiceName: strings.Repeat("s", 120)},
		}
		findings := checkStatefulSetPodNames("p", []manifest{obj})
		var sawFQDN bool
		for _, f := range findings {
			if strings.Contains(f.message, "fully qualified DNS name") {
				sawFQDN = true
			}
		}
		if !sawFQDN {
			t.Fatalf("reported %v, want the fully qualified name finding", findings)
		}
	})
}

func TestCheckDeploymentPodNames(t *testing.T) {
	t.Run("two ordinary Deployments pass", func(t *testing.T) {
		objects := workloads("r-controller", "r-runner")
		if findings := checkDeploymentPodNames("p", objects); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("two Deployments whose pod names truncate to one string are caught", func(t *testing.T) {
		// Kubernetes cuts a generated pod name's base at 58 characters, so
		// two Deployments agreeing that far produce pods that differ only in
		// their random suffix. Nothing in Kubernetes reports this: both
		// workloads run, and `kubectl get pods` stops saying which is which.
		prefix := strings.Repeat("a", 58)
		objects := workloads(prefix+"-controller", prefix+"-runner")
		findings := checkDeploymentPodNames("p", objects)
		if len(findings) != 1 || !strings.Contains(findings[0].message, "random suffix") {
			t.Fatalf("reported %v, want one finding about indistinguishable pod names", findings)
		}
	})

	t.Run("the chart's own longest names stay distinguishable", func(t *testing.T) {
		// The budget is 52, so this is what the two Deployments of the
		// longest legal release name really look like.
		prefix := strings.Repeat("a", 52)
		objects := workloads(prefix+"-controller", prefix+"-runner")
		if findings := checkDeploymentPodNames("p", objects); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing: the component word survives the cut far enough to tell the two apart", findings)
		}
	})
}

func TestCheckIngressPaths(t *testing.T) {
	ingress := func(pathType string) manifest {
		return manifest{
			Kind:     "Ingress",
			Metadata: objectMeta{Name: "r-controller"},
			Spec: manifestSpec{Rules: []ingressRule{{
				Host: "pleiades.example.com",
				HTTP: ingressHTTPRule{Paths: []ingressPath{{Path: "/", PathType: pathType}}},
			}}},
		}
	}

	t.Run("a path with a pathType passes", func(t *testing.T) {
		if findings := checkIngressPaths("p", []manifest{ingress("Prefix")}); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a path with no pathType is refused", func(t *testing.T) {
		findings := checkIngressPaths("p", []manifest{ingress("")})
		if len(findings) != 1 || !strings.Contains(findings[0].message, "pathType") {
			t.Fatalf("reported %v, want one finding about the missing pathType", findings)
		}
	})

	t.Run("an Ingress with no paths proves nothing and says so", func(t *testing.T) {
		findings := checkIngressPaths("p", []manifest{{Kind: "Ingress", Metadata: objectMeta{Name: "r"}}})
		if len(findings) != 1 || !strings.Contains(findings[0].message, "proved nothing") {
			t.Fatalf("reported %v, want the empty-ingress guard to fire", findings)
		}
	})
}

func TestCheckDisruptionBudgets(t *testing.T) {
	budget := func(min, max *budgetField) manifest {
		return manifest{
			Kind:     "PodDisruptionBudget",
			Metadata: objectMeta{Name: "r-runner"},
			Spec:     manifestSpec{MinAvailable: min, MaxUnavailable: max},
		}
	}
	value := func(text string) *budgetField { return &budgetField{Text: text} }

	t.Run("one field passes", func(t *testing.T) {
		if findings := checkDisruptionBudgets("p", []manifest{budget(nil, value("1"))}); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a zero budget is a real budget", func(t *testing.T) {
		// maxUnavailable 0 permits no voluntary eviction at all. The truthiness
		// test this rule replaces read it as absent.
		if findings := checkDisruptionBudgets("p", []manifest{budget(nil, value("0"))}); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("both fields are refused", func(t *testing.T) {
		findings := checkDisruptionBudgets("p", []manifest{budget(value("2"), value("1"))})
		if len(findings) != 1 || !strings.Contains(findings[0].message, "both") {
			t.Fatalf("reported %v, want one finding about carrying both", findings)
		}
	})

	t.Run("neither field is refused", func(t *testing.T) {
		findings := checkDisruptionBudgets("p", []manifest{budget(nil, nil)})
		if len(findings) != 1 || !strings.Contains(findings[0].message, "neither") {
			t.Fatalf("reported %v, want one finding about carrying neither", findings)
		}
	})
}

func TestCheckRetainedDataStamp(t *testing.T) {
	statefulSet := func(annotations map[string]string) manifest {
		return manifest{
			Kind:     "StatefulSet",
			Metadata: objectMeta{Name: "r-the-pleiades-postgres"},
			Spec: manifestSpec{VolumeClaimTemplates: []claimTemplate{{
				Metadata: objectMeta{Name: "data", Annotations: annotations},
			}}},
		}
	}

	t.Run("a stamped claim template passes", func(t *testing.T) {
		objects := []manifest{statefulSet(map[string]string{postgresStampAnnotation: "6551171588d16cf5"})}
		if findings := checkRetainedDataStamp("p", objects); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("an unstamped claim template is refused", func(t *testing.T) {
		findings := checkRetainedDataStamp("p", []manifest{statefulSet(nil)})
		if len(findings) != 1 || !strings.Contains(findings[0].message, postgresStampAnnotation) {
			t.Fatalf("reported %v, want one finding naming the missing stamp", findings)
		}
	})

	t.Run("no claim template is not a finding", func(t *testing.T) {
		// postgresql.persistence.enabled=false runs on an emptyDir. There is
		// no volume to retain, so there is nothing to stamp.
		objects := []manifest{{Kind: "StatefulSet", Metadata: objectMeta{Name: "r-the-pleiades-postgres"}}}
		if findings := checkRetainedDataStamp("p", objects); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})
}

func TestRequirePodDisruptionBudgets(t *testing.T) {
	pdb := manifest{Kind: "PodDisruptionBudget", Metadata: objectMeta{Name: "r-runner"}}

	t.Run("the expected count passes", func(t *testing.T) {
		if findings := requirePodDisruptionBudgets("p", []manifest{pdb}, 1); len(findings) != 0 {
			t.Fatalf("reported %v, want nothing", findings)
		}
	})

	t.Run("a budget that silently did not render is caught", func(t *testing.T) {
		// The absence is the defect. Every rule that iterates over rendered
		// objects passes when the object is missing, which is how a chart
		// shipped with a budget that vanished for ordinary values.
		findings := requirePodDisruptionBudgets("p", nil, 1)
		if len(findings) != 1 || !strings.Contains(findings[0].message, "want 1") {
			t.Fatalf("reported %v, want one finding about the missing budget", findings)
		}
	})
}

func TestBudgetFieldDecodesEitherShape(t *testing.T) {
	// An integer and a percentage string are both legal, and the presence of
	// the key is what the rule reads.
	rendered := `apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: r-runner
spec:
  minAvailable: 0
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: r-controller
spec:
  maxUnavailable: 50%
`
	objects, err := decodeManifests(rendered)
	if err != nil {
		t.Fatalf("decodeManifests: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("decoded %d objects, want 2", len(objects))
	}
	if objects[0].Spec.MinAvailable == nil || objects[0].Spec.MinAvailable.Text != "0" {
		t.Fatalf("minAvailable decoded as %+v, want a present field holding 0", objects[0].Spec.MinAvailable)
	}
	if objects[0].Spec.MaxUnavailable != nil {
		t.Fatalf("maxUnavailable decoded as %+v, want nil for an absent key", objects[0].Spec.MaxUnavailable)
	}
	if objects[1].Spec.MaxUnavailable == nil || objects[1].Spec.MaxUnavailable.Text != "50%" {
		t.Fatalf("maxUnavailable decoded as %+v, want a present field holding 50%%", objects[1].Spec.MaxUnavailable)
	}
}
