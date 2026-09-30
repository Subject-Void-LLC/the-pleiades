package credtype_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// FuzzInjectDeclaresEverySecretItRenders establishes the security property
// the whole masking chain downstream of this package depends on.
//
// The property: if a rendered value CONTAINS a secret input's value, that
// rendered value is declared secret by the artifact, and if it does not, it
// is not.
//
// Both halves matter and they fail in opposite ways. Missing a value means
// a secret crosses the wire with nothing telling the adapter to mask it, so
// a module echoing it back leaks it. Declaring too much means an ordinary
// URL or region is scrubbed out of every later line in the Runner for the
// rest of its life, which corrupts output without protecting anything, and
// which is exactly the defect the release gate caught in this phase's first
// implementation.
//
// A table cannot establish this. The interesting cases are the ones nobody
// thinks to write: a template whose output happens to contain the secret by
// coincidence, a secret that is a substring of a non-secret input, a
// rendered value assembled from both.
func FuzzInjectDeclaresEverySecretItRenders(f *testing.F) {
	f.Add("a-real-bearer-token", "https://api.example.test", "{{ api_token }}", "{{ api_url }}")
	f.Add("a-real-bearer-token", "https://api.example.test", "Bearer {{ api_token }}", "prefix-{{ api_url }}")
	f.Add("short", "https://api.example.test", "{{ api_token }}", "{{ api_url }}")
	f.Add("https://api.example.test", "https://api.example.test", "{{ api_token }}", "{{ api_url }}")
	f.Add("a-real-bearer-token", "a-real-bearer-token-and-more", "{{ api_token }}", "{{ api_url }}")
	f.Add("", "", "{{ api_token }}", "{{ api_url }}")
	f.Add("a-real-bearer-token", "u", "{{ api_token }}{{ api_url }}", "{{ api_url | default('x') }}")

	f.Fuzz(func(t *testing.T, secretValue, plainValue, secretTemplate, plainTemplate string) {
		// Templates are bounded so the fuzzer spends its time on values
		// and shapes rather than on the renderer's own size limits, which
		// internal/render's own fuzz target already covers.
		if len(secretTemplate) > 256 || len(plainTemplate) > 256 {
			t.Skip("a template past the 256-byte bound this target keeps to")
		}

		in, err := credtype.NewInjector(render.New(), credtype.WithLiterals(isolatedLiterals(t)))
		if err != nil {
			t.Fatalf("NewInjector() error = %v", err)
		}

		cred := credtype.Credential{
			ID:   1,
			Name: "fuzz",
			Type: credtype.CredentialType{
				Name: "Fuzz", Kind: credtype.KindCloud, Namespace: "fuzz",
				Inputs: credtype.InputSchema{Fields: []credtype.InputField{
					{ID: "api_token", Label: "Token", Secret: true},
					{ID: "api_url", Label: "URL"},
				}},
				Injectors: credtype.Injectors{Env: map[string]string{
					"SECRET_TARGET": secretTemplate,
					"PLAIN_TARGET":  plainTemplate,
				}},
			},
			Inputs: map[string]string{"api_token": secretValue, "api_url": plainValue},
		}

		art, err := in.Inject([]credtype.Credential{cred}, nil)
		if err != nil {
			// A template naming something undeclared, or one the grammar
			// refuses, is an ordinary refusal rather than a counterexample.
			return
		}

		declared := make(map[string]bool, len(art.SecretValues()))
		for _, v := range art.SecretValues() {
			declared[v] = true
		}

		for name, rendered := range art.Env {
			carriesSecret := secretValue != "" && strings.Contains(rendered, secretValue)

			switch {
			case carriesSecret && len(rendered) >= redact.MinLiteralLength && !declared[rendered]:
				// The dangerous direction: a value carrying the secret,
				// long enough to mask, that nothing downstream will mask.
				t.Fatalf("%s rendered %q, which contains the secret %q and was not declared secret; declared=%v",
					name, rendered, secretValue, art.SecretValues())
			case !carriesSecret && declared[rendered]:
				// The corrupting direction: an ordinary value that would be
				// scrubbed out of every later line for no reason.
				t.Fatalf("%s rendered %q, which does not contain the secret %q and was declared secret anyway",
					name, rendered, secretValue)
			}
		}

		// The secret input's own value is always declared when it is long
		// enough to mask, whether or not any template happened to use it:
		// it is about to be handled by code that can log it.
		if len(secretValue) >= redact.MinLiteralLength && !declared[secretValue] {
			t.Fatalf("the secret input's own value %q was not declared secret; declared=%v",
				secretValue, art.SecretValues())
		}
	})
}

