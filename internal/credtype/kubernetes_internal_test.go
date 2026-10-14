// In-package tests of the Kubernetes credential kind's refusals and its
// host matching, beyond what the shipped types' tests reach.
package credtype

import (
	"errors"
	"strings"
	"testing"
)

// kubernetesCred is a Kubernetes kind credential with inputs.
func kubernetesCred(inputs map[string]string) Credential {
	return Credential{ID: 4, Name: "c", Type: CredentialType{Kind: KindKubernetes}, Inputs: inputs}
}

// TestKubernetesCredentialOf_Refusals: each input set that cannot be a
// cluster credential is refused, naming why.
func TestKubernetesCredentialOf_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		cred Credential
		want string
	}{
		{"another kind", Credential{Name: "c", Type: CredentialType{Kind: KindCloud}}, "not a Kubernetes one"},
		{"no host", kubernetesCred(map[string]string{"bearer_token": "t"}), "needs its API server"},
		{"a host that is no URL", kubernetesCred(map[string]string{"host": "https://", "bearer_token": "t"}), "not a URL with a host"},
		{"a token and a certificate", kubernetesCred(map[string]string{"host": "k.example", "bearer_token": "t", "client_certificate": "C", "client_key": "K"}), "not both"},
		{"no identity", kubernetesCred(map[string]string{"host": "k.example"}), "needs a bearer token or a client certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := KubernetesCredentialOf(tc.cred)
			if !errors.Is(err, ErrInjection) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("KubernetesCredentialOf = %v, want an injection refusal saying %q", err, tc.want)
			}
		})
	}
}

// TestForServerHost: matching from the visible host input alone, as a
// lookup over redacted credentials does; a credential whose host does not
// parse matches nothing.
func TestForServerHost(t *testing.T) {
	if !ForServerHost("k.example:6443", "https://K.example:6443/") {
		t.Error("the same server written two ways did not match")
	}
	if ForServerHost("k.example", "https://k.example:6443") {
		t.Error("443 matched 6443")
	}
	if ForServerHost("http://k.example", "https://k.example") || ForServerHost("", "https://k.example") {
		t.Error("a credential whose host is not an https server matched")
	}
}

// TestSecretTrackingKeepsItsTargetsKey: the decorator every target is
// wrapped in reports the wrapped target's key, which a log line and an
// error name.
func TestSecretTrackingKeepsItsTargetsKey(t *testing.T) {
	wrapped := secretTracking{inner: kubernetesTarget{}}
	if wrapped.Key() != "kubernetes" || wrapped.Phase() != PhaseValues {
		t.Errorf("wrapped target reports %q in phase %v", wrapped.Key(), wrapped.Phase())
	}
}
