// Package kubernetes holds the kubernetes_cluster device type: one
// Kubernetes cluster, reached through its API server (Part XVIII D6).
package kubernetes

import (
	"fmt"
	"net/url"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/kubeapi"
)

// TypeCluster is the kubernetes_cluster device type.
const TypeCluster = "kubernetes_cluster"

// The properties a cluster's record holds.
const (
	// APIServerProperty is the API server's https URL.
	APIServerProperty = "api_server"
	// NamespaceProperty is the namespace a task works in when it names
	// none; "default" when absent.
	NamespaceProperty = "namespace"
	// NodeEngineHostProperty names the container_host whose engine runs
	// the cluster's nodes as containers (kind, Docker Desktop), so
	// onboarding records which container each node is.
	NodeEngineHostProperty = "node_engine_host"
	// EKSClusterProperty and AWSRegionProperty name an Amazon EKS cluster
	// and its region. A record naming them authenticates with EKS tokens
	// minted from its own credential read as an AWS key (the access key ID
	// as its username, the secret access key as its password), the way
	// cloud.aws methods read one.
	EKSClusterProperty = "eks_cluster_name"
	AWSRegionProperty  = "aws_region"
)

// ConnectionProperties are the properties a kubeconfig import owns: where
// the cluster is, the authority that vouches for it, and the namespace. A
// replacing import sets or removes exactly these and leaves every other
// property (a route, a node engine host, an EKS cluster) as the operator
// set it.
var ConnectionProperties = []string{APIServerProperty, devicetls.CAPEMProperty, devicetls.ServerNameProperty, NamespaceProperty}

// defaultNamespace is the namespace a cluster's tasks use when its record
// names none, as every Kubernetes client does.
const defaultNamespace = "default"

// Cluster is kubernetes_cluster. KubernetesCapable is discovered:
// onboarding reaches the API server with the cluster's credential, and only
// an answer grants it. Nodes, namespaces and pods are resources on it, not
// devices of their own.
type Cluster struct {
	*record.Base
	server *url.URL
	tls    devicetls.Settings
	stale  string
}

// NewCluster builds a kubernetes_cluster from rec, refusing an API server
// that is not an https URL with a host and no credential in it, and TLS
// settings a cluster does not take: a Kubernetes API server speaks TLS 1.2
// or later, and its client identity is the credential stored for it, so
// the weak-TLS opt-ins and the client-certificate switch are refused rather
// than ignored.
func NewCluster(rec record.Record) (inventory.InventoryItem, error) {
	props := inventory.NewProperties(rec.Properties)
	raw, _ := props.String(APIServerProperty)
	server, err := apiServerURL(raw)
	if err != nil {
		return nil, fmt.Errorf("%s %s: property %s: %w", TypeCluster, rec.Name, APIServerProperty, err)
	}
	if h, present := rec.Properties[NodeEngineHostProperty]; present {
		if s, ok := h.(string); !ok || s == "" {
			return nil, fmt.Errorf("%s %s: property %s must name a container_host device", TypeCluster, rec.Name, NodeEngineHostProperty)
		}
	}
	eksName, _ := props.String(EKSClusterProperty)
	region, _ := props.String(AWSRegionProperty)
	switch {
	case (eksName == "") != (region == ""):
		return nil, fmt.Errorf("%s %s: properties %s and %s name an EKS cluster together; set both or neither", TypeCluster, rec.Name, EKSClusterProperty, AWSRegionProperty)
	case eksName != "" && !awscloud.ValidEKSClusterName(eksName):
		return nil, fmt.Errorf("%s %s: property %s %q is not an EKS cluster name", TypeCluster, rec.Name, EKSClusterProperty, eksName)
	case region != "" && !awscloud.ValidRegion(region):
		return nil, fmt.Errorf("%s %s: property %s %q is not an AWS region", TypeCluster, rec.Name, AWSRegionProperty, region)
	}
	if ns, present := rec.Properties[NamespaceProperty]; present {
		if s, ok := ns.(string); !ok || !kubeapi.ValidNamespace(s) {
			return nil, fmt.Errorf("%s %s: property %s must be a Kubernetes namespace name", TypeCluster, rec.Name, NamespaceProperty)
		}
	}
	settings, err := devicetls.Parse(props)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeCluster, rec.Name, err)
	}
	switch {
	case settings.Deprecated() || settings.AllowLegacyCiphers:
		return nil, fmt.Errorf("%s %s: a Kubernetes API server speaks TLS 1.2 or later with current ciphers, so the weak-TLS settings do not apply", TypeCluster, rec.Name)
	case settings.ClientCertificate:
		return nil, fmt.Errorf("%s %s: property %s does not apply: a cluster presents a client certificate exactly when its stored credential holds one", TypeCluster, rec.Name, devicetls.ClientCertificateProperty)
	}
	caps, stale, err := record.Declared(TypeCluster, rec, nil)
	if err != nil {
		return nil, err
	}
	return &Cluster{Base: record.NewBase(rec, caps), server: server, tls: settings, stale: stale}, nil
}

