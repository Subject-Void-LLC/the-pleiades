// A cluster's credential from the credential store, for a caller with no
// template to name one: onboarding a Kubernetes cluster through the
// Controller.
package resolve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// CredentialLister lists every stored credential, with secret inputs
// redacted (credstore.Store's own ListAllCredentials).
type CredentialLister interface {
	ListAllCredentials(ctx context.Context) ([]credstore.Credential, error)
}

// CredentialResolver returns credentials with their real values
// (Resolver's Resolve).
type CredentialResolver interface {
	Resolve(ctx context.Context, ids []int) ([]credtype.Credential, error)
}

// BorrowingCluster is a cluster device that has no credential of its own,
// described by what decides whether a stored credential may be lent to it.
type BorrowingCluster struct {
	// Name is the device's name, for messages.
	Name string
	// APIServer is the device's api_server.
	APIServer string
	// OrganizationID is the organization the device belongs to; zero when it
	// belongs to none.
	OrganizationID int
	// RecordCAPEM is the authority the device's own record pins
	// (tls_ca_pem), or empty.
	RecordCAPEM string
	// Routed says the device is reached through a route of SSH hops.
	Routed bool
}

// BorrowedKubernetesSecrets returns the secrets of the one stored Kubernetes
// credential that may be lent to cluster, which has none of its own, or nil
// when none matches. The credential must belong to the cluster's own
// organization and name its API server; and because the device's record is
// written by whoever can write inventory while the credential was stored by
// whoever could write credentials, nothing from the record may decide where
// the lent credential goes: a routed cluster is refused, since its hops would
// carry the token, and an authority the record pins must be the credential's
// own, since the record's alone would let its writer answer for the cluster.
//
// Before 2026-10-14 the search covered every organization's credentials and
// the record's authority won, so a device naming another organization's
// cluster address onboarded with that organization's credential, and one
// with its own authority and route could receive its token.
func BorrowedKubernetesSecrets(ctx context.Context, list CredentialLister, resolver CredentialResolver, cluster BorrowingCluster) (map[string]string, error) {
	if cluster.OrganizationID == 0 {
		return nil, fmt.Errorf("cluster %s belongs to no organization, so no stored credential is lent to it; store its own credential, or add it to the organization whose credential names it", cluster.Name)
	}
	if cluster.Routed {
		return nil, fmt.Errorf("cluster %s is reached through a route of SSH hops, and a credential that is not its own is never sent through one; store the cluster's own credential", cluster.Name)
	}
	secrets, err := kubernetesSecrets(ctx, list, resolver, cluster.APIServer, cluster.OrganizationID)
	if err != nil || secrets == nil {
		return nil, err
	}
	if record := strings.TrimSpace(cluster.RecordCAPEM); record != "" && record != strings.TrimSpace(secrets[wire.SecretClusterCAPEM]) {
		return nil, fmt.Errorf("cluster %s pins an authority its organization's stored credential does not carry; a credential that is not the cluster's own is verified only against its own authority (put it in the credential's ssl_ca_cert, or store the cluster's own credential)", cluster.Name)
	}
	return secrets, nil
}

// kubernetesSecrets returns the secrets of the one stored Kubernetes
// credential of organization orgID whose host names apiServer, as a cluster
// device's own (credtype.KubernetesCredential.Secrets), or nil when none
// does. Only the match is decrypted: the search reads the host input, which
// is not secret. Two matches are refused, naming both, rather than one
// chosen: which credential a cluster uses is the operator's to say, and
// until a binding can say it (credential slots) the platform does not guess.
func kubernetesSecrets(ctx context.Context, list CredentialLister, resolver CredentialResolver, apiServer string, orgID int) (map[string]string, error) {
	all, err := list.ListAllCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing stored credentials: %w", err)
	}
	var matches []credstore.Credential
	for _, c := range all {
		if c.OrganizationID == orgID && c.Kind == credtype.KindKubernetes && credtype.ForServerHost(c.Inputs[credtype.KubernetesInputHost], apiServer) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
	default:
		names := make([]string, len(matches))
		for i, c := range matches {
			names[i] = fmt.Sprintf("%q", c.Name)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("stored Kubernetes credentials %s all name %s; keep one, or store the cluster's own credential for it", strings.Join(names, ", "), apiServer)
	}
	resolved, err := resolver.Resolve(ctx, []int{matches[0].ID})
	if err != nil {
		return nil, fmt.Errorf("resolving credential %q: %w", matches[0].Name, err)
	}
	if len(resolved) != 1 {
		return nil, fmt.Errorf("resolving credential %q returned %d credentials", matches[0].Name, len(resolved))
	}
	k, err := credtype.KubernetesCredentialOf(resolved[0])
	if err != nil {
		return nil, err
	}
	return k.Secrets(), nil
}
