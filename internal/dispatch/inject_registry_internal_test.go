// Tests that a bound registry credential reaches the dispatch payload as its
// one typed, masked entry.
package dispatch

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/kubernetes"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestApplyInjection_CarriesTheRegistryAccountApart: a template's registry
// credential rides the dispatch in its own field, beside the machine
// credential and never inside it, so a method that does not pull never sees
// it in its secrets.
func TestApplyInjection_CarriesTheRegistryAccountApart(t *testing.T) {
	masker, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatal(err)
	}
	in, err := credtype.NewInjector(render.New(), credtype.WithLiterals(masker.Literals()))
	if err != nil {
		t.Fatal(err)
	}
	machine := credtype.Credential{ID: 1, Name: "machine", Type: credtype.CredentialType{
		Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{{ID: "username", Label: "Username"}, {ID: "password", Label: "Password", Secret: true}}},
	}, Inputs: map[string]string{"username": "ops", "password": "ssh-secret"}}
	registry := credtype.Credential{ID: 2, Name: "ghcr", Type: credtype.CredentialType{
		Name: "Container Registry", Kind: credtype.KindRegistry, Namespace: "registry", Managed: true,
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{{ID: "host", Label: "Host"}, {ID: "username", Label: "Username"}, {ID: "password", Label: "Password", Secret: true}}},
	}, Inputs: map[string]string{"host": "ghcr.io", "username": "deployer", "password": "registry-secret"}}
	art, err := in.Inject([]credtype.Credential{machine, registry}, nil)
	if err != nil {
		t.Fatal(err)
	}

	payload := wire.DispatchPayload{DeviceType: "linux_server", Capabilities: []capability.Name{capability.NameSSHTransport}}
	ApplyInjection(&payload, art)
	if payload.Registry == nil || payload.Registry.Host != "ghcr.io" || payload.Registry.Password != "registry-secret" {
		t.Fatalf("payload.Registry = %+v", payload.Registry)
	}
	if payload.Secrets[wire.SecretPassword] != "ssh-secret" {
		t.Errorf("the machine credential was lost: %v", payload.Secrets)
	}
	for k, v := range payload.Secrets {
		if v == "registry-secret" {
			t.Errorf("the registry password is in the machine secrets under %q", k)
		}
	}
}

// TestApplyInjection_AMachineCredentialNeverAuthenticatesACluster: a
// template's SSH login reaches every machine in the fan-out but not a
// Kubernetes cluster, whose payload keeps no secrets from it, so the
// cluster's own stored credential is what the fallback supplies.
func TestApplyInjection_AMachineCredentialNeverAuthenticatesACluster(t *testing.T) {
	in, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatal(err)
	}
	machine := credtype.Credential{ID: 1, Name: "machine", Type: credtype.CredentialType{
		Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
		Inputs: credtype.InputSchema{Fields: []credtype.InputField{{ID: "username", Label: "Username"}, {ID: "password", Label: "Password", Secret: true}}},
	}, Inputs: map[string]string{"username": "ops", "password": "ssh-secret"}}
	art, err := in.Inject([]credtype.Credential{machine}, nil)
	if err != nil {
		t.Fatal(err)
	}

	server := wire.DispatchPayload{DeviceType: "linux_server", Capabilities: []capability.Name{capability.NameSSHTransport}}
	ApplyInjection(&server, art)
	if server.Secrets[wire.SecretPassword] != "ssh-secret" {
		t.Errorf("a server did not get the template's machine credential: %v", server.Secrets)
	}
	cluster := wire.DispatchPayload{DeviceType: kubernetes.TypeCluster}
	ApplyInjection(&cluster, art)
	if cluster.Secrets != nil {
		t.Errorf("a cluster was given the template's machine credential: %d secrets", len(cluster.Secrets))
	}
}
