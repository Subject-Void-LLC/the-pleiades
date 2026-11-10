// This file holds every refusal built from a credential's input values to
// naming the input and the rule it breaks, never the value: a custom type may
// mark any input secret, and a refusal reaches an API response and a job's
// failure reason, where nothing masks it.
package credtype_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
)

// quoteCanary is the value each case stores in the input it breaks. It is
// lowercase letters and underscores, so it passes as a connection name and a
// vault identifier where a case needs it to.
const quoteCanary = "sk_live_quote_canary_58c1"

// allSecret marks every input of cred's type secret, as a custom type may.
func allSecret(cred credtype.Credential) credtype.Credential {
	fields := append([]credtype.InputField(nil), cred.Type.Inputs.Fields...)
	for i := range fields {
		fields[i].Secret = true
	}
	cred.Type.Inputs.Fields = fields
	return cred
}

// TestNoInjectionRefusalQuotesAnInputValue injects one credential per case
// whose input breaks a rule, with every input secret.
func TestNoInjectionRefusalQuotesAnInputValue(t *testing.T) {
	in, _ := newTestInjector(t)
	db := func(inputs map[string]string) credtype.Credential {
		base := map[string]string{"name": quoteCanary, "host": "db.internal", "username": "app"}
		for k, v := range inputs {
			base[k] = v
		}
		return allSecret(databaseCredential(1, "conn", base))
	}
	for name, tc := range map[string]struct {
		cred  credtype.Credential
		input string
	}{
		"a database port that is not a number": {db(map[string]string{"port": quoteCanary}), "port"},
		"a database host sent in the clear":    {db(map[string]string{"host": quoteCanary + ".example", "sslmode": "disable"}), "host"},
		"a database sslmode that is not one":   {db(map[string]string{"sslmode": quoteCanary}), "sslmode"},
		"a database name that is not one":      {db(map[string]string{"name": quoteCanary + "!"}), "name"},
		"a live database that is not one":      {db(map[string]string{"live": quoteCanary + string(rune(1))}), "live"},
		"a Kubernetes host that is not https": {allSecret(kubernetesCredential(t, 2, "kubernetes_bearer_token",
			map[string]string{"host": "http://" + quoteCanary + ".example", "bearer_token": "a-token"})), "host"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := in.InjectOne(tc.cred, nil)
			if err == nil {
				t.Fatal("InjectOne() accepted the credential")
			}
			if strings.Contains(err.Error(), quoteCanary) {
				t.Errorf("the refusal quotes the input's value: %v", err)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.input)) {
				t.Errorf("the refusal does not name input %q: %v", tc.input, err)
			}
		})
	}
}

// TestNoConflictQuotesAnIdentifier binds and injects two credentials sharing
// an identifier their type marks secret, which a type stored before
// Validate refused that could do: the binding rule and the injector each
// refuse the pair without quoting it.
func TestNoConflictQuotesAnIdentifier(t *testing.T) {
	vault := func(id int) credtype.Credential {
		return allSecret(credtype.Credential{
			ID: id, Name: fmt.Sprintf("vault %d", id),
			Type: credtype.CredentialType{Name: "Vault", Kind: credtype.KindVault, Namespace: "vault", Inputs: credtype.InputSchema{
				Fields: []credtype.InputField{{ID: credtype.VaultIdentifierInput, Label: "ID"}, {ID: credtype.VaultPasswordInput, Label: "Password"}},
			}},
			Inputs: map[string]string{credtype.VaultIdentifierInput: quoteCanary, credtype.VaultPasswordInput: fmt.Sprintf("password-%d", id)},
		})
	}
	database := func(id int) credtype.Credential {
		return allSecret(databaseCredential(id, fmt.Sprintf("conn %d", id), map[string]string{"name": quoteCanary, "host": "db.internal", "username": "app"}))
	}
	for name, pair := range map[string][2]credtype.Credential{
		"two vault credentials":    {vault(1), vault(2)},
		"two database connections": {database(1), database(2)},
	} {
		t.Run(name, func(t *testing.T) {
			bindErr := credtype.CheckBinding([]credtype.Bound{pair[0].Bound(), pair[1].Bound()})
			_, injectErr := injectTwo(t, pair[0], pair[1])
			for what, err := range map[string]error{"the binding rule": bindErr, "the injector": injectErr} {
				if err == nil {
					t.Fatalf("%s accepted the pair", what)
				}
				if !errors.Is(err, credtype.ErrBindingConflict) && !errors.Is(err, credtype.ErrInjectorConflict) {
					t.Errorf("%s refused the pair with %v, want a conflict", what, err)
				}
				if strings.Contains(err.Error(), quoteCanary) {
					t.Errorf("%s's refusal quotes the identifier: %v", what, err)
				}
			}
		})
	}
}
