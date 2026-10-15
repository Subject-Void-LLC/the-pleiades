// Tests of onboarding's credential fallback for a Kubernetes cluster with
// no per-device credential.
package onboard

import (
	"context"
	"errors"
	"testing"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestWithKubernetesFallback: the per-device credential wins; a cluster
// with none gets what the lookup finds for its API server; any other
// device never reaches the lookup.
func TestWithKubernetesFallback(t *testing.T) {
	factory := inv.NewItemFactory()
	cluster, err := factory.Build(record.Record{ID: "c1", Name: "c1", Type: kubernetes.TypeCluster,
		Properties: map[string]inventory.PropertyValue{kubernetes.APIServerProperty: "https://k.example:6443"}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := factory.Build(record.Record{ID: "s1", Name: "s1", Type: "linux_server"})
	if err != nil {
		t.Fatal(err)
	}
	own := map[string]string{"bearer_token": "own"}
	var asked []string
	lookup := func(_ context.Context, device inventory.InventoryItem) (map[string]string, error) {
		asked = append(asked, device.(capability.KubernetesCapable).KubeAPIServer())
		return map[string]string{"bearer_token": "stored"}, nil
	}
	primary := func(stored map[string]string, err error) SecretsFunc {
		return func(context.Context, inventory.InventoryItem) (map[string]string, error) { return stored, err }
	}
	ctx := context.Background()

	if got, _ := WithKubernetesFallback(primary(own, nil), lookup)(ctx, cluster); got["bearer_token"] != "own" || len(asked) != 0 {
		t.Errorf("a cluster with its own credential got %v (lookups %v)", got, asked)
	}
	if got, _ := WithKubernetesFallback(primary(nil, nil), lookup)(ctx, cluster); got["bearer_token"] != "stored" || len(asked) != 1 || asked[0] != "https://k.example:6443" {
		t.Errorf("a cluster with none got %v (lookups %v)", got, asked)
	}
	if got, _ := WithKubernetesFallback(primary(nil, nil), lookup)(ctx, server); got != nil || len(asked) != 1 {
		t.Errorf("a server reached the cluster lookup: %v (lookups %v)", got, asked)
	}
	broken := errors.New("store unreadable")
	if _, err := WithKubernetesFallback(primary(nil, broken), lookup)(ctx, cluster); !errors.Is(err, broken) || len(asked) != 1 {
		t.Errorf("a failing per-device store: %v (lookups %v)", err, asked)
	}
}
