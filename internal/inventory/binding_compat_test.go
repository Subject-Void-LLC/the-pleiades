// Tests that a device's discovery binding is what a build before 0.4.0
// computes for it, for every registered device type whose discovery is
// bound: so a release that binds one more property onboards no device
// again, and a Controller and a Runner of either release agree on every
// binding of a device that does not set the new key.
package inventory_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// boundSince040 are the keys bound since 0.4.0, which a build before it
// never bound: each contributes nothing to a binding while absent.
var boundSince040 = map[string]bool{inventory.TLSFirstUseProperty: true}

// allAbsentDigest is, per bound type, the binding of a record that sets
// none of its bound keys, as every release since the type was bound has
// computed it. A type whose digest moves would void the discovery and the
// grants of every device of the type that does not set the key that moved
// it: bind a new key (whose absence contributes nothing), never add a key to
// pkg/inventory's absenceRecorded.
var allAbsentDigest = map[string]string{
	"ai_provider":        "sha256:9fb16fee0fe1f203fe4d5ce0a5d2e21c9e5884a18cfcefebbfccb8c4610abecf",
	"cloudflare_account": "sha256:a8d1c63be3e4836ed1217eeb1dc83afd57c6c1b7876a0217824324a5145e54b0",
	"container_host":     "sha256:2bdcdc82162ba30559da8ecc166abb2c7c2e6b7d84cd11062f09f85a51c7bc2f",
	"generic_gnmi":       "sha256:537069cf452ea58138d674b29a9bdb81380c41b75f36ffb36a36121e1e484a91",
	"generic_grpc":       "sha256:92bb0a098ae24599087e77aa8ea6e4fcc1b6b5bc70c476bc8fe25184ce26784a",
	"generic_http":       "sha256:0485f8e956bd875d92dd68247f9a343fba8902aa46c55b4c7f21c98fb4a074e6",
	"generic_restconf":   "sha256:0485f8e956bd875d92dd68247f9a343fba8902aa46c55b4c7f21c98fb4a074e6",
	"generic_snmp":       "sha256:4299781b12e17a461c39dc8ec9561cea4b0086cd5df531bf89deec8e958ebd7c",
	"kubernetes_cluster": "sha256:392e7376751ed7b1095480dca4aef600188f44ac440c99b74345b91b0f377638",
}

// preRelease040Digest is BindingDigest as every build before 0.4.0 computed
// it: every bound key in sorted order, an absent one as a marker of its own.
func preRelease040Digest(props map[string]any, keys []string) string {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	var b strings.Builder
	for _, k := range sorted {
		b.WriteString(k)
		v, present := props[k]
		if !present {
			b.WriteString("\x00absent\n")
			continue
		}
		encoded, _ := json.Marshal(v)
		b.WriteString("=")
		b.Write(encoded)
		b.WriteString("\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// boundTypes are the registered device types whose discovery is bound.
func boundTypes(t *testing.T) []string {
	t.Helper()
	var types []string
	for typ := range record.AllTypes() {
		if record.BoundProperties(typ) != nil {
			types = append(types, typ)
		}
	}
	sort.Strings(types)
	if len(types) == 0 {
		t.Fatal("no registered device type binds its discovery, so this test checks nothing")
	}
	return types
}

// TestBinding_EveryBoundTypeKeepsItsDigest: for each bound type, a record
// that does not set a key bound since 0.4.0 has the binding a build before
// it computes, with nothing set, with every older key set, and with some of
// them; and the binding of a record that sets nothing is the one recorded.
func TestBinding_EveryBoundTypeKeepsItsDigest(t *testing.T) {
	for _, typ := range boundTypes(t) {
		bound := record.BoundProperties(typ)
		var older []string
		for _, k := range bound {
			if !boundSince040[k] {
				older = append(older, k)
			}
		}
		every, some := map[string]any{}, map[string]any{}
		for i, k := range older {
			every[k] = "value-of-" + k
			if i%2 == 0 {
				some[k] = true
			}
		}
		for name, props := range map[string]map[string]any{"nothing set": {}, "every older key set": every, "some set": some} {
			if got, want := record.DiscoveryBinding(typ, inventory.NewProperties(props)), preRelease040Digest(props, older); got != want {
				t.Errorf("%s, %s: binding %s, and a build before 0.4.0 computes %s: its devices would be onboarded again", typ, name, got, want)
			}
		}
		got := record.DiscoveryBinding(typ, inventory.NewProperties(nil))
		if want, recorded := allAbsentDigest[typ]; !recorded || got != want {
			t.Errorf("%s: the binding of a record that sets nothing is %s, recorded %q", typ, got, want)
		}
	}
}
