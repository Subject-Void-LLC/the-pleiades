// Tests of the Kubernetes credential kind through the real injector and the
// two shipped types: what each execution path receives, and what is
// refused.
package credtype_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// kubernetesCredential is a credential of the shipped type namespace.
func kubernetesCredential(t *testing.T, id int, namespace string, inputs map[string]string) credtype.Credential {
	t.Helper()
	ct, ok := managed.Type(namespace)
	if !ok {
		t.Fatalf("the catalog does not ship %q", namespace)
	}
	return (credtype.Credential{ID: id, Name: namespace, Type: ct, Inputs: inputs}).WithDefaults()
}

// injectAll renders creds through the real injector.
func injectAll(t *testing.T, creds ...credtype.Credential) (credtype.Artifact, error) {
	t.Helper()
	in, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatal(err)
	}
	return in.Inject(creds, nil)
}

const testCA = "-----BEGIN CERTIFICATE-----\nMIIBcluster\n-----END CERTIFICATE-----\n"

// TestKubernetesBearerToken_EachPath: one credential, read directly by the
// native path as a cluster's secrets and turned into AWX's environment on
// the Ansible path, with its secrets masked.
func TestKubernetesBearerToken_EachPath(t *testing.T) {
	art, err := injectAll(t, kubernetesCredential(t, 7, "kubernetes_bearer_token", map[string]string{
		"host": "https://k8s.example.com:6443", "bearer_token": "token-abcdefgh", "verify_ssl": "true", "ssl_ca_cert": testCA,
	}))
	if err != nil {
		t.Fatal(err)
	}
	k, ok := art.Kubernetes()
	if !ok {
		t.Fatal("the artifact carries no Kubernetes credential")
	}
	if len(art.Env) != 0 || len(art.Files) != 0 {
		t.Errorf("the type's own document injected env %v and files %v; it is read in code", art.Env, art.Files)
	}

	secrets := k.Secrets()
	if secrets[wire.SecretBearerToken] != "token-abcdefgh" || secrets[wire.SecretClusterCAPEM] != strings.TrimSpace(testCA) {
		t.Errorf("native secrets = %v", secrets)
	}

	env, files := k.AnsibleInjection()
	want := map[string]string{credtype.K8sAuthHost: "https://k8s.example.com:6443", credtype.K8sAuthAPIKey: "token-abcdefgh",
		credtype.K8sAuthVerifySSL: "True", credtype.K8sAuthCACert: credtype.FilePath(7, "ssl_ca_cert")}
	for name, value := range want {
		if env[name] != value {
			t.Errorf("%s = %q, want %q", name, env[name], value)
		}
	}
	if len(files) != 1 || files[0].Path != env[credtype.K8sAuthCACert] || files[0].Content != strings.TrimSpace(testCA) || files[0].Mode != credtype.FileMode {
		t.Errorf("files = %+v, want the CA at the path K8S_AUTH_SSL_CA_CERT names", files)
	}

	masked := strings.Join(art.SecretValues(), "\n")
	if !strings.Contains(masked, "token-abcdefgh") || !strings.Contains(masked, "MIIBcluster") {
		t.Errorf("the token and the authority are not both masked")
	}
}

// TestKubernetesBearerToken_Verification is AWX's verify branch, and the
// one place this platform departs from it: with verification on and no
// authority, AWX quietly sets K8S_AUTH_VERIFY_SSL False; this platform
// keeps it True, checked against the system's roots. Only verify_ssl false
// turns it off.
func TestKubernetesBearerToken_Verification(t *testing.T) {
	for _, tc := range []struct {
		name, verify, ca, want string
		caFile                 bool
	}{
		{"verify with an authority", "true", testCA, "True", true},
		{"verify with no authority (AWX: False)", "true", "", "True", false},
		{"verify unset, as AWX's default", "", "", "True", false},
		{"verify off", "false", testCA, "False", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputs := map[string]string{"host": "k8s.example.com", "bearer_token": "token-abcdefgh"}
			if tc.verify != "" {
				inputs["verify_ssl"] = tc.verify
			}
			if tc.ca != "" {
				inputs["ssl_ca_cert"] = tc.ca
			}
			art, err := injectAll(t, kubernetesCredential(t, 1, "kubernetes_bearer_token", inputs))
			if err != nil {
				t.Fatal(err)
			}
			k, _ := art.Kubernetes()
			env, files := k.AnsibleInjection()
			if env[credtype.K8sAuthVerifySSL] != tc.want {
				t.Errorf("K8S_AUTH_VERIFY_SSL = %q, want %q", env[credtype.K8sAuthVerifySSL], tc.want)
			}
			if _, set := env[credtype.K8sAuthCACert]; set != tc.caFile || (len(files) == 1) != tc.caFile {
				t.Errorf("CA file written = %v (%d files), want %v", set, len(files), tc.caFile)
			}
		})
	}
}