// FuzzCombineNeverSilentlyPicksAWinner establishes the property that makes
// a collision an error rather than a coin toss.
//
// The property: for any two artifacts, either Combine refuses, or every key
// in the result came from exactly one of them with its value intact. There
// is no third outcome where a value is quietly replaced, which is what AWX
// does and what this platform's whole ignored-fields design rests on not
// doing.
func FuzzCombineNeverSilentlyPicksAWinner(f *testing.F) {
	f.Add("A", "one", "A", "two")
	f.Add("A", "one", "B", "two")
	f.Add("A", "one", "A", "one")
	f.Add("", "", "", "")

	f.Fuzz(func(t *testing.T, firstName, firstValue, secondName, secondValue string) {
		if !validEnvName(firstName) || !validEnvName(secondName) {
			t.Skip("a variable name the injector refuses, which injectors_fuzz_test.go covers")
		}

		in, err := credtype.NewInjector(render.New(), credtype.WithLiterals(isolatedLiterals(t)))
		if err != nil {
			t.Fatalf("NewInjector() error = %v", err)
		}

		build := func(id int, name, envName, value string) credtype.Credential {
			return credtype.Credential{
				ID: id, Name: name,
				Type: credtype.CredentialType{
					Name: "Fuzz", Kind: credtype.KindCloud, Namespace: "fuzz",
					Inputs:    credtype.InputSchema{Fields: []credtype.InputField{{ID: "value", Label: "Value"}}},
					Injectors: credtype.Injectors{Env: map[string]string{envName: "{{ value }}"}},
				},
				Inputs: map[string]string{"value": value},
			}
		}

		art, err := in.Inject([]credtype.Credential{
			build(1, "first", firstName, firstValue),
			build(2, "second", secondName, secondValue),
		}, nil)
		if err != nil {
			// A collision refused, which is the outcome this property
			// permits. What it does not permit is a silent resolution.
			if firstName != secondName {
				t.Fatalf("Inject() refused two credentials injecting different variables: %v", err)
			}
			return
		}

		if firstName == secondName {
			t.Fatalf("Inject() accepted two credentials both injecting %q and produced %v", firstName, art.Env)
		}
		if art.Env[firstName] != firstValue {
			t.Fatalf("%s = %q, want %q", firstName, art.Env[firstName], firstValue)
		}
		if art.Env[secondName] != secondValue {
			t.Fatalf("%s = %q, want %q", secondName, art.Env[secondName], secondValue)
		}
	})
}

// validEnvName reports whether name is one the injector would accept, so
// the fuzzer spends its budget on collision behaviour rather than on the
// name validator injectors_fuzz_test.go already covers.
func validEnvName(name string) bool {
	if name == "" || len(name) > 32 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	// The reserved names have their own coverage, and hitting one here
	// would only re-prove that check.
	return !strings.HasPrefix(name, "BASH_FUNC_") &&
		name != "PATH" && name != "HOME" && name != "IFS" && name != "ENV"
}

// isolatedLiterals returns a masking set no other test shares, so a
// fuzzer's own registrations cannot accumulate into the process-wide one.
func isolatedLiterals(t *testing.T) *redact.Literals {
	t.Helper()

	masker, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() error = %v", err)
	}
	return masker.Literals()
}
