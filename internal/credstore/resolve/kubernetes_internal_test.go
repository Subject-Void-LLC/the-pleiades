// In-package tests of BorrowedKubernetesSecrets' failures, through fakes of the
// two ports it takes.
package resolve

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

type fakeLister struct {
	creds []credstore.Credential
	err   error
}

func (f fakeLister) ListAllCredentials(context.Context) ([]credstore.Credential, error) {
	return f.creds, f.err
}

type fakeResolver struct {
	creds []credtype.Credential
	err   error
}

func (f fakeResolver) Resolve(context.Context, []int) ([]credtype.Credential, error) {
	return f.creds, f.err
}

// TestKubernetesSecrets_Failures: a store that cannot list, a resolver that
// fails or answers with the wrong count, and a stored credential that is
// not a usable cluster credential are each an error naming what failed,
// never an absent credential.
func TestKubernetesSecrets_Failures(t *testing.T) {
	const server = "https://k.example:6443"
	match := fakeLister{creds: []credstore.Credential{{ID: 1, Name: "prod", OrganizationID: 7, Kind: credtype.KindKubernetes, Inputs: map[string]string{"host": "k.example:6443"}}}}
	broken := errors.New("store down")
	for _, tc := range []struct {
		name     string
		lister   CredentialLister
		resolver CredentialResolver
		want     string
	}{
		{"the list fails", fakeLister{err: broken}, fakeResolver{}, "listing stored credentials"},
		{"the resolve fails", match, fakeResolver{err: broken}, `resolving credential "prod"`},
		{"the resolve answers twice", match, fakeResolver{creds: []credtype.Credential{{}, {}}}, "returned 2 credentials"},
		{"the credential has no identity", match, fakeResolver{creds: []credtype.Credential{{Name: "prod", Type: credtype.CredentialType{Kind: credtype.KindKubernetes},
			Inputs: map[string]string{"host": "k.example:6443"}}}}, "needs a bearer token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BorrowedKubernetesSecrets(context.Background(), tc.lister, tc.resolver, BorrowingCluster{Name: "c", APIServer: server, OrganizationID: 7})
			if err == nil || got != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("BorrowedKubernetesSecrets = %v, %v; want an error saying %q", got, err, tc.want)
			}
		})
	}
}
