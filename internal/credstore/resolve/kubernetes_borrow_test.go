// Tests of what decides whether a stored Kubernetes credential may be lent to
// a cluster device with none of its own.
package resolve_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore/resolve"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// borrowCA and otherCA stand in for two authorities' PEM text; the rule
// compares them as text and never parses them.
const (
	borrowCA = "-----BEGIN CERTIFICATE-----\nbmF0aXZlLWNsdXN0ZXItY2E=\n-----END CERTIFICATE-----"
	otherCA  = "-----BEGIN CERTIFICATE-----\nYXR0YWNrZXItY2E=\n-----END CERTIFICATE-----"
)

// TestBorrowedKubernetesSecrets_StaysInsideTheOrganization: another
// organization's credential for the same API server is never lent. Before
// 2026-10-14 the search covered every organization, so a device naming that
// address onboarded with the other organization's token.
func TestBorrowedKubernetesSecrets_StaysInsideTheOrganization(t *testing.T) {
	resolver, store, client, orgID, _ := fixture(t)
	ctx := context.Background()
	other := client.Organization.Create().SetName("other").SaveX(ctx)
	typ, err := store.EnsureManagedType(ctx, mustManaged(t, "kubernetes_bearer_token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCredential(ctx, other.ID, typ.ID, "theirs", "", map[string]string{"host": "https://prod.example.com:6443", "bearer_token": "their-token-1234"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := resolve.BorrowedKubernetesSecrets(ctx, store, resolver, cluster("https://prod.example.com:6443", orgID))
	if got != nil || err != nil {
		t.Fatalf("a cluster of another organization was lent %v, %v", got, err)
	}
	if got, err := resolve.BorrowedKubernetesSecrets(ctx, store, resolver, cluster("https://prod.example.com:6443", other.ID)); err != nil || got[wire.SecretBearerToken] != "their-token-1234" {
		t.Errorf("the credential's own organization = %v, %v", got, err)
	}
}

// TestBorrowedKubernetesSecrets_RefusesWhatTheRecordWouldDecide: the record is
// written by whoever writes inventory, so nothing in it decides where a lent
// credential goes. A device in no organization, a routed device, and a
// pinned authority the credential does not carry are each refused by name;
// an authority the credential does carry is accepted.
func TestBorrowedKubernetesSecrets_RefusesWhatTheRecordWouldDecide(t *testing.T) {
	resolver, store, _, orgID, _ := fixture(t)
	ctx := context.Background()
	typ, err := store.EnsureManagedType(ctx, mustManaged(t, "kubernetes_bearer_token"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCredential(ctx, orgID, typ.ID, "prod", "", map[string]string{"host": "https://prod.example.com:6443", "bearer_token": "prod-token-1234", "ssl_ca_cert": borrowCA}, nil); err != nil {
		t.Fatal(err)
	}
	const server = "https://prod.example.com:6443"
	for _, tc := range []struct {
		name    string
		cluster resolve.BorrowingCluster
		refusal string
	}{
		{"no organization", resolve.BorrowingCluster{Name: "c", APIServer: server}, "belongs to no organization"},
		{"routed", resolve.BorrowingCluster{Name: "c", APIServer: server, OrganizationID: orgID, Routed: true}, "route of SSH hops"},
		{"another authority", resolve.BorrowingCluster{Name: "c", APIServer: server, OrganizationID: orgID, RecordCAPEM: otherCA}, "pins an authority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve.BorrowedKubernetesSecrets(ctx, store, resolver, tc.cluster)
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.refusal) {
				t.Fatalf("got %v, %v; want a refusal naming %q", got, err, tc.refusal)
			}
		})
	}
	got, err := resolve.BorrowedKubernetesSecrets(ctx, store, resolver, resolve.BorrowingCluster{Name: "c", APIServer: server, OrganizationID: orgID, RecordCAPEM: borrowCA + "\n"})
	if err != nil || got[wire.SecretBearerToken] != "prod-token-1234" || got[wire.SecretClusterCAPEM] != borrowCA {
		t.Errorf("a record pinning the credential's own authority = %v, %v", got, err)
	}
}
