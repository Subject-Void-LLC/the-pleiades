// Package validate_test: tests of the Kubernetes kind rule (Phase 119c),
// against real kubernetes_cluster records carrying a recorded discovery.
package validate_test

import (
	"context"
	"strings"
	"testing"

	internalinventory "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// registerManifestMethod registers a fixture method whose definition
// parameter is a Kubernetes manifest, for the rest of the test.
func registerManifestMethod(t *testing.T) {
	t.Helper()
	t.Cleanup(collection.SnapshotForTest())
	run := func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
		return collection.Result{}, nil
	}
	if err := collection.Register(collection.Descriptor{Name: "t119.apply", Invoke: run, Manifest: collection.Manifest{Status: collection.StatusImplemented,
		Reversibility: collection.Reversibility{ReadOnly: true, Notes: "a test fixture that changes nothing"},
		Doc:           collection.Doc{Params: []collection.Param{{Name: "definition", Type: "raw", Format: collection.ParamFormatKubernetesManifest}}}}}); err != nil {
		t.Fatal(err)
	}
}

// servedKinds is what the fixture cluster's discovery found.
var servedKinds = []any{"v1 ConfigMap", "v1 Service", "apps/v1 Deployment", "autoscaling/v2 HorizontalPodAutoscaler",
	"apiextensions.k8s.io/v1 CustomResourceDefinition", "apiregistration.k8s.io/v1 APIService"}

