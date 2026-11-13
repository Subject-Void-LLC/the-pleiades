// This file tests the setup subcommand's flag parsing from inside package
// main, since parseSetupFlags is unexported and the command's other tests run
// the built binary.
package main

import "testing"

// TestParseSetupFlags_TakesAJWKSURL: --jwks-url is how a compose or Helm
// deployment is set up to check API tokens against an identity provider
// instead of a JWT secret, which the controller refuses to hold beside it.
func TestParseSetupFlags_TakesAJWKSURL(t *testing.T) {
	const url = "https://idp.example.com/realms/pleiades/protocol/openid-connect/certs"
	f, err := parseSetupFlags([]string{"--jwks-url", url, "--force"})
	if err != nil {
		t.Fatalf("parseSetupFlags error = %v", err)
	}
	if f.jwksURL != url || !f.force {
		t.Errorf("parsed %+v, want jwksURL %q with force", f, url)
	}
	if f, err := parseSetupFlags(nil); err != nil || f.jwksURL != "" {
		t.Errorf("no flags: %+v, %v; want no JWKS URL", f, err)
	}
}