// apiServerURL validates an API server address: https, a host, and nothing
// a client could mistake for a credential.
func apiServerURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, fmt.Errorf("is required: the API server's https URL")
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return nil, fmt.Errorf("does not parse: %w", err)
	case u.Scheme != "https":
		return nil, fmt.Errorf("must be an https URL; a cluster is reached over TLS only")
	case u.Host == "":
		return nil, fmt.Errorf("names no host")
	case u.User != nil:
		return nil, fmt.Errorf("carries a user name or password; a cluster's credential is stored, never written into its address")
	case u.RawQuery != "" || u.Fragment != "":
		return nil, fmt.Errorf("carries a query or fragment")
	}
	return u, nil
}

// HasCapability checks the declared set AND the structural assertion.
func (c *Cluster) HasCapability(name capability.Name) bool {
	return c.Declares(name) && capability.Implements(c, name)
}

// KubeAPIServer returns the validated API server URL.
func (c *Cluster) KubeAPIServer() string { return c.server.String() }

// KubeNamespace returns the record's namespace, or "default".
func (c *Cluster) KubeNamespace() string {
	if ns, _ := c.Properties().String(NamespaceProperty); ns != "" {
		return ns
	}
	return defaultNamespace
}

// KubeEKS returns the EKS cluster name and region the record names, both
// empty for a cluster that is not EKS.
func (c *Cluster) KubeEKS() (cluster, region string) {
	cluster, _ = c.Properties().String(EKSClusterProperty)
	region, _ = c.Properties().String(AWSRegionProperty)
	return cluster, region
}

// SharedAcrossRuns implements inventory.Shared: many runs may work on one
// cluster at once, since Kubernetes' own optimistic concurrency settles
// concurrent writers, so no Pleiades lease serializes them.
func (c *Cluster) SharedAcrossRuns() bool { return true }

// TLSSettings returns the cluster's validated TLS settings: its pinned
// authority and the name its certificate is checked against.
func (c *Cluster) TLSSettings() devicetls.Settings { return c.tls }

// The discovery facts the cluster's accessors read, which onboarding's
// probe writes.
const (
	FactKinds          = "kinds"
	FactKindsTruncated = "kinds_truncated"
	FactPartialGroups  = "partial_groups"
)

// ServedKinds returns what onboarding found the cluster serves, as
// "group/version Kind", and the group versions whose discovery failed.
// complete is false when there is nothing to go by: before onboarding,
// when the discovery no longer holds for the record's address, or when
// the server named more kinds than a discovery keeps.
func (c *Cluster) ServedKinds() (kinds, partialGroups []string, complete bool) {
	if c.stale != "" {
		return nil, nil, false
	}
	d, ok, err := inventory.DiscoveryFrom(c.Properties())
	if err != nil || !ok {
		return nil, nil, false
	}
	kinds = record.DiscoveredList(c.Properties(), FactKinds)
	if truncated, _ := d.Facts[FactKindsTruncated].(bool); truncated || len(kinds) == 0 {
		return nil, nil, false
	}
	return kinds, record.DiscoveredList(c.Properties(), FactPartialGroups), true
}

// DiscoveryBinding implements inventory.DiscoveryBinder.
func (c *Cluster) DiscoveryBinding() string {
	return record.DiscoveryBinding(TypeCluster, c.Properties())
}

// StaleDiscovery implements inventory.StaleDiscoverer.
func (c *Cluster) StaleDiscovery() string { return c.stale }

func init() {
	record.RegisterType(TypeCluster, NewCluster)
	record.RegisterOnboardedType(TypeCluster)
	record.RegisterOwnCredentialType(TypeCluster)
	record.RegisterDiscoveryRules(TypeCluster, record.DiscoveryRules{
		Grants: []capability.Name{capability.NameKubernetes},
		// What the discovery proved holds only for this address and this
		// authority; changing either asks for onboarding again.
		Bound: append([]string{APIServerProperty, NodeEngineHostProperty, EKSClusterProperty, AWSRegionProperty}, devicetls.Properties()...),
	})
	record.RegisterDispatchProperties(TypeCluster, append([]string{APIServerProperty, NamespaceProperty, NodeEngineHostProperty, EKSClusterProperty, AWSRegionProperty}, devicetls.Properties()...)...)
}