// cluster builds a kubernetes_cluster named prod the way one loads after
// onboarding: its record carries a discovery bound to its address, holding
// facts. A nil facts builds one never onboarded.
func cluster(t *testing.T, facts map[string]any) inventory.InventoryItem {
	t.Helper()
	rec := record.Record{ID: "c1", Name: "prod", Type: kubernetes.TypeCluster,
		Properties: map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example:6443"}}
	factory := internalinventory.NewItemFactory()
	item, err := factory.Build(rec)
	if err != nil {
		t.Fatal(err)
	}
	if facts == nil {
		return item
	}
	d := inventory.Discovery{Capabilities: []capability.Name{capability.NameKubernetes}, Facts: facts,
		Binding: item.(inventory.DiscoveryBinder).DiscoveryBinding()}
	rec.Properties[inventory.DiscoveredProperty] = d.Property()
	if item, err = factory.Build(rec); err != nil {
		t.Fatal(err)
	}
	return item
}

// TestKubernetesKindRule: an object whose kind the cluster does not serve
// is refused, naming the cluster and the kind; every case the rule cannot
// know passes.
func TestKubernetesKindRule(t *testing.T) {
	registerManifestMethod(t)
	onboarded := cluster(t, map[string]any{"kinds": servedKinds})
	widgetCRD := "apiVersion: apiextensions.k8s.io/v1\n        kind: CustomResourceDefinition\n        metadata: {name: widgets.example.com}\n        spec:\n          group: example.com\n          names: {kind: Widget, plural: widgets}\n          scope: Namespaced\n          versions:\n          - {name: v1, served: true, storage: true}\n          - {name: v0, served: false, storage: false}"
	for _, tc := range []struct {
		name  string
		tasks string
		want  string
	}{
		{"a served kind", "definition: {apiVersion: apps/v1, kind: Deployment, metadata: {name: web}}", ""},
		{"an unserved kind", "definition: {apiVersion: example.com/v1, kind: Widget, metadata: {name: w}}", "does not serve example.com/v1 Widget"},
		{"a served kind at a version not served", "definition: {apiVersion: autoscaling/v1, kind: HorizontalPodAutoscaler, metadata: {name: h}}", "only at autoscaling/v2"},
		{"a document stream", "definition: |\n        apiVersion: v1\n        kind: Service\n        metadata: {name: web}\n        ---\n        apiVersion: batch/v1\n        kind: CronJob\n        metadata: {name: nightly}", "does not serve batch/v1 CronJob"},
		{"a list of objects", "definition:\n        - {apiVersion: v1, kind: ConfigMap, metadata: {name: a}}\n        - {apiVersion: policy/v1, kind: PodDisruptionBudget, metadata: {name: b}}", "does not serve policy/v1 PodDisruptionBudget"},
		{"a v1 List", "definition: {apiVersion: v1, kind: List, items: [{apiVersion: v1, kind: Service}, {apiVersion: networking.k8s.io/v1, kind: Ingress}]}", "does not serve networking.k8s.io/v1 Ingress"},
		{"an object with no kind", "definition: {apiVersion: v1, metadata: {name: x}}", "name no apiVersion and kind"},
		{"a CRD in the same task", "definition:\n        - " + strings.ReplaceAll(widgetCRD, "\n        ", "\n          ") + "\n        - {apiVersion: example.com/v1, kind: Widget, metadata: {name: w}}", ""},
		{"a CRD's version it does not serve", "definition:\n        - " + strings.ReplaceAll(widgetCRD, "\n        ", "\n          ") + "\n        - {apiVersion: example.com/v0, kind: Widget, metadata: {name: w}}", "does not serve example.com/v0 Widget"},
		{"a group an APIService registers", "definition:\n        - {apiVersion: apiregistration.k8s.io/v1, kind: APIService, spec: {group: metrics.example.com, version: v1}}\n        - {apiVersion: metrics.example.com/v1, kind: Usage}", ""},
		{"an APIService's other version", "definition:\n        - {apiVersion: apiregistration.k8s.io/v1, kind: APIService, spec: {group: metrics.example.com, version: v1}}\n        - {apiVersion: metrics.example.com/v2, kind: Usage}", "does not serve metrics.example.com/v2 Usage"},
		{"a manifest that renders", "definition: \"{{ vars.manifest }}\"", ""},
		{"a templated kind", "definition: {apiVersion: example.com/v1, kind: \"{{ vars.kind }}\"}", ""},
		{"a literal kind beside a templated value", "definition: {apiVersion: example.com/v1, kind: Widget, metadata: {name: \"{{ vars.name }}\"}}", "does not serve example.com/v1 Widget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dag := yamlDAG(t, "id: k\nhosts: prod\ntasks:\n  - name: apply\n    t119.apply:\n      "+tc.tasks+"\n")
			findings := validate.KubernetesKindRule(validate.WorldView{Items: []inventory.InventoryItem{onboarded}, DAG: dag})
			switch {
			case tc.want == "" && len(findings) > 0:
				t.Fatalf("refused a manifest it cannot fault: %v", findings)
			case tc.want != "" && (len(findings) != 1 || !strings.Contains(findings[0].Message, tc.want)):
				t.Fatalf("findings %v, want one saying %q", findings, tc.want)
			case tc.want != "" && !strings.Contains(findings[0].Message, "kind") && !strings.Contains(findings[0].Message, "prod"):
				t.Fatalf("finding %q names neither the cluster nor the kind", findings[0].Message)
			}
		})
	}
}

// TestKubernetesKindRule_ACRDInAnotherTask proves a kind a CRD defines
// counts as served wherever in the runbook the CRD is applied.
func TestKubernetesKindRule_ACRDInAnotherTask(t *testing.T) {
	registerManifestMethod(t)
	onboarded := cluster(t, map[string]any{"kinds": servedKinds})
	dag := yamlDAG(t, `id: k
hosts: prod
tasks:
  - name: the widget
    t119.apply:
      definition: {apiVersion: example.com/v1, kind: Widget, metadata: {name: w}}
  - name: the CRD
    t119.apply:
      definition:
        apiVersion: apiextensions.k8s.io/v1
        kind: CustomResourceDefinition
        spec:
          group: example.com
          names: {kind: Widget}
          versions: [{name: v1, served: true}]
`)
	if f := validate.KubernetesKindRule(validate.WorldView{Items: []inventory.InventoryItem{onboarded}, DAG: dag}); len(f) > 0 {
		t.Fatalf("refused a kind a CRD later in the runbook defines: %v", f)
	}
}

