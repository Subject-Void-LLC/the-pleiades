// Tests that the store holds a credential's values to what its kind reads
// from them at every write (credtype.CheckStoredValues): an SNMP
// credential's rules, and a private key refused beside a client certificate.
package credstore_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype/managed"
)

// TestTheStoreHoldsValuesToWhatTheirKindReads: a pleiades_snmp credential
// with a short community is refused at the write naming the input that
// allows one, and stored with it; one whose passphrase an external reference
// supplies is left to injection; a Kubernetes client certificate holding its
// key is refused on create and on an update, naming the key's input; and no
// refusal repeats a value.
func TestTheStoreHoldsValuesToWhatTheirKindReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _, orgID, _ := fixture(t)
	managedType := func(namespace string) int {
		def, ok := managed.Type(namespace)
		if !ok {
			t.Fatalf("%s is not shipped", namespace)
		}
		ct, err := store.EnsureManagedType(ctx, def)
		if err != nil {
			t.Fatalf("EnsureManagedType(%s): %v", namespace, err)
		}
		return ct.ID
	}
	snmp := managedType("pleiades_snmp")

	_, err := store.CreateCredential(ctx, orgID, snmp, "old switch", "", map[string]string{"community": "public"}, nil)
	if !errors.Is(err, credtype.ErrInvalidCredential) || !strings.Contains(err.Error(), "allow_short_community") || strings.Contains(err.Error(), "public") {
		t.Errorf("a short community was stored: %v", err)
	}
	if _, err := store.CreateCredential(ctx, orgID, snmp, "old switch", "", map[string]string{"community": "public", "allow_short_community": "true"}, nil); err != nil {
		t.Errorf("an allowed short community was refused: %v", err)
	}
	if _, err := store.CreateCredential(ctx, orgID, snmp, "core v3", "", map[string]string{"username": "monitor", "auth_protocol": "sha256"},
		map[string]string{"auth_passphrase": "file:snmp_auth"}); err != nil {
		t.Errorf("a passphrase from an external source was refused at the write: %v", err)
	}

	const cert = "-----BEGIN CERTIFICATE-----\nMIIBclient\n-----END CERTIFICATE-----\n"
	const key = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49\n-----END PRIVATE KEY-----\n"
	kube := managedType("pleiades_kubernetes_client_certificate")
	inputs := func(certificate string) map[string]string {
		return map[string]string{"host": "https://k8s.example.com:6443", "client_certificate": certificate, "client_key": key}
	}
	_, err = store.CreateCredential(ctx, orgID, kube, "cluster", "", inputs(cert+key), nil)
	if !errors.Is(err, credtype.ErrInvalidCredential) || !strings.Contains(err.Error(), "client_key") || strings.Contains(err.Error(), "MIGHAgEAMBMGByqGSM49") {
		t.Errorf("a key beside the client certificate was stored: %v", err)
	}
	created, err := store.CreateCredential(ctx, orgID, kube, "cluster", "", inputs(cert), nil)
	if err != nil {
		t.Fatalf("a client certificate alone was refused: %v", err)
	}
	_, err = store.UpdateCredential(ctx, created.ID, "cluster", "", map[string]string{"client_certificate": cert + key}, nil)
	if !errors.Is(err, credtype.ErrInvalidCredential) || !strings.Contains(err.Error(), "client_key") {
		t.Errorf("an update putting a key beside the certificate was stored: %v", err)
	}
}
