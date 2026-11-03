//go:build unix

// Package loader: tests that an external program's fact declarations are
// held to Register's rules (Phase 41b).
package loader

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestLoad_FactDeclarationsAreHeldToRegistersRules: a program's malformed
// declaration is refused before anything registers, one disagreeing with a
// built-in method's declaration of the same key is refused by Register, and
// a sound one reaches the registry, which is what the engine reads.
func TestLoad_FactDeclarationsAreHeldToRegistersRules(t *testing.T) {
	requireConfinement(t)
	t.Cleanup(collection.SnapshotForTest())
	if err := collection.Register(collection.Descriptor{
		Name: "factbuiltin.gather.kernel",
		Manifest: collection.Manifest{Status: collection.StatusImplemented, Reversibility: collection.Reversibility{Notes: "a test fixture"},
			Facts: []collection.FactSpec{{Key: "loadertest_kernel", Kind: collection.FactStable, Type: collection.FactString, Description: "the kernel"}}},
		Invoke: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	withFacts := func(specs ...collection.FactSpec) collection.Manifest {
		m := implemented(false)
		m.Facts = specs
		return m
	}
	for _, tc := range []struct {
		name  string
		specs []collection.FactSpec
		want  string
	}{
		{"acme.facts.bad", []collection.FactSpec{{Key: "a b", Kind: collection.FactStable, Type: collection.FactString, Description: "x"}}, "not a fact name"},
		{"acme.facts.nokind", []collection.FactSpec{{Key: "k", Type: collection.FactString, Description: "x"}}, "neither"},
		{"acme.facts.clash", []collection.FactSpec{{Key: "loadertest_kernel", Kind: collection.FactVolatile, Type: collection.FactString, Description: "x"}}, "factbuiltin.gather.kernel"},
		{"acme.facts.sound", []collection.FactSpec{{Key: "acme_widgets", Kind: collection.FactVolatile, Type: collection.FactInteger, Description: "widgets"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := programDir(t)
			out := describeJSON(t, 0, external.DescribedMethod{Name: tc.name, Manifest: withFacts(tc.specs...)})
			writeProgram(t, dir, "prog", script(out, `printf '{}' >&3`))
			if tc.want != "" {
				if err := tryLoad(t, dir, testOptions()); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("Load = %v, want a refusal naming %q", err, tc.want)
				}
				return
			}
			// Load itself, not tryLoad, which puts the registry back.
			restore := collection.SnapshotForTest()
			defer restore()
			if _, err := Load(t.Context(), dir, testOptions()); err != nil {
				t.Fatalf("Load: %v", err)
			}
			d, ok := collection.Lookup(tc.name)
			if !ok || len(d.Manifest.Facts) != 1 || d.Manifest.Facts[0].Kind != collection.FactVolatile {
				t.Fatalf("registered %+v, want the program's one volatile fact", d.Manifest.Facts)
			}
		})
	}
}
