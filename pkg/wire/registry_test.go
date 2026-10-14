// Tests for RegistryCredential and RegistryHost: how a registry's name is
// normalized, so an account is matched to the one registry an image names.
package wire

import "testing"

// TestRegistryHost folds Docker Hub's names together and normalizes case
// and scheme, so a credential stored under one spelling answers another.
func TestRegistryHost(t *testing.T) {
	for in, want := range map[string]string{
		"docker.io":                  "docker.io",
		"index.docker.io":            "docker.io",
		"registry-1.docker.io":       "docker.io",
		"https://GHCR.io/":           "ghcr.io",
		" registry.example.com:5000": "registry.example.com:5000",
		"":                           "",
	} {
		if got := RegistryHost(in); got != want {
			t.Errorf("RegistryHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRegistryCredential_For gives the account only to its own host, and
// nothing from a nil or hostless credential.
func TestRegistryCredential_For(t *testing.T) {
	c := &RegistryCredential{Host: "ghcr.io", Username: "u", Password: "p"}
	if u, p, ok := c.For("GHCR.IO"); !ok || u != "u" || p != "p" {
		t.Errorf("For(own host) = %q, %q, %v", u, p, ok)
	}
	for _, other := range []string{"docker.io", "ghcr.io.evil.example", "", "registry.example.com"} {
		if _, _, ok := c.For(other); ok {
			t.Errorf("For(%q) answered with another host's account", other)
		}
	}
	var none *RegistryCredential
	if _, _, ok := none.For("ghcr.io"); ok {
		t.Error("a nil credential answered")
	}
	if _, _, ok := (&RegistryCredential{Username: "u"}).For(""); ok {
		t.Error("a credential with no host answered")
	}
}
