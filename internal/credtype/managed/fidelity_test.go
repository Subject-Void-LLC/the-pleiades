package managed_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// The fidelity tests: what a shipped managed type actually injects,
// compared against what AWX injects for the same inputs.
//
// These run the REAL injector rather than reading the JSON documents back,
// which is the whole point. A test asserting that aws.json contains the
// string AWS_SESSION_TOKEN proves the file has not been edited; it does not
// prove that a credential of that type produces that variable, and the
// second is what decides whether a migrated playbook runs.
//
// The expectations are transcriptions of AWX's own behaviour, taken from
// awx_plugins.credentials.injectors for the types AWX implements in Python
// and from awx_plugins.credentials.plugins for the ones it implements as
// data. They are not captured from a running deployment, and the same
// caveat that cmd/runner/testdata/README.md records applies here: this is
// AWX's documented and published behaviour, which is a weaker claim than a
// capture and a stronger one than a guess.

// inject renders one credential of a shipped type through the real
// injector.
func inject(t *testing.T, namespace string, inputs map[string]string) credtype.Artifact {
	t.Helper()

	ct, ok := managed.Type(namespace)
	if !ok {
		t.Fatalf("the catalog does not ship %q", namespace)
	}
	in, err := credtype.NewInjector(render.New())
	if err != nil {
		t.Fatalf("NewInjector() error = %v", err)
	}
	art, err := in.Inject([]credtype.Credential{
		(credtype.Credential{ID: 1, Name: namespace + " credential", Type: ct, Inputs: inputs}).WithDefaults(),
	}, nil)
	if err != nil {
		t.Fatalf("Inject(%s) error = %v", namespace, err)
	}
	return art
}

// TestAWSMatchesAWXWithAndWithoutASessionToken is the test that justifies
// credtype.Injectors.OmitEmpty existing at all.
//
// AWX's aws injector sets AWS_SECURITY_TOKEN and AWS_SESSION_TOKEN only
// when has_input('security_token') is true. Setting them to the empty
// string instead is not a cosmetic difference: botocore treats a present
// but empty session token as a credential to use and fails the request
// with InvalidClientTokenId rather than falling back to the key and
// secret. So both directions are asserted, and the second is the one a
// regression would break.
func TestAWSMatchesAWXWithAndWithoutASessionToken(t *testing.T) {
	t.Parallel()

	t.Run("with an sts token every variable is set", func(t *testing.T) {
		t.Parallel()

		art := inject(t, "aws", map[string]string{
			"username":       "AKIAIOSFODNN7EXAMPLE",
			"password":       "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"security_token": "FwoGZXIvYXdzEBYaDEXAMPLETOKEN",
		})

		want := map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIAIOSFODNN7EXAMPLE",
			"AWS_SECRET_ACCESS_KEY": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			"AWS_SECURITY_TOKEN":    "FwoGZXIvYXdzEBYaDEXAMPLETOKEN",
			"AWS_SESSION_TOKEN":     "FwoGZXIvYXdzEBYaDEXAMPLETOKEN",
		}
		if !reflect.DeepEqual(art.Env, want) {
			t.Errorf("Env = %v, want %v", art.Env, want)
		}
	})

	t.Run("without one the token variables are absent rather than empty", func(t *testing.T) {
		t.Parallel()

		art := inject(t, "aws", map[string]string{
			"username": "AKIAIOSFODNN7EXAMPLE",
			"password": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		})

		want := map[string]string{
			"AWS_ACCESS_KEY_ID":     "AKIAIOSFODNN7EXAMPLE",
			"AWS_SECRET_ACCESS_KEY": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		}
		if !reflect.DeepEqual(art.Env, want) {
			t.Errorf("Env = %v, want %v", art.Env, want)
		}
		// Stated separately from the comparison above, because this is the
		// assertion with a consequence and a reader should not have to
		// infer it from a DeepEqual.
		for _, name := range []string{"AWS_SECURITY_TOKEN", "AWS_SESSION_TOKEN"} {
			if v, set := art.Env[name]; set {
				t.Errorf("%s = %q, want it unset: an empty session token fails authentication rather than being ignored", name, v)
			}
		}
	})
}

// TestControllerMatchesAWXIncludingItsBlankOptionals covers the one AWX
// managed type whose injector document this platform copies exactly.
//
// The blanks are the interesting half. An operator authenticating with an
// OAuth token supplies no username or password, and AWX still SETS
// TOWER_USERNAME and TOWER_PASSWORD, to the empty string. Refusing to
// render them, which is what strict-undefined did before RenderVars seeded
// the declared inputs, would have failed every such job.
func TestControllerMatchesAWXIncludingItsBlankOptionals(t *testing.T) {
	t.Parallel()

	art := inject(t, "controller", map[string]string{
		"host":        "https://aap.example.test",
		"oauth_token": "an-oauth-token",
		"verify_ssl":  "true",
	})

	want := map[string]string{
		"TOWER_HOST":                 "https://aap.example.test",
		"TOWER_USERNAME":             "",
		"TOWER_PASSWORD":             "",
		"TOWER_VERIFY_SSL":           "True",
		"TOWER_OAUTH_TOKEN":          "an-oauth-token",
		"CONTROLLER_HOST":            "https://aap.example.test",
		"CONTROLLER_USERNAME":        "",
		"CONTROLLER_PASSWORD":        "",
		"CONTROLLER_VERIFY_SSL":      "True",
		"CONTROLLER_OAUTH_TOKEN":     "an-oauth-token",
		"CONTROLLER_REQUEST_TIMEOUT": "10",
		"AAP_HOSTNAME":               "https://aap.example.test",
		"AAP_USERNAME":               "",
		"AAP_PASSWORD":               "",
		"AAP_VALIDATE_CERTS":         "True",
		"AAP_TOKEN":                  "an-oauth-token",
		"AAP_REQUEST_TIMEOUT":        "10",
	}
	if !reflect.DeepEqual(art.Env, want) {
		t.Errorf("Env = %v, want %v", art.Env, want)
	}

	wantVars := map[string]any{
		"aap_hostname":        "https://aap.example.test",
		"aap_username":        "",
		"aap_password":        "",
		"aap_token":           "an-oauth-token",
		"aap_request_timeout": "10",
		"aap_validate_certs":  "True",
	}
	if !reflect.DeepEqual(art.ExtraVars, wantVars) {
		t.Errorf("ExtraVars = %v, want %v", art.ExtraVars, wantVars)
	}
}

