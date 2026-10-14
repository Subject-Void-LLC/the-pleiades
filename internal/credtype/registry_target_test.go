// Tests for the managed registry type's target: one registry account per
// job, carried as a typed entry rather than among the machine secrets.
package credtype_test

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// registryCredential builds a registry credential for host.
func registryCredential(id int, name, host string) credtype.Credential {
	return credtype.Credential{
		ID:   id,
		Name: name,
		Type: credtype.CredentialType{
			Name: "Container Registry", Kind: credtype.KindRegistry, Namespace: "registry", Managed: true,
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "host", Label: "Authentication URL"},
				{ID: "username", Label: "Username"},
				{ID: "password", Label: "Password or Token", Secret: true},
			}},
		},
		Inputs: map[string]string{"host": host, "username": "deployer", "password": "token-" + name},
	}
}

// TestRegistryTarget_CarriesTheAccountForItsHost: a registry credential
// becomes the artifact's one registry account, its host normalized, and
// nothing about it reaches the machine identity, the environment or the
// extra variables, so no method but a pull or push can read it.
func TestRegistryTarget_CarriesTheAccountForItsHost(t *testing.T) {
	in, _ := newTestInjector(t)
	art, err := in.InjectOne(registryCredential(1, "ghcr", "https://GHCR.io/"), nil)
	if err != nil {
		t.Fatal(err)
	}
	reg, ok := art.Registry()
	if !ok || reg.Host != "ghcr.io" || reg.Username != "deployer" || reg.Password != "token-ghcr" {
		t.Fatalf("Registry() = %+v, %v", reg, ok)
	}
	if _, ok := art.Machine(); ok {
		t.Error("a registry credential became the run's machine identity")
	}
	if len(art.Env) != 0 || len(art.ExtraVars) != 0 || len(art.Files) != 0 {
		t.Errorf("a registry credential reached the environment, extra vars or files: %+v", art)
	}
}

// TestRegistryTarget_RefusesNoHost: an account with no registry could be
// sent anywhere, so it is refused.
func TestRegistryTarget_RefusesNoHost(t *testing.T) {
	in, _ := newTestInjector(t)
	if _, err := in.InjectOne(registryCredential(1, "nohost", " "), nil); err == nil {
		t.Fatal("a registry credential with no host was accepted")
	}
}

// TestCombineRefusesTwoRegistryAccounts: a run carries one registry
// account until a task can name which it uses.
func TestCombineRefusesTwoRegistryAccounts(t *testing.T) {
	_, err := injectTwo(t, registryCredential(1, "ghcr", "ghcr.io"), registryCredential(2, "quay", "quay.io"))
	if !errors.Is(err, credtype.ErrInjectorConflict) {
		t.Fatalf("Inject() of two registry accounts = %v, want ErrInjectorConflict", err)
	}
}
