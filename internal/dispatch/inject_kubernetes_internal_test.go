// Tests that a template's Kubernetes credential reaches each execution path
// in the form that path reads, and reaches only the cluster it names.
package dispatch

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/playbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds/runbook"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// kubernetesArtifact renders a machine credential and a bearer token for
// https://k8s.example.com:6443 through the real injector.
func kubernetesArtifact(t *testing.T) credtype.Artifact {
	t.Helper()
	in, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatal(err)
	}
	machine := credtype.Credential{ID: 1, Name: "machine", Type: credtype.CredentialType{
		Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{{ID: "username", Label: "Username"}, {ID: "password", Label: "Password", Secret: true}}},
	}, Inputs: map[string]string{"username": "ops", "password": "ssh-secret"}}
	ct, _ := managed.Type("kubernetes_bearer_token")
	token := (credtype.Credential{ID: 2, Name: "prod cluster", Type: ct, Inputs: map[string]string{
		"host": "https://k8s.example.com:6443", "bearer_token": "cluster-token-1234", "ssl_ca_cert": "CA-PEM",
	}}).WithDefaults()
	art, err := in.Inject([]credtype.Credential{machine, token}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return art
}

// clusterPayload is a native dispatch to a cluster at server.
func clusterPayload(kind, server string) wire.DispatchPayload {
	return wire.DispatchPayload{Kind: kind, DeviceType: kubernetes.TypeCluster,
		DeviceProperties: map[string]any{kubernetes.APIServerProperty: server}}
}

// TestApplyInjection_ATokenAuthenticatesTheClusterItNames: on the native
// path the token is the secrets of the cluster whose API server it names,
// in place of that cluster's own; another cluster, and every machine, never
// receive it.
func TestApplyInjection_ATokenAuthenticatesTheClusterItNames(t *testing.T) {
	art := kubernetesArtifact(t)

	named := clusterPayload(runbook.Kind, "https://k8s.example.com:6443")
	ApplyInjection(&named, art)
	if named.Secrets[wire.SecretBearerToken] != "cluster-token-1234" || named.Secrets[wire.SecretClusterCAPEM] != "CA-PEM" {
		t.Errorf("the named cluster's secrets = %v", named.Secrets)
	}
	if named.Secrets[wire.SecretPassword] != "" {
		t.Error("the named cluster was given the machine credential")
	}
	if named.Injected != nil && (len(named.Injected.Env) > 0 || len(named.Injected.Files) > 0) {
		t.Errorf("the native path was given Ansible environment %v", named.Injected.Env)
	}

	other := clusterPayload(runbook.Kind, "https://staging.example.com:6443")
	ApplyInjection(&other, art)
	if other.Secrets != nil {
		t.Errorf("a cluster the credential does not name received secrets: %d", len(other.Secrets))
	}

	server := wire.DispatchPayload{Kind: runbook.Kind, DeviceType: "linux_server"}
	ApplyInjection(&server, art)
	if server.Secrets[wire.SecretPassword] != "ssh-secret" || server.Secrets[wire.SecretBearerToken] != "" {
		t.Errorf("a server's secrets = %v, want the machine credential alone", server.Secrets)
	}
}

// TestApplyInjection_TheAnsiblePathGetsAWXsEnvironment: a playbook job gets
// the token as AWX sets it for kubernetes.core, masked, with the authority
// in a file.
func TestApplyInjection_TheAnsiblePathGetsAWXsEnvironment(t *testing.T) {
	art := kubernetesArtifact(t)
	payload := wire.DispatchPayload{Kind: playbook.Kind, DeviceType: "linux_server"}
	ApplyInjection(&payload, art)
	if payload.Injected == nil {
		t.Fatal("a playbook job received no injection")
	}
	env := payload.Injected.Env
	if env[credtype.K8sAuthHost] != "https://k8s.example.com:6443" || env[credtype.K8sAuthAPIKey] != "cluster-token-1234" || env[credtype.K8sAuthVerifySSL] != "True" {
		t.Errorf("env = %v", env)
	}
	if len(payload.Injected.Files) != 1 || payload.Injected.Files[0].Path != env[credtype.K8sAuthCACert] || payload.Injected.Files[0].Content != "CA-PEM" {
		t.Errorf("files = %+v, want the authority where K8S_AUTH_SSL_CA_CERT points", payload.Injected.Files)
	}
	masked := false
	for _, m := range payload.Injected.Mask {
		masked = masked || m == "cluster-token-1234"
	}
	if !masked {
		t.Error("the token is not in the injection's mask")
	}
}
