// Tests that bound database connections reach the dispatch payload as
// named, typed entries apart from the machine credential.
package dispatch

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// TestApplyInjection_CarriesDatabaseConnectionsApart: a template's two
// database connections ride the dispatch by name, beside the machine
// credential and never inside it.
func TestApplyInjection_CarriesDatabaseConnectionsApart(t *testing.T) {
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
	database := func(id int, name, host, password string) credtype.Credential {
		return credtype.Credential{ID: id, Name: name, Type: credtype.CredentialType{
			Name: "PostgreSQL Connection", Kind: credtype.KindDatabase, Namespace: "pleiades_postgres", Managed: true,
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{{ID: "name", Label: "Name"}, {ID: "host", Label: "Host"}, {ID: "username", Label: "Username"}, {ID: "password", Label: "Password", Secret: true}}},
		}, Inputs: map[string]string{"name": name, "host": host, "username": "app", "password": password}}
	}
	art, err := in.Inject([]credtype.Credential{machine, database(2, "appdb", "db.internal", "db-secret-1"), database(3, "scratch", "localhost", "db-secret-2")}, nil)
	if err != nil {
		t.Fatal(err)
	}

	// A server: a machine credential reaches only a device it can log in to.
	payload := wire.DispatchPayload{DeviceType: "linux_server", Capabilities: []capability.Name{capability.NameSSHTransport}}
	ApplyInjection(&payload, art)
	if len(payload.Databases) != 2 {
		t.Fatalf("payload.Databases = %+v", payload.Databases)
	}
	if appdb, ok := wire.FindDatabaseConnection(payload.Databases, "appdb"); !ok || appdb.Password != "db-secret-1" || appdb.Host != "db.internal" {
		t.Errorf("appdb = %+v, %v", appdb, ok)
	}
	if payload.Secrets[wire.SecretPassword] != "ssh-secret" {
		t.Errorf("the machine credential was lost: %v", payload.Secrets)
	}
	for k, v := range payload.Secrets {
		if v == "db-secret-1" || v == "db-secret-2" {
			t.Errorf("a database password is in the machine secrets under %q", k)
		}
	}
	for _, want := range []string{"db-secret-1", "db-secret-2"} {
		found := false
		for _, v := range art.SecretValues() {
			found = found || v == want
		}
		if !found {
			t.Errorf("the artifact does not name %q among its secret values", want)
		}
	}
}
