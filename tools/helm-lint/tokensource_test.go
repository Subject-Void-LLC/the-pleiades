// Tests for the one-token-check rule, on synthetic renders.
package main

import (
	"strings"
	"testing"
)

// tokenRender builds a controller Deployment whose controller container sets
// the given extra variables beside its master key, and a chart Secret with
// the given keys.
func tokenRender(env []envVar, secretKeys ...string) []manifest {
	var controller manifest
	controller.Kind = "Deployment"
	controller.Metadata.Name = "r-the-pleiades-controller"
	controller.Spec.Template.Spec.Containers = []container{{
		Name: "controller",
		Env: append([]envVar{
			{Name: "MASTER_ENCRYPTION_KEY", ValueFrom: &envVarSource{SecretKeyRef: &secretKeyRef{Name: "r-the-pleiades", Key: "MASTER_ENCRYPTION_KEY"}}},
		}, env...),
	}}
	var secret manifest
	secret.Kind = "Secret"
	secret.Metadata.Name = "r-the-pleiades"
	secret.StringData = map[string]string{"MASTER_ENCRYPTION_KEY": "k"}
	for _, k := range secretKeys {
		secret.StringData[k] = "v"
	}
	return []manifest{controller, secret}
}

// jwtFromSecret is the JWT_SECRET variable the chart sets by default.
var jwtFromSecret = envVar{Name: "JWT_SECRET", ValueFrom: &envVarSource{SecretKeyRef: &secretKeyRef{Name: "r-the-pleiades", Key: "JWT_SECRET"}}}

// TestCheckTokenSource covers each way a render could give the controller
// both token checks, neither, or the wrong URL.
func TestCheckTokenSource(t *testing.T) {
	jwks := envVar{Name: "JWKS_URL", Value: profileJWKSURL}
	cases := []struct {
		name      string
		objects   []manifest
		wantJWKS  string
		wantFound string
	}{
		{"the default, a JWT secret", tokenRender([]envVar{jwtFromSecret}, "JWT_SECRET"), "", ""},
		{"a JWKS URL alone", tokenRender([]envVar{jwks}), profileJWKSURL, ""},
		{"a JWKS URL beside the secret", tokenRender([]envVar{jwks, jwtFromSecret}), profileJWKSURL, "beside JWKS_URL"},
		{"a JWKS URL with the secret still in the Secret", tokenRender([]envVar{jwks}, "JWT_SECRET"), profileJWKSURL, "carries JWT_SECRET"},
		{"the wrong JWKS URL", tokenRender([]envVar{{Name: "JWKS_URL", Value: "https://other.example.com/keys"}}), profileJWKSURL, "want controller.jwksUrl's"},
		{"no JWKS URL rendered", tokenRender(nil), profileJWKSURL, "want controller.jwksUrl's"},
		{"neither", tokenRender(nil), "", "sets no JWT_SECRET"},
		{"a JWKS URL nobody asked for", tokenRender([]envVar{jwtFromSecret, jwks}, "JWT_SECRET"), "", "though controller.jwksUrl is empty"},
		{"no controller at all", nil, "", "checked nothing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkTokenSource("p", tc.objects, tc.wantJWKS)
			if tc.wantFound == "" {
				if len(got) != 0 {
					t.Fatalf("a correct render produced findings: %v", got)
				}
				return
			}
			if len(got) == 0 || !strings.Contains(got[0].message, tc.wantFound) {
				t.Fatalf("findings = %v, want one saying %q", got, tc.wantFound)
			}
		})
	}
}
