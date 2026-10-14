// Tests of the kubernetes_cluster type: what a record may hold, what its
// accessors read, and how its discovery binds to its address.
package kubernetes_test

import (
	"slices"
	"strings"
	"testing"

	internalinventory "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// build builds a cluster record named c1 with props through the factory
// every store uses.
func build(props map[string]inventory.PropertyValue) (inventory.InventoryItem, error) {
	return internalinventory.NewItemFactory().Build(record.Record{ID: "c1", Name: "c1", Type: kubernetes.TypeCluster, Properties: props})
}

// TestNewCluster_Refusals: a record a cluster cannot be is refused, naming
// the property.
func TestNewCluster_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]inventory.PropertyValue
		want  string
	}{
		{"no API server", map[string]inventory.PropertyValue{}, "is required"},
		{"plain http", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "http://k.example"}, "https"},
		{"a password in the address", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://u:p@k.example"}, "user name or password"},
		{"a query", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example/?x=1"}, "query"},
		{"a bad namespace", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", kubernetes.NamespaceProperty: "Team_A"}, "namespace"},
		{"an empty node engine host", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", kubernetes.NodeEngineHostProperty: ""}, "container_host"},
		{"deprecated TLS", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", devicetls.MinVersionProperty: "1.1", devicetls.AllowDeprecatedProperty: true}, "weak-TLS"},
		{"the client-certificate switch", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", devicetls.ClientCertificateProperty: true}, "stored credential"},
		{"an EKS name without a region", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", kubernetes.EKSClusterProperty: "prod"}, "set both or neither"},
		{"a region without an EKS name", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", kubernetes.AWSRegionProperty: "us-east-1"}, "set both or neither"},
		{"a bad EKS name", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", kubernetes.EKSClusterProperty: "prod cluster", kubernetes.AWSRegionProperty: "us-east-1"}, "not an EKS cluster name"},
		{"a host as region", map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example", kubernetes.EKSClusterProperty: "prod", kubernetes.AWSRegionProperty: "evil.example.com"}, "not an AWS region"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := build(tc.props); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("build = %v, want a refusal saying %q", err, tc.want)
			}
		})
	}
}

// TestNewCluster_Accessors: a valid record reads back as the cluster its
// properties describe, shared across runs, and granted nothing before
// onboarding.
func TestNewCluster_Accessors(t *testing.T) {
	item, err := build(map[string]inventory.PropertyValue{
		kubernetes.APIServerProperty: "https://k.example:6443", kubernetes.NamespaceProperty: "team-a",
		kubernetes.EKSClusterProperty: "prod-cluster", kubernetes.AWSRegionProperty: "eu-west-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	c := item.(*kubernetes.Cluster)
	if c.KubeAPIServer() != "https://k.example:6443" || c.KubeNamespace() != "team-a" {
		t.Errorf("server %q, namespace %q", c.KubeAPIServer(), c.KubeNamespace())
	}
	if name, region := c.KubeEKS(); name != "prod-cluster" || region != "eu-west-1" {
		t.Errorf("KubeEKS = %q, %q", name, region)
	}
	if !inventory.IsShared(c) {
		t.Error("a cluster is not shared across runs")
	}
	if c.HasCapability(capability.NameKubernetes) {
		t.Error("a cluster never onboarded has KubernetesCapable")
	}
	if _, _, complete := c.ServedKinds(); complete {
		t.Error("a cluster never onboarded reports complete served kinds")
	}

	plain, err := build(map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example"})
	if err != nil {
		t.Fatal(err)
	}
	if ns := plain.(*kubernetes.Cluster).KubeNamespace(); ns != "default" {
		t.Errorf("a record naming no namespace works in %q, want default", ns)
	}
}

// TestCluster_DiscoveryBindsToItsAddress: what onboarding discovered holds
// for the address and authority it was made against; moving the record to
// another API server or EKS cluster asks for onboarding again.
func TestCluster_DiscoveryBindsToItsAddress(t *testing.T) {
	props := map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example"}
	item, err := build(props)
	if err != nil {
		t.Fatal(err)
	}
	d := inventory.Discovery{Capabilities: []capability.Name{capability.NameKubernetes},
		Facts:   map[string]any{kubernetes.FactKinds: []any{"v1 Pod", "apps/v1 Deployment"}, kubernetes.FactPartialGroups: []any{"metrics.k8s.io/v1beta1"}},
		Binding: item.(inventory.DiscoveryBinder).DiscoveryBinding()}
	props[inventory.DiscoveredProperty] = d.Property()
	onboarded, err := build(props)
	if err != nil {
		t.Fatal(err)
	}
	if !onboarded.HasCapability(capability.NameKubernetes) {
		t.Fatal("an onboarded cluster lacks KubernetesCapable")
	}
	kinds, partial, complete := onboarded.(*kubernetes.Cluster).ServedKinds()
	if !complete || !slices.Contains(kinds, "apps/v1 Deployment") || !slices.Contains(partial, "metrics.k8s.io/v1beta1") {
		t.Errorf("ServedKinds = %v, %v, %v", kinds, partial, complete)
	}

	for name, change := range map[string]map[string]inventory.PropertyValue{
		"another API server":  {kubernetes.APIServerProperty: "https://elsewhere.example"},
		"another EKS cluster": {kubernetes.EKSClusterProperty: "other", kubernetes.AWSRegionProperty: "us-east-1"},
	} {
		moved := map[string]inventory.PropertyValue{}
		for k, v := range props {
			moved[k] = v
		}
		for k, v := range change {
			moved[k] = v
		}
		item, err := build(moved)
		if err != nil {
			t.Fatal(err)
		}
		c := item.(*kubernetes.Cluster)
		if c.HasCapability(capability.NameKubernetes) || c.StaleDiscovery() == "" {
			t.Errorf("%s: the discovery still holds", name)
		}
		if _, _, complete := c.ServedKinds(); complete {
			t.Errorf("%s: a stale discovery reports complete served kinds", name)
		}
	}
}
