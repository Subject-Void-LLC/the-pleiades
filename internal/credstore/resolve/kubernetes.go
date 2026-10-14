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

// KubernetesSecrets returns the secrets of the one stored Kubernetes
// credential whose host names apiServer, as a cluster device's own
// (credtype.KubernetesCredential.Secrets), or nil when none does. Only the
// match is decrypted: the search reads the host input, which is not
// secret. Two matches are refused, naming both, rather than one chosen:
// which credential a cluster uses is the operator's to say, and until a
// binding can say it (credential slots) the platform does not guess.
func KubernetesSecrets(ctx context.Context, list CredentialLister, resolver CredentialResolver, apiServer string) (map[string]string, error) {
	all, err := list.ListAllCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing stored credentials: %w", err)
	}
	var matches []credstore.Credential
	for _, c := range all {
		if c.Kind == credtype.KindKubernetes && credtype.ForServerHost(c.Inputs[credtype.KubernetesInputHost], apiServer) {
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
