// This file tests how the controller chooses its token key source, from
// inside package main, since loadKeyProvider is unexported and its refusals
// happen before the process does anything observable from outside.
package main

import (
	"errors"
	"strings"
	"testing"
)

// TestLoadKeyProvider_RefusesBothTokenSources: JWKS_URL beside JWT_SECRET
// is a startup error naming both, never JWKS winning silently with the
// secret left as a fallback (FAILURE_PATTERNS 1229). The JWKS URL points at
// a closed port, so reaching a fetch would fail differently: the refusal
// has to come first.
func TestLoadKeyProvider_RefusesBothTokenSources(t *testing.T) {
	t.Setenv("JWKS_URL", "https://127.0.0.1:1/keys")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 40))

	_, err := loadKeyProvider()
	if !errors.Is(err, errBothTokenSources) {
		t.Fatalf("loadKeyProvider() error = %v, want the both-set refusal", err)
	}
	// It names both settings and which to remove for each intention, and
	// where setup put the secret, so an operator who ran setup first can
	// find it.
	for _, want := range []string{"JWKS_URL", "JWT_SECRET", "remove JWT_SECRET", "remove JWKS_URL", "controller setup"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}
}

// TestLoadKeyProvider_EachSourceAlone covers the three other combinations:
// a plain http JWKS URL is refused for its scheme, a secret alone builds the
// static provider, and neither is refused.
func TestLoadKeyProvider_EachSourceAlone(t *testing.T) {
	t.Run("plain http JWKS URL", func(t *testing.T) {
		t.Setenv("JWKS_URL", "http://127.0.0.1:1/keys")
		t.Setenv("JWT_SECRET", "")
		_, err := loadKeyProvider()
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Fatalf("loadKeyProvider() error = %v, want an https refusal", err)
		}
	})
	t.Run("secret alone", func(t *testing.T) {
		t.Setenv("JWKS_URL", "")
		t.Setenv("JWT_SECRET", strings.Repeat("j", 40))
		provider, err := loadKeyProvider()
		if err != nil || provider == nil {
			t.Fatalf("loadKeyProvider() = %v, %v; want the static provider", provider, err)
		}
		if algs := provider.Algorithms(); len(algs) != 1 || algs[0] != "HS256" {
			t.Errorf("algorithms = %v, want the static provider's HS256", algs)
		}
	})
	t.Run("neither", func(t *testing.T) {
		t.Setenv("JWKS_URL", "")
		t.Setenv("JWT_SECRET", "")
		if _, err := loadKeyProvider(); err == nil {
			t.Fatal("loadKeyProvider() with no token source succeeded")
		}
	})
}
