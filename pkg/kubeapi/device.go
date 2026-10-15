// Sessions for a device: the endpoint from a cluster's record, the
// identity from its credential, and nothing else.
package kubeapi

import (
	"context"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/awscloud"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ParamNamespace is the task parameter naming the namespace a task works
// in; the cluster's own namespace when absent.
const ParamNamespace = "namespace"

// EndpointOf reads how device is reached from its record: the API server,
// the pinned authority and the name its certificate is checked against.
func EndpointOf(device inventory.InventoryItem) (Endpoint, error) {
	cluster, ok := device.(capability.KubernetesCapable)
	if !ok {
		return Endpoint{}, fmt.Errorf("device %q is not a Kubernetes cluster", device.Name())
	}
	settings := devicetls.For(device)
	return Endpoint{Server: cluster.KubeAPIServer(), CAPEM: settings.CAPEM(), ServerName: settings.ServerName}, nil
}

// IdentityOf reads who a session authenticates as from a cluster's
// credential: a bearer token, or a client certificate and its key. Anything
// else is refused by name: Kubernetes removed password authentication, and
// a PKCS#12 bundle or an SSH key is some other device's credential.
func IdentityOf(device string, secrets map[string]string) (Identity, error) {
	token := secrets[wire.SecretBearerToken]
	cert, key := secrets[wire.SecretCertificatePEM], secrets[wire.SecretPrivateKeyPEM]
	switch {
	case secrets[wire.SecretPassword] != "":
		return Identity{}, fmt.Errorf("the credential for cluster %q holds a password; Kubernetes authenticates a token or a client certificate (store one with `pleiades add-credential %s --token-stdin` or `--certificate --key`)", device, device)
	case secrets[wire.SecretPFXBase64] != "":
		return Identity{}, fmt.Errorf("the credential for cluster %q is a PKCS#12 bundle; store the client certificate and its key as PEM (`--certificate --key`)", device)
	case token != "" && (cert != "" || key != ""):
		return Identity{}, fmt.Errorf("the credential for cluster %q holds both a token and a client certificate; a cluster credential carries one", device)
	case token != "":
		return Identity{Token: token}, nil
	case cert != "" && key != "":
		return Identity{ClientCertPEM: []byte(cert), ClientKeyPEM: []byte(key)}, nil
	case key != "":
		return Identity{}, fmt.Errorf("the credential for cluster %q holds a private key with no client certificate; an SSH key does not authenticate to a cluster", device)
	}
	return Identity{}, fmt.Errorf("no credential for cluster %q: store its token or client certificate with `pleiades add-credential`", device)
}

// eksCluster is a cluster record that may name an Amazon EKS cluster
// (kubernetes_cluster's eks_cluster_name and aws_region).
type eksCluster interface {
	KubeEKS() (cluster, region string)
}

// identityFor is IdentityOf, except that a record naming an EKS cluster
// authenticates with tokens minted from its credential read as an AWS key,
// a fresh one before each nears its expiry (awscloud.EKSToken).
func identityFor(device inventory.InventoryItem, secrets map[string]string) (Identity, error) {
	eks, ok := device.(eksCluster)
	if !ok {
		return IdentityOf(device.Name(), secrets)
	}
	cluster, region := eks.KubeEKS()
	if cluster == "" {
		return IdentityOf(device.Name(), secrets)
	}
	keyID, secret := secrets[awscloud.SecretAccessKeyID], secrets[awscloud.SecretSecretAccessKey]
	switch {
	case secrets[wire.SecretBearerToken] != "" || secrets[wire.SecretCertificatePEM] != "":
		return Identity{}, fmt.Errorf("cluster %q is EKS cluster %s, which authenticates with an AWS key, but its credential holds a token or a client certificate", device.Name(), cluster)
	case keyID == "" || secret == "":
		return Identity{}, fmt.Errorf("no AWS key for EKS cluster %q: store its access key ID and secret access key with `pleiades add-credential %s --username <access key id> --password-stdin`", device.Name(), device.Name())
	}
	session := secrets[awscloud.SecretSessionToken]
	maskSecrets(secret, session)
	return Identity{Mint: func(ctx context.Context) (string, time.Time, error) {
		return awscloud.EKSToken(ctx, region, cluster, keyID, secret, session, time.Now())
	}}, nil
}

// ForDevice builds a session for device from its record and secrets alone,
// without asking whether onboarding granted KubernetesCapable: onboarding
// itself needs one to decide. namespace empty means the cluster's own.
func ForDevice(device inventory.InventoryItem, secrets map[string]string, namespace string) (*Session, error) {
	return ForDeviceVia(device, secrets, namespace, nil)
}

// ForDeviceVia is ForDevice reaching the API server through dial, the
// device's route of SSH hops (a bastion), or directly when dial is nil.
func ForDeviceVia(device inventory.InventoryItem, secrets map[string]string, namespace string, dial sdk.RouteDial) (*Session, error) {
	ep, err := EndpointOf(device)
	if err != nil {
		return nil, err
	}
	ep.Dial = dial
	// A template's credential may carry the authority for a cluster whose
	// record pins none; a pinned one always wins.
	if len(ep.CAPEM) == 0 && secrets[wire.SecretClusterCAPEM] != "" {
		ep.CAPEM = []byte(secrets[wire.SecretClusterCAPEM])
	}
	id, err := identityFor(device, secrets)
	if err != nil {
		return nil, err
	}
	if namespace == "" {
		namespace = device.(capability.KubernetesCapable).KubeNamespace()
	}
	if !ValidNamespace(namespace) {
		return nil, fmt.Errorf("namespace %q is not a Kubernetes namespace name", namespace)
	}
	return NewSession(ep, id, DefaultLimits, namespace)
}

// ValidNamespace reports whether s is a Kubernetes namespace name: an RFC
// 1123 label, at most 63 lower-case alphanumerics and hyphens, starting and
// ending with an alphanumeric.
func ValidNamespace(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, c := range s {
		alnum := c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !alnum && (c != '-' || i == 0 || i == len(s)-1) {
			return false
		}
	}
	return true
}

// Open is a Collection method's session for its task: the device must
// carry KubernetesCapable, which onboarding grants, and the credential is
// the run's (rc.InjectSecrets), never anything ambient.
func Open(_ context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any, fqcn string) (*Session, error) {
	if device == nil {
		return nil, fmt.Errorf("%s: no target device: a Kubernetes method addresses a cluster, set the task's target", fqcn)
	}
	if !device.HasCapability(capability.NameKubernetes) {
		return nil, fmt.Errorf("%s: device %q is not an onboarded Kubernetes cluster; run `pleiades onboard %s`", fqcn, device.Name(), device.Name())
	}
	// A cluster behind a bastion is reached through the device's route,
	// which the run hands over (sdk.Router); otherwise directly.
	var dial sdk.RouteDial
	if r, ok := rc.(sdk.Router); ok {
		dial = r.DeviceRoute()
	}
	s, err := ForDeviceVia(device, rc.InjectSecrets(), sdk.StringParam(params, ParamNamespace), dial)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fqcn, err)
	}
	return s, nil
}
