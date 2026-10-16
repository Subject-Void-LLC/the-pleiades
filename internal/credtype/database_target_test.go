// Tests for the managed database connection type's target and binding
// rule: several connections per job, one per name, carried as typed
// values rather than among the machine secrets.
package credtype_test

import (
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// databaseCredential builds a database connection credential.
func databaseCredential(id int, credName string, inputs map[string]string) credtype.Credential {
	fields := []credtype.InputField{}
	for _, f := range []string{"name", "host", "port", "username", "password", "sslmode", "ca_pem", "server_name", "insecure_transport", "live"} {
		fields = append(fields, credtype.InputField{ID: f, Label: f, Secret: f == "password"})
	}
	return credtype.Credential{
		ID: id, Name: credName,
		Type: credtype.CredentialType{
			Name: "PostgreSQL Connection", Kind: credtype.KindDatabase, Namespace: "pleiades_postgres", Managed: true,
			Inputs: credtype.InputSchema{Fields: fields},
		},
		Inputs: inputs,
	}
}

// TestDatabaseTarget_CarriesNamedConnections: two connections become two
// named values on the artifact, with their port, TLS and live databases,
// and none of it reaches the machine identity, the environment, the extra
// variables or a file.
func TestDatabaseTarget_CarriesNamedConnections(t *testing.T) {
	art, err := injectTwo(t,
		databaseCredential(1, "prod", map[string]string{"name": "appdb", "host": "db.internal", "port": "6432", "username": "app", "password": "pw-prod", "live": "app, billing"}),
		databaseCredential(2, "scratch", map[string]string{"name": "scratch", "host": "/var/run/postgresql", "username": "postgres", "sslmode": "disable"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	dbs := art.Databases()
	if len(dbs) != 2 {
		t.Fatalf("Databases() = %+v", dbs)
	}
	byName := map[string]int{dbs[0].Name: 0, dbs[1].Name: 1}
	app := dbs[byName["appdb"]]
	if app.Host != "db.internal" || app.Port != 6432 || app.User != "app" || app.Password != "pw-prod" || !app.IsLive("billing") || app.EffectiveSSLMode() != "verify-full" {
		t.Errorf("appdb = %+v", app)
	}
	if scratch := dbs[byName["scratch"]]; !scratch.Socket() || scratch.Password != "" {
		t.Errorf("scratch = %+v", scratch)
	}
	if _, ok := art.Machine(); ok {
		t.Error("a database connection became the run's machine identity")
	}
	if len(art.Env) != 0 || len(art.ExtraVars) != 0 || len(art.Files) != 0 {
		t.Errorf("a database connection reached the environment, extra vars or files: %+v", art)
	}
}

// TestDatabaseTarget_RefusesWhatNoRunCouldUse holds a bound connection to
// the rules a stored one is held to, and refuses two of one name.
func TestDatabaseTarget_RefusesWhatNoRunCouldUse(t *testing.T) {
	in, _ := newTestInjector(t)
	for name, inputs := range map[string]map[string]string{
		"plaintext across a network":  {"name": "a", "host": "10.0.0.5", "username": "u", "sslmode": "disable"},
		"a port that is not a number": {"name": "a", "host": "h", "username": "u", "port": "five"},
		"no name":                     {"host": "h", "username": "u"},
	} {
		if _, err := in.InjectOne(databaseCredential(1, "bad", inputs), nil); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := in.InjectOne(databaseCredential(1, "remote", map[string]string{"name": "a", "host": "10.0.0.5", "username": "u", "sslmode": "disable", "insecure_transport": "true"}), nil); err != nil {
		t.Errorf("plaintext asked for by name: %v", err)
	}
	_, err := injectTwo(t,
		databaseCredential(1, "one", map[string]string{"name": "appdb", "host": "a", "username": "u"}),
		databaseCredential(2, "two", map[string]string{"name": "appdb", "host": "b", "username": "u"}))
	if !errors.Is(err, credtype.ErrInjectorConflict) {
		t.Fatalf("two connections named appdb = %v, want ErrInjectorConflict", err)
	}
}

// TestCheckBinding_DatabasesBindOncePerName: like vault credentials,
// database connections bind once per name, not once per kind.
func TestCheckBinding_DatabasesBindOncePerName(t *testing.T) {
	bound := []credtype.Bound{
		{CredentialID: 1, CredentialName: "prod", Kind: credtype.KindDatabase, DatabaseName: "appdb"},
		{CredentialID: 2, CredentialName: "scratch", Kind: credtype.KindDatabase, DatabaseName: "scratch"},
		{CredentialID: 3, CredentialName: "machine", Kind: credtype.KindSSH},
	}
	if err := credtype.CheckBinding(bound); err != nil {
		t.Fatalf("two connections of different names = %v", err)
	}
	err := credtype.CheckBinding(append(bound, credtype.Bound{CredentialID: 4, CredentialName: "prod-again", Kind: credtype.KindDatabase, DatabaseName: "appdb"}))
	if !errors.Is(err, credtype.ErrBindingConflict) {
		t.Fatalf("two connections named appdb = %v, want ErrBindingConflict", err)
	}
	if got := databaseCredential(1, "prod", map[string]string{"name": " appdb "}).Bound().DatabaseName; got != "appdb" {
		t.Errorf("Bound().DatabaseName = %q", got)
	}
}