// TestKubernetesClientCertificate_EachPath: the x509 type reaches the
// native path as a certificate and key, and the Ansible path as the two
// files kubernetes.core reads.
func TestKubernetesClientCertificate_EachPath(t *testing.T) {
	art, err := injectAll(t, kubernetesCredential(t, 3, "pleiades_kubernetes_client_certificate", map[string]string{
		"host": "https://k8s.example.com", "client_certificate": "CERT-PEM-BODY", "client_key": "KEY-PEM-BODY-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	k, _ := art.Kubernetes()
	secrets := k.Secrets()
	if secrets[wire.SecretCertificatePEM] != "CERT-PEM-BODY" || secrets[wire.SecretPrivateKeyPEM] != "KEY-PEM-BODY-secret" || secrets[wire.SecretBearerToken] != "" {
		t.Errorf("native secrets = %v", secrets)
	}
	env, files := k.AnsibleInjection()
	if _, token := env[credtype.K8sAuthAPIKey]; token || len(files) != 2 {
		t.Fatalf("env %v with %d files, want the certificate and key files and no token", env, len(files))
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Content
	}
	if byPath[env[credtype.K8sAuthCertFile]] != "CERT-PEM-BODY" || byPath[env[credtype.K8sAuthKeyFile]] != "KEY-PEM-BODY-secret" {
		t.Errorf("K8S_AUTH_CERT_FILE and K8S_AUTH_KEY_FILE do not name the certificate and key: %v", env)
	}
	if !strings.Contains(strings.Join(art.SecretValues(), "\n"), "KEY-PEM-BODY-secret") {
		t.Error("the client key is not masked")
	}
}

// TestKubernetesCredential_Refusals: what cannot be a cluster credential is
// refused when the job is rendered, naming the credential.
func TestKubernetesCredential_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name, namespace string
		inputs          map[string]string
		want            string
	}{
		{"plain http", "kubernetes_bearer_token", map[string]string{"host": "http://k8s.example.com", "bearer_token": "token-abcdefgh"}, "not https"},
		{"a certificate with no key", "pleiades_kubernetes_client_certificate", map[string]string{"host": "k8s.example.com", "client_certificate": "CERT", "client_key": " "}, "needs both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := injectAll(t, kubernetesCredential(t, 1, tc.namespace, tc.inputs))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.namespace) {
				t.Fatalf("Inject = %v, want a refusal saying %q and naming the credential", err, tc.want)
			}
		})
	}
}

// TestKubernetesCredential_Conflicts: a run carries one cluster credential,
// and AWX's environment may not collide with what another credential
// injects.
func TestKubernetesCredential_Conflicts(t *testing.T) {
	one := kubernetesCredential(t, 1, "kubernetes_bearer_token", map[string]string{"host": "a.example.com", "bearer_token": "token-aaaaaaaa"})
	two := kubernetesCredential(t, 2, "kubernetes_bearer_token", map[string]string{"host": "b.example.com", "bearer_token": "token-bbbbbbbb"})
	if _, err := injectAll(t, one, two); !errors.Is(err, credtype.ErrInjectorConflict) {
		t.Errorf("two Kubernetes credentials: %v, want a conflict", err)
	}

	custom := credtype.Credential{ID: 9, Name: "custom", Type: credtype.CredentialType{
		Name: "Custom", Kind: credtype.KindCloud, Namespace: "",
		Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "endpoint", Label: "Endpoint"}}},
		Injectors: credtype.Injectors{Env: map[string]string{credtype.K8sAuthHost: "{{ endpoint }}"}},
	}, Inputs: map[string]string{"endpoint": "https://elsewhere.example.com"}}
	if _, err := injectAll(t, one, custom); !errors.Is(err, credtype.ErrInjectorConflict) || !strings.Contains(err.Error(), credtype.K8sAuthHost) {
		t.Errorf("a custom type setting %s beside a Kubernetes credential: %v, want a conflict naming it", credtype.K8sAuthHost, err)
	}
}

// TestKubernetesCredential_ForServer: a credential is for the API server
// its host names, whichever way either is written, and for no other.
func TestKubernetesCredential_ForServer(t *testing.T) {
	art, err := injectAll(t, kubernetesCredential(t, 1, "kubernetes_bearer_token", map[string]string{"host": "K8S.example.com:443", "bearer_token": "token-abcdefgh"}))
	if err != nil {
		t.Fatal(err)
	}
	k, _ := art.Kubernetes()
	for server, want := range map[string]bool{
		"https://k8s.example.com":         true,
		"https://k8s.example.com:443/":    true,
		"https://k8s.example.com:6443":    false,
		"https://other.example.com":       false,
		"https://k8s.example.com.evil.io": false,
		"":                                false,
	} {
		if got := k.ForServer(server); got != want {
			t.Errorf("ForServer(%q) = %v, want %v", server, got, want)
		}
	}
}
