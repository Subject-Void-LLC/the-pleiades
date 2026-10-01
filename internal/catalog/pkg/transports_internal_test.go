// Keeps the generic pkg.* methods' declared transports equal to the union
// of the concrete methods they dispatch to.
package pkg

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"

	// The concrete methods managerNamespace dispatches to, registered so
	// the union below is computed from the real registry.
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/apt"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/pkg/dnf"
)

// TestGenericTransportsAreTheConcreteUnion keeps each generic pkg.*
// method's declared transports equal to the union of the concrete methods
// it dispatches to. Fewer would refuse at plan time a device that
// dispatch would have served; more would admit one that no concrete
// method can reach. A new package manager added to managerNamespace over a
// new transport fails here until genericManifest says so.
func TestGenericTransportsAreTheConcreteUnion(t *testing.T) {
	for _, verb := range []string{"install", "remove", "upgrade"} {
		generic, ok := collection.Lookup("pkg." + verb)
		if !ok {
			t.Fatalf("pkg.%s is not registered", verb)
		}
		var want []string
		for _, namespace := range managerNamespace {
			concrete, ok := collection.Lookup(namespace + "." + verb)
			if !ok {
				t.Fatalf("%s.%s is not registered", namespace, verb)
			}
			for _, transport := range concrete.Manifest.SupportedTransports {
				if !slices.Contains(want, transport) {
					want = append(want, transport)
				}
			}
		}
		got := slices.Clone(generic.Manifest.SupportedTransports)
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("pkg.%s declares transports %v, want the union of its concrete methods' %v", verb, got, want)
		}
	}
}
