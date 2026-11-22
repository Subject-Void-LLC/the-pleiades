// Tests for factsOf, the decision on a read's verified keys, over keys of
// every type a fact names, a host certificate, a type none names, and the
// cases that record nothing. The real sessions are sshfacts_test.go's; this
// covers the key types the in-process server cannot present.
package sshfacts

import (
	"crypto/dsa" // only to prove a DSA key is recorded under dsa
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// mustKey converts a crypto public key to an SSH one, failing t on error.
func mustKey(t *testing.T, pub any) ssh.PublicKey {
	t.Helper()
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("NewPublicKey(%T): %v", pub, err)
	}
	return key
}

// ecdsaKey is a fresh ECDSA key on curve.
func ecdsaKey(t *testing.T, curve elliptic.Curve) ssh.PublicKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return mustKey(t, &priv.PublicKey)
}

// ed25519Key is a fresh ed25519 key, with its signer for certifying.
func ed25519Key(t *testing.T) (ssh.PublicKey, ssh.Signer) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return mustKey(t, pub), signer
}

// dsaKey is a fresh 1024-bit DSA key, the only size SSH's ssh-dss carries.
func dsaKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	var priv dsa.PrivateKey
	if err := dsa.GenerateParameters(&priv.Parameters, rand.Reader, dsa.L1024N160); err != nil {
		t.Fatal(err)
	}
	if err := dsa.GenerateKey(&priv, rand.Reader); err != nil {
		t.Fatal(err)
	}
	return mustKey(t, &priv.PublicKey)
}

// unknownKey is a key of a type no fact names, as a security key's
// sk-ssh-ed25519@openssh.com or a type a later library adds would be.
type unknownKey struct{ ssh.PublicKey }

// Type names a type no fact names.
func (unknownKey) Type() string { return "sk-ssh-ed25519@openssh.com" }

// TestFactsOf_EveryKeyTypeHasItsFact: each type lands under its own fact,
// valued as its SHA-256 fingerprint, in FactKeys order.
func TestFactsOf_EveryKeyTypeHasItsFact(t *testing.T) {
	ed, _ := ed25519Key(t)
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]ssh.PublicKey{
		FactDSA:       dsaKey(t),
		FactRSA:       mustKey(t, &rsaPriv.PublicKey),
		FactECDSAP521: ecdsaKey(t, elliptic.P521()),
		FactECDSAP384: ecdsaKey(t, elliptic.P384()),
		FactECDSAP256: ecdsaKey(t, elliptic.P256()),
		FactED25519:   ed,
	}
	// Offered in reverse, so the order out is FactKeys', not the input's.
	var in []ssh.PublicKey
	for i := len(FactKeys) - 1; i >= 0; i-- {
		in = append(in, keys[FactKeys[i]])
	}
	facts, warnings := factsOf("web1", in, false)
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	if len(facts) != len(FactKeys) {
		t.Fatalf("facts = %v, want one per type", facts)
	}
	for i, f := range facts {
		if f.Key != FactKeys[i] || f.Value != ssh.FingerprintSHA256(keys[f.Key]) || !strings.HasPrefix(f.Value, "SHA256:") {
			t.Errorf("fact %d = %+v, want %s = %s", i, f, FactKeys[i], ssh.FingerprintSHA256(keys[FactKeys[i]]))
		}
	}
}

// TestFactsOf_AHostCertificateRecordsItsKey: a host certificate is recorded
// under its key's type, valued as its key's fingerprint, which is what
// ssh-keygen -lf prints for a certificate.
func TestFactsOf_AHostCertificateRecordsItsKey(t *testing.T) {
	hostKey, _ := ed25519Key(t)
	_, ca := ed25519Key(t)
	cert := &ssh.Certificate{Key: hostKey, CertType: ssh.HostCert, ValidPrincipals: []string{"web1"}, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	facts, warnings := factsOf("web1", []ssh.PublicKey{cert}, false)
	if len(warnings) != 0 || len(facts) != 1 || facts[0].Key != FactED25519 || facts[0].Value != ssh.FingerprintSHA256(hostKey) {
		t.Errorf("factsOf(certificate) = %+v, %q; want %s = the certified key's fingerprint", facts, warnings, FactED25519)
	}
}

// TestFactsOf_WhatRecordsNothing: an unknown type warns and is left out; two
// different keys of one type, or more keys than a connection keeps, record
// nothing at all; the same key twice is one fact.
func TestFactsOf_WhatRecordsNothing(t *testing.T) {
	a, _ := ed25519Key(t)
	b, _ := ed25519Key(t)
	p256 := ecdsaKey(t, elliptic.P256())

	facts, warnings := factsOf("web1", []ssh.PublicKey{unknownKey{a}, p256}, false)
	if len(facts) != 1 || facts[0].Key != FactECDSAP256 || len(warnings) != 1 || !strings.Contains(warnings[0], "sk-ssh-ed25519@openssh.com") {
		t.Errorf("unknown type: %+v, %q; want only the ECDSA fact and a warning naming the type", facts, warnings)
	}

	facts, warnings = factsOf("web1", []ssh.PublicKey{a, p256, b}, false)
	if len(facts) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "more than one server") {
		t.Errorf("two ed25519 keys: %+v, %q; want no fact at all and one warning", facts, warnings)
	}

	facts, warnings = factsOf("web1", []ssh.PublicKey{a}, true)
	if len(facts) != 0 || len(warnings) != 1 {
		t.Errorf("more keys than kept: %+v, %q; want no fact and one warning", facts, warnings)
	}

	facts, warnings = factsOf("web1", []ssh.PublicKey{a, a}, false)
	if len(facts) != 1 || len(warnings) != 0 {
		t.Errorf("the same key twice: %+v, %q; want one fact", facts, warnings)
	}

	if facts, warnings := Read("web1"); len(facts) != 0 || len(warnings) != 0 {
		t.Errorf("Read with no session = %+v, %q; want nothing, since no SSH session was made", facts, warnings)
	}
}