// TestKubernetesKindRule_WhatItCannotKnow: a cluster with no complete
// discovery, and a group whose discovery failed, are never refused.
func TestKubernetesKindRule_WhatItCannotKnow(t *testing.T) {
	registerManifestMethod(t)
	const task = "definition: {apiVersion: metrics.k8s.io/v1beta1, kind: NodeMetrics}"
	for name, item := range map[string]inventory.InventoryItem{
		"never onboarded": cluster(t, nil),
		"truncated":       cluster(t, map[string]any{"kinds": servedKinds, "kinds_truncated": true}),
		"partial group":   cluster(t, map[string]any{"kinds": servedKinds, "partial_groups": []any{"metrics.k8s.io/v1beta1"}}),
	} {
		t.Run(name, func(t *testing.T) {
			dag := yamlDAG(t, "id: k\nhosts: prod\ntasks:\n  - name: apply\n    t119.apply:\n      "+task+"\n")
			if f := validate.KubernetesKindRule(validate.WorldView{Items: []inventory.InventoryItem{item}, DAG: dag}); len(f) > 0 {
				t.Fatalf("refused against a discovery that cannot say: %v", f)
			}
		})
	}

	// A discovery made against another address no longer holds.
	moved := cluster(t, map[string]any{"kinds": servedKinds})
	props := moved.Properties().Raw()
	props[kubernetes.APIServerProperty] = "https://elsewhere.example:6443"
	movedItem, err := internalinventory.NewItemFactory().Build(record.Record{ID: "c1", Name: "prod", Type: kubernetes.TypeCluster, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	dag := yamlDAG(t, "id: k\nhosts: prod\ntasks:\n  - name: apply\n    t119.apply:\n      "+task+"\n")
	if f := validate.KubernetesKindRule(validate.WorldView{Items: []inventory.InventoryItem{movedItem}, DAG: dag}); len(f) > 0 {
		t.Fatalf("refused against a discovery bound to another address: %v", f)
	}
}

// TestKubernetesKindRule_EdgesOfWhatItReads: a manifest parameter the task
// leaves out, a device that is not a cluster, an item that is not an
// object, and a manifest past the bound on objects read.
func TestKubernetesKindRule_EdgesOfWhatItReads(t *testing.T) {
	registerManifestMethod(t)
	onboarded := cluster(t, map[string]any{"kinds": servedKinds})
	world := func(task string, items ...inventory.InventoryItem) validate.WorldView {
		return validate.WorldView{Items: items, DAG: yamlDAG(t, "id: k\nhosts: prod\ntasks:\n  - name: apply\n    t119.apply:\n      "+task+"\n")}
	}
	if f := validate.KubernetesKindRule(world("{}", onboarded)); len(f) > 0 {
		t.Errorf("a task without the manifest parameter: %v", f)
	}
	server, err := internalinventory.NewItemFactory().Build(record.Record{ID: "prod", Name: "prod", Type: "linux_server"})
	if err != nil {
		t.Fatal(err)
	}
	if f := validate.KubernetesKindRule(world("definition: {apiVersion: example.com/v1, kind: Widget}", server)); len(f) > 0 {
		t.Errorf("a manifest aimed at a server was checked as a cluster's: %v", f)
	}
	if f := validate.KubernetesKindRule(world("definition: [plain text, 7]", onboarded)); len(f) != 1 || !strings.Contains(f[0].Message, "2 object(s) name no apiVersion and kind") {
		t.Errorf("items that are not objects: %v", f)
	}

	// Past the bound, the rest is left to the method: an unserved kind
	// after 10,000 objects is not read.
	var b strings.Builder
	b.WriteString("definition:\n")
	for range 10000 {
		b.WriteString("        - {apiVersion: v1, kind: ConfigMap}\n")
	}
	b.WriteString("        - {apiVersion: example.com/v1, kind: Widget}")
	if f := validate.KubernetesKindRule(world(b.String(), onboarded)); len(f) > 0 {
		t.Errorf("an object past the bound was read: %v", f)
	}
}
