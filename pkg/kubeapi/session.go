// A Session: one task's clients for one cluster, built from Config.
package kubeapi

import (
	"fmt"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Session is one task's connection to one cluster: the dynamic client,
// which reaches every resource the cluster serves (custom ones included)
// through one path, and discovery, which says what those are.
type Session struct {
	// Config is the client configuration every client of this session
	// shares, for what neither client covers (exec, attach).
	Config *rest.Config
	// Dynamic reads and writes any resource.
	Dynamic dynamic.Interface
	// Discovery reports the server's version and what it serves, each
	// call bounded by its caller's context.
	Discovery discovery.DiscoveryInterfaceWithContext
	// Namespace is the one a task works in when it names none.
	Namespace string
}

// NewSession builds a session's clients from ep and id (Config).
func NewSession(ep Endpoint, id Identity, limits Limits, namespace string) (*Session, error) {
	cfg, err := Config(ep, id, limits)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubeapi: building the dynamic client: %w", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubeapi: building the discovery client: %w", err)
	}
	if namespace == "" {
		namespace = "default"
	}
	return &Session{Config: cfg, Dynamic: dyn, Discovery: disc, Namespace: namespace}, nil
}
