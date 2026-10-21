// The devices the injection tests hand ApplyInjection, which decides from a
// device whether a machine credential may authenticate it.
package dispatch

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// loginDevice is a server reached over SSH, which a machine credential
// authenticates.
func loginDevice() *inventorytest.Stub {
	return &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameSSHTransport, capability.NameLinux}}
}

// clusterDevice is a Kubernetes cluster, reached only through its API
// server, which a machine credential never authenticates.
func clusterDevice() *inventorytest.Stub {
	return &inventorytest.Stub{StubName: "prod", Caps: []capability.Name{capability.NameKubernetes}}
}