// TestBooleanInputsRenderInPythonCapitalisation pins the fidelity decision
// RenderVars documents, in both the set and unset directions.
//
// A playbook migrated from AWX may compare an injected value against the
// literal "True", because AWX's injector namespace holds a real Python
// bool. Rendering "true" here would break that comparison silently, which
// is the worst available shape: the job runs, the condition is false, and
// nothing reports an error.
func TestBooleanInputsRenderInPythonCapitalisation(t *testing.T) {
	t.Parallel()

	set := inject(t, "controller", map[string]string{"host": "https://aap.example.test", "verify_ssl": "true"})
	if got := set.Env["TOWER_VERIFY_SSL"]; got != "True" {
		t.Errorf("a true boolean rendered as %q, want %q", got, "True")
	}

	// Unset, not false: AWX defaults a missing boolean input to False
	// rather than leaving it undefined, so the variable is present.
	unset := inject(t, "controller", map[string]string{"host": "https://aap.example.test"})
	if got, ok := unset.Env["TOWER_VERIFY_SSL"]; !ok || got != "False" {
		t.Errorf("an unset boolean rendered as %q (present=%v), want %q and present", got, ok, "False")
	}
}

// TestHCPTerraformFillsItsHostnameDefault covers the declared default
// reaching a template, which is the only reason this type needs no
// conditional at all where AWX's Python uses get_input(default=...).
func TestHCPTerraformFillsItsHostnameDefault(t *testing.T) {
	t.Parallel()

	art := inject(t, "hcp_terraform", map[string]string{"token": "a-terraform-token"})

	want := map[string]string{"TF_TOKEN": "a-terraform-token", "TF_HOSTNAME": "app.terraform.io"}
	if !reflect.DeepEqual(art.Env, want) {
		t.Errorf("Env = %v, want %v", art.Env, want)
	}
}

// TestMachineAndNetworkCredentialsBothReachTheTransport is the assertion
// behind machineTarget accepting two kinds.
//
// A network credential storing a username and a key that nothing read
// would be FAILURE_PATTERNS.md #116's shape, and the operator's symptom
// would be an authentication failure against a device whose credential
// they filled in correctly.
func TestMachineAndNetworkCredentialsBothReachTheTransport(t *testing.T) {
	t.Parallel()

	for _, namespace := range []string{"ssh", "net"} {
		t.Run(namespace, func(t *testing.T) {
			t.Parallel()

			art := inject(t, namespace, map[string]string{
				"username": "operator",
				"password": "a-real-password",
			})

			machine, ok := art.Machine()
			if !ok {
				t.Fatalf("a %s credential produced no machine identity, so nothing would authenticate with it", namespace)
			}
			if machine[credtype.MachineUsername] != "operator" {
				t.Errorf("username = %q, want %q", machine[credtype.MachineUsername], "operator")
			}
			if machine[credtype.MachinePassword] != "a-real-password" {
				t.Errorf("password did not reach the transport")
			}
			// Nothing on the wire: a machine identity travels in
			// DispatchPayload.Secrets, and a second home for it would mean
			// two answers to what a run authenticates as.
			if len(art.Env) != 0 || len(art.ExtraVars) != 0 || len(art.Files) != 0 {
				t.Errorf("a %s credential injected env/extra_vars/files as well as an identity", namespace)
			}
		})
	}
}

// TestAPrivateKeyGainsItsTrailingNewline covers AWX's own normalisation.
// A PEM body pasted into a form without its final newline is rejected by
// several of the readers a file injector generates content for, and AWX
// appends one in its injector namespace for exactly that reason.
func TestAPrivateKeyGainsItsTrailingNewline(t *testing.T) {
	t.Parallel()

	ct, ok := managed.Type("ssh")
	if !ok {
		t.Fatal("the catalog does not ship ssh")
	}
	cred := credtype.Credential{
		ID: 1, Name: "keyed", Type: ct,
		Inputs: map[string]string{"ssh_key_data": "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----"},
	}

	got, _ := cred.RenderVars()["ssh_key_data"].(string)
	if want := "-----BEGIN PRIVATE KEY-----\nbody\n-----END PRIVATE KEY-----\n"; got != want {
		t.Errorf("RenderVars()[ssh_key_data] = %q, want it to end with a newline", got)
	}

	// Idempotent: a key that already ends correctly is untouched, so a
	// round trip through a form does not accumulate blank lines.
	cred.Inputs["ssh_key_data"] += "\n"
	again, _ := cred.RenderVars()["ssh_key_data"].(string)
	if got != again {
		t.Errorf("appending a newline was not idempotent: %q then %q", got, again)
	}
}
