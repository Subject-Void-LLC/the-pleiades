// The Kubernetes probe: the API server, asked with the cluster's own
// credential what it is, what it serves and what that credential may do.
package onboard

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The facts a cluster's discovery records.
const (
	FactKubernetesVersion = "kubernetes_version"
	FactKinds             = kubernetes.FactKinds
	FactKindsTruncated    = kubernetes.FactKindsTruncated
	FactPartialGroups     = kubernetes.FactPartialGroups
	FactNamespace         = "namespace"
	FactRules             = "rules"
	FactRulesIncomplete   = "rules_incomplete"
	FactNodes             = "nodes"
)

type kubernetesProber struct{}

func init() { Register(kubernetes.TypeCluster, kubernetesProber{}) }

func (kubernetesProber) Protocol() string { return "kubernetes" }

// Probe is ProbeWithPeers with no peers to consult.
func (p kubernetesProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	return p.ProbeWithPeers(ctx, device, secrets, nil)
}

// ProbeWithPeers reaches the API server with the cluster's own credential, over TLS
// with its pinned authority, and records what the server reports: its
// version, the kinds it serves, what the credential may do in the cluster's
// namespace (its own SelfSubjectRulesReview), and its nodes when the
// credential may list them. An answer grants KubernetesCapable; a refused
// credential or an unverifiable certificate grants nothing. When the
// record names the container host its nodes run on, it also records which
// container each node is (mapNodes).
func (kubernetesProber) ProbeWithPeers(ctx context.Context, device inventory.InventoryItem, secrets map[string]string, peers Peers) (Probed, error) {
	// A cluster behind a bastion is probed through its route, as its
	// tasks will reach it.
	var dial sdk.RouteDial
	if peers != nil {
		var err error
		if dial, err = peers.Route(ctx, device); err != nil {
			return Probed{}, fmt.Errorf("resolving the cluster's route: %w", err)
		}
	}
	s, err := kubeapi.ForDeviceVia(device, secrets, "", dial)
	if err != nil {
		return Probed{}, err
	}
	d, err := s.Discover(ctx)
	if err != nil {
		return Probed{}, err
	}
	facts := map[string]any{
		FactKubernetesVersion: factText(d.Version),
		FactKinds:             anyStrings(d.Kinds),
		FactNamespace:         factText(d.Namespace),
		FactRules:             anyStrings(d.Rules),
		FactRulesIncomplete:   d.RulesIncomplete,
	}
	if len(d.Partial) > 0 {
		facts[FactPartialGroups] = anyStrings(d.Partial)
	}
	if d.KindsTruncated {
		facts[FactKindsTruncated] = true
	}
	if len(d.Nodes) > 0 {
		nodes := make([]any, len(d.Nodes))
		for i, n := range d.Nodes {
			nodes[i] = map[string]any{"name": factText(n.Name), "container_runtime": factText(n.ContainerRuntime), "machine_id": factText(n.MachineID)}
		}
		facts[FactNodes] = nodes
	}
	if host, _ := device.Properties().String(kubernetes.NodeEngineHostProperty); host != "" && len(d.Nodes) > 0 {
		if peers == nil {
			return Probed{}, fmt.Errorf("the record names node engine host %q, which this onboarding cannot reach", host)
		}
		mapped, err := mapNodes(ctx, d.Nodes, host, peers)
		if err != nil {
			return Probed{}, err
		}
		facts[FactNodeContainers] = mapped
	}
	return Probed{Capabilities: []capability.Name{capability.NameKubernetes}, Facts: facts}, nil
}

// anyStrings is ss as the []any a discovery's facts hold.
func anyStrings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = factText(s)
	}
	return out
}
