// Tests for the discovery property's encoding: it round-trips through the
// plain values every store keeps, and a malformed one is refused rather
// than read as "discovered nothing".
package inventory_test

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

func discovered(v inventory.PropertyValue) inventory.Properties {
	return inventory.NewProperties(map[string]inventory.PropertyValue{inventory.DiscoveredProperty: v})
}

// TestDiscovery_RoundTrips encodes a discovery and reads it back, with its
// capabilities sorted and its time kept to the second.
func TestDiscovery_RoundTrips(t *testing.T) {
	at := time.Date(2026, 9, 24, 12, 30, 45, 0, time.UTC)
	d := inventory.Discovery{
		Protocol:     "netconf",
		Capabilities: []capability.Name{capability.NameNetconf, capability.NameHTTPAPI},
		Facts:        map[string]any{"framing": "chunked", "urns": []any{"a", "b"}},
		ProbedAt:     at,
	}
	got, ok, err := inventory.DiscoveryFrom(discovered(d.Property()))
	if err != nil || !ok {
		t.Fatalf("ok %v, err %v", ok, err)
	}
	want := d
	want.Capabilities = []capability.Name{capability.NameHTTPAPI, capability.NameNetconf}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	if !inventory.IsReservedProperty(inventory.DiscoveredProperty) || inventory.IsReservedProperty("host") {
		t.Error("the reserved set is wrong")
	}
}

// TestDiscoveryFrom_Shapes covers an absent property, the []string form
// a Go caller may store, and each malformed shape.
func TestDiscoveryFrom_Shapes(t *testing.T) {
	if _, ok, err := inventory.DiscoveryFrom(inventory.NewProperties(nil)); ok || err != nil {
		t.Errorf("absent: ok %v, err %v", ok, err)
	}
	got, ok, err := inventory.DiscoveryFrom(discovered(map[string]any{"capabilities": []string{"GRPCCapable"}, "probed_at": "not a time"}))
	if err != nil || !ok || len(got.Capabilities) != 1 || !got.ProbedAt.IsZero() {
		t.Errorf("[]string form: %+v, %v, %v", got, ok, err)
	}
	for name, v := range map[string]inventory.PropertyValue{
		"not a mapping":         "yes",
		"capabilities a string": map[string]any{"capabilities": "GRPCCapable"},
		"a capability a number": map[string]any{"capabilities": []any{7}},
	} {
		if _, ok, err := inventory.DiscoveryFrom(discovered(v)); err == nil || !ok {
			t.Errorf("%s: ok %v, err %v, want a refusal", name, ok, err)
		}
	}
}

// TestBindingDigest_ChangesWithEveryBoundProperty: changing, adding or
// removing any bound key changes the digest, the order keys are named in
// does not, and a key outside the bound set does not either. The digest
// never holds a value in the clear.
func TestBindingDigest_ChangesWithEveryBoundProperty(t *testing.T) {
	keys := []string{"base_url", "http_auth", "tls_ca_pem"}
	base := map[string]inventory.PropertyValue{"base_url": "https://api.example.com", "http_auth": "bearer", "other": "x"}
	digest := inventory.BindingDigest(inventory.NewProperties(base), keys)

	if !strings.HasPrefix(digest, "sha256:") || strings.Contains(digest, "api.example.com") {
		t.Fatalf("digest = %q, want an opaque sha256", digest)
	}
	if got := inventory.BindingDigest(inventory.NewProperties(base), []string{"tls_ca_pem", "base_url", "http_auth"}); got != digest {
		t.Error("the order keys are named in changed the digest")
	}
	for name, change := range map[string]func(map[string]inventory.PropertyValue){
		"a changed value": func(p map[string]inventory.PropertyValue) { p["base_url"] = "https://attacker.example.net" },
		"an added key":    func(p map[string]inventory.PropertyValue) { p["tls_ca_pem"] = "-----BEGIN CERTIFICATE-----" },
		"a removed key":   func(p map[string]inventory.PropertyValue) { delete(p, "http_auth") },
		"an empty value":  func(p map[string]inventory.PropertyValue) { p["http_auth"] = "" },
	} {
		p := map[string]inventory.PropertyValue{}
		for k, v := range base {
			p[k] = v
		}
		change(p)
		if inventory.BindingDigest(inventory.NewProperties(p), keys) == digest {
			t.Errorf("%s did not change the digest", name)
		}
	}
	unbound := map[string]inventory.PropertyValue{}
	for k, v := range base {
		unbound[k] = v
	}
	unbound["other"] = "y"
	if inventory.BindingDigest(inventory.NewProperties(unbound), keys) != digest {
		t.Error("a key outside the bound set changed the digest")
	}
}

// TestBindingDigest_BindsAValueJSONCannotEncode: a bound value JSON refuses
// (a NaN, an infinity) is still bound, by its Go form, so the digest is
// stable for it, differs from the key being absent, and changes when the
// value does.
func TestBindingDigest_BindsAValueJSONCannotEncode(t *testing.T) {
	keys := []string{"weight"}
	digest := func(v inventory.PropertyValue) string {
		return inventory.BindingDigest(inventory.NewProperties(map[string]inventory.PropertyValue{"weight": v}), keys)
	}
	nan := digest(math.NaN())
	if nan != digest(math.NaN()) {
		t.Error("the digest of an unencodable value is not stable")
	}
	absent := inventory.BindingDigest(inventory.NewProperties(nil), keys)
	if nan == absent || nan == digest(math.Inf(1)) || digest(math.Inf(1)) == digest(math.Inf(-1)) {
		t.Error("an unencodable value collided with another value or with its absence")
	}
}

// TestDiscovery_BindingRoundTrips: the binding survives the property
// encoding every inventory store round-trips, and an unbound discovery
// writes no binding key at all.
func TestDiscovery_BindingRoundTrips(t *testing.T) {
	d := inventory.Discovery{Protocol: "http", Binding: "sha256:abc"}
	got, ok, err := inventory.DiscoveryFrom(inventory.NewProperties(map[string]inventory.PropertyValue{inventory.DiscoveredProperty: d.Property()}))
	if err != nil || !ok || got.Binding != "sha256:abc" {
		t.Fatalf("inventory.DiscoveryFrom() = %+v, %v, %v, want the binding back", got, ok, err)
	}
	if _, present := (inventory.Discovery{Protocol: "http"}).Property()["binding"]; present {
		t.Error("an unbound discovery wrote a binding key")
	}
}
