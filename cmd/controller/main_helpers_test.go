// This file covers the small pure helpers main.go carries beside its own
// wiring: getenv's fallback, decodeEnvelopeKey's base64/length validation,
// and actorFromRequest's no-identity case. The identity-present branch of
// actorFromRequest cannot be exercised from here: internal/api's identity
// context key is deliberately unexported (middleware.go's own doc comment),
// so only the real AuthMiddleware can stamp one onto a context, and that is
// covered where AuthMiddleware itself is tested, not in this composition
// root.
package main

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestGetenv(t *testing.T) {
	const key = "PLEIADES_TEST_GETENV_HELPER"

	t.Run("set", func(t *testing.T) {
		t.Setenv(key, "explicit")
		if got := getenv(key, "fallback"); got != "explicit" {
			t.Errorf("getenv(%q, fallback) = %q, want the explicit value", key, got)
		}
	})

	t.Run("unset falls back", func(t *testing.T) {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv: %v", err)
		}
		if got := getenv(key, "fallback"); got != "fallback" {
			t.Errorf("getenv(%q, fallback) = %q, want fallback", key, got)
		}
	})
}

func TestActorFromRequest_NoIdentityIsEmpty(t *testing.T) {
	if got := actorFromRequest(context.Background()); got != "" {
		t.Errorf("actorFromRequest(no identity) = %q, want empty", got)
	}
}

func TestDecodeEnvelopeKey(t *testing.T) {
	const envVar = "PLEIADES_TEST_ENVELOPE_KEY"
	valid := base64.StdEncoding.EncodeToString(make([]byte, 32))

	t.Run("valid 32 byte key", func(t *testing.T) {
		t.Setenv(envVar, valid)
		key, err := decodeEnvelopeKey(envVar)
		if err != nil {
			t.Fatalf("decodeEnvelopeKey: %v", err)
		}
		if len(key) != 32 {
			t.Errorf("decoded key is %d bytes, want 32", len(key))
		}
	})

	t.Run("not valid base64", func(t *testing.T) {
		t.Setenv(envVar, "not base64!!")
		if _, err := decodeEnvelopeKey(envVar); err == nil {
			t.Error("decodeEnvelopeKey accepted a value that is not base64")
		}
	})

	t.Run("wrong length", func(t *testing.T) {
		t.Setenv(envVar, base64.StdEncoding.EncodeToString(make([]byte, 16)))
		_, err := decodeEnvelopeKey(envVar)
		if err == nil {
			t.Fatal("decodeEnvelopeKey accepted a 16 byte key")
		}
		if !strings.Contains(err.Error(), envVar) {
			t.Errorf("error %q does not name the env var it complains about", err.Error())
		}
	})

	t.Run("whitespace is trimmed", func(t *testing.T) {
		t.Setenv(envVar, " "+valid+"\n")
		if _, err := decodeEnvelopeKey(envVar); err != nil {
			t.Errorf("decodeEnvelopeKey did not trim surrounding whitespace: %v", err)
		}
	})
}
