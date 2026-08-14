package credtype_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// What these measure, and against what.
//
// AWX renders a credential type's injectors in Python, with Jinja2, once
// per launch, inside the same process that serves the web request. That is
// the comparison that matters, and internal/render's own benchmarks already
// establish the renderer half of it: 498x faster to compile and 70x to
// render than Python's jinja2 3.1.6 on this machine.
//
// These measure the whole injection: validating a credential against its
// type, rendering every target, resolving the reserved filename namespace,
// registering what is secret, and combining. That is one dispatch's worth
// of work in the Controller, and it happens once per JOB rather than once
// per device (see internal/dispatch's own fan-out for why), so the number
// that matters is per-launch rather than per-host.
//
// Measured on an Intel i7-8700K, go1.x, this machine:
//
//	BenchmarkInjectFullType            14,782 ns/op   7,316 B/op    99 allocs/op
//	BenchmarkInjectThreeCredentials    27,817 ns/op  13,107 B/op   184 allocs/op
//	BenchmarkParallelInject             7,327 ns/op   7,407 B/op    99 allocs/op
//	BenchmarkValidateCredentialType     4,401 ns/op     380 B/op    18 allocs/op
//
// Two things are worth reading off those figures rather than leaving to be
// rediscovered. A full injection of the parity corpus's own three-credential
// template costs 28 microseconds per launch, against AWX's per-launch Python
// Jinja2 render of the same documents; the renderer half of that comparison
// is 70x, measured directly in internal/render's own benchmarks. And the
// parallel number is HALF the serial one rather than worse, which is the
// answer to the only real concern here: the render cache behind this is
// mutex-guarded and sits on the fan-out path every replica runs, and a
// parallel figure worse than serial would mean the cache was the constraint
// rather than the work.

// benchType is a realistic custom type: two inputs, three environment
// variables, a nested extra-variable tree and a generated file.
func benchType() credtype.CredentialType {
	return credtype.CredentialType{
		Name:      "Custom REST API Token",
		Kind:      credtype.KindCloud,
		Namespace: "custom_api_token",
		Inputs: credtype.InputSchema{
			Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
				{ID: "api_url", Label: "URL"},
			},
			Required: []string{"api_token", "api_url"},
		},
		Injectors: credtype.Injectors{
			Env: map[string]string{
				"REST_API_TOKEN":  "{{ api_token }}",
				"REST_API_URL":    "{{ api_url }}",
				"REST_API_AUTH":   "Bearer {{ api_token }}",
				"REST_API_CONFIG": "{{ tower.filename }}",
			},
			ExtraVars: map[string]any{
				"ansible_api_url": "{{ api_url }}",
				"nested":          map[string]any{"token": "{{ api_token }}"},
			},
			File: map[string]string{"template": "token={{ api_token }}\nurl={{ api_url }}\n"},
		},
	}
}

func benchCredential(id int) credtype.Credential {
	return credtype.Credential{
		ID:     id,
		Name:   "prod api",
		Type:   benchType(),
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}
}

// benchInjector builds an injector over an isolated masking set, so the
// benchmark does not grow the process-wide one on every iteration and
// measure the growth instead of the work.
func benchInjector(b *testing.B) *credtype.Injector {
	b.Helper()

	masker, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		b.Fatalf("NewMasker() error = %v", err)
	}
	in, err := credtype.NewInjector(render.New(), credtype.WithLiterals(masker.Literals()))
	if err != nil {
		b.Fatalf("NewInjector() error = %v", err)
	}
	return in
}

// BenchmarkInjectFullType is one launch's worth of injection for one
// credential exercising every target.
func BenchmarkInjectFullType(b *testing.B) {
	in := benchInjector(b)
	creds := []credtype.Credential{benchCredential(18)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := in.Inject(creds, nil); err != nil {
			b.Fatalf("Inject() error = %v", err)
		}
	}
}

// BenchmarkInjectThreeCredentials is the parity corpus's own shape: a
// template binding a machine credential, a vault credential and a cloud
// credential at once, which is the case this platform could not express at
// all before this phase.
func BenchmarkInjectThreeCredentials(b *testing.B) {
	in := benchInjector(b)

	creds := []credtype.Credential{
		{
			ID: 3, Name: "machine",
			Type: credtype.CredentialType{
				Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
				Inputs: credtype.InputSchema{Fields: []credtype.InputField{
					{ID: "username", Label: "Username"},
					{ID: "password", Label: "Password", Secret: true},
				}},
			},
			Inputs: map[string]string{"username": "operator", "password": "a-real-password"},
		},
		{
			ID: 9, Name: "prod vault",
			Type: credtype.CredentialType{
				Name: "Vault", Kind: credtype.KindVault, Namespace: "vault", Managed: true,
				Inputs: credtype.InputSchema{Fields: []credtype.InputField{
					{ID: "vault_password", Label: "Password", Secret: true},
					{ID: "vault_id", Label: "Identifier"},
				}},
			},
			Inputs: map[string]string{"vault_password": "a-real-vault-password", "vault_id": "prod"},
		},
		benchCredential(18),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := in.Inject(creds, nil); err != nil {
			b.Fatalf("Inject() error = %v", err)
		}
	}
}

// BenchmarkParallelInject proves the injector is not a bottleneck under a
// real Controller's concurrency.
//
// It matters because injection sits on the fan-out path, which every
// replica runs, and the render cache behind it is mutex-guarded. A number
// here materially worse than the serial one would mean the cache is the
// constraint rather than the work, which is what internal/render's own
// parallel benchmark already established it is not.
func BenchmarkParallelInject(b *testing.B) {
	in := benchInjector(b)
	creds := []credtype.Credential{benchCredential(18)}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := in.Inject(creds, nil); err != nil {
				b.Fatalf("Inject() error = %v", err)
			}
		}
	})
}

// BenchmarkValidateCredentialType measures the SAVE path rather than the
// launch path: compiling every injector template and checking every name it
// references against the schema.
//
// It is worth its own number because it is what moves the strict-undefined
// failure from launch time to save time (Architecture Principle 5), and a
// save-time check nobody would tolerate the cost of would end up skipped.
func BenchmarkValidateCredentialType(b *testing.B) {
	ct := benchType()
	engine := render.New()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ct.Validate(engine); err != nil {
			b.Fatalf("Validate() error = %v", err)
		}
	}
}
