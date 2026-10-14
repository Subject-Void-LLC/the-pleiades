// The capability of a Kubernetes cluster, reached through its API server.
package capability

// NameKubernetes is KubernetesCapable's registered name.
const NameKubernetes Name = "KubernetesCapable"

// KubernetesCapable is satisfied by a cluster reached through its API
// server with the credential stored for it (Part XVIII D6: a cluster is one
// device, and its nodes, namespaces and pods are resources on it).
// Onboarding grants it once the API server answered that credential.
type KubernetesCapable interface {
	// KubeAPIServer returns the API server's https URL.
	KubeAPIServer() string
	// KubeNamespace returns the namespace a task works in when it names
	// none.
	KubeNamespace() string
}

func init() {
	Register(Descriptor{
		Name:   NameKubernetes,
		Assert: func(item any) bool { _, ok := item.(KubernetesCapable); return ok },
	})
}
