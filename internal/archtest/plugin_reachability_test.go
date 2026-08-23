// This file guards the sync plugin registry the way
// transport_reachability_test.go guards the transport fqcn table: not
// "is it registered" (registry_sweep_test.go already asks that) but "can
// the binary a user runs actually build it".
//
// The distinction is the whole finding. The AWS plugin was registered,
// StatusImplemented, covered by its own package suite, by a LocalStack
// integration suite and by the shared conformance suite, and
// `pleiades inventory sync --plugin aws` failed 100% of the time,
// because the two things it needed (a credential store and a region)
// arrived through constructor Options that only tests passed, while the
// CLI built it through the registry's argument-less constructor. Every
// suite constructed the plugin its own way, so none of them was testing
// the way the product does. See FAILURE_PATTERNS.md.
package archtest

import (
	"sort"
	"strings"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/plugins"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// pluginPackagePrefix is where individual sync plugin packages live. The
// aggregator sits at exactly this path with no trailing element; a
// concrete plugin is always one segment below it.
const pluginPackagePrefix = modulePath + "/internal/inventory/plugins"

// TestEveryRegisteredPluginOpensFromTheSharedPath proves every plugin in
// the registry can be built by syncplugin.Open, the one function
// cmd/pleiades and the conformance suite both construct through, given
// only what a user can actually supply: the descriptor's own default
// config, the settings that descriptor declares, and the project's
// credential store. It then proves the RequiresCredentials flag really
// changes the outcome, by opening each plugin a second time with no
// store at all.
//
// Be precise about what this does and does not catch, because
// overstating it would recreate the problem in a new place. It catches a
// descriptor the shared path cannot satisfy: an invalid default config,
// a constructor that returns nil, a required setting with no way to
// supply it, a RequiresCredentials flag that turns out to change
// nothing. It does NOT catch a plugin whose Connect needs something its
// descriptor never declares, because Open can only check declarations.
// That gap is closed by two other things instead: the structural rule
// below (a composition root cannot special-case one plugin, so it cannot
// hand one plugin something the others never get) and the conformance
// suite, which now drives every backend through Open and Connect against
// a real upstream rather than through each plugin's own constructor.
func TestEveryRegisteredPluginOpensFromTheSharedPath(t *testing.T) {
	all := syncplugin.All()
	if len(all) == 0 {
		t.Fatal("no sync plugins are registered, which means internal/inventory/plugins was not linked in")
	}

	// The real file-backed store cmd/pleiades builds. It is lazy (it
	// touches disk only when a credential is actually resolved), so
	// pointing it at an empty temp directory is a genuine store rather
	// than a stand-in, and Open never resolves anything through it.
	deps := syncplugin.Deps{Credentials: credential.NewLazyFileStore(t.TempDir())}

	for name, desc := range all {
		t.Run(name, func(t *testing.T) {
			cfg := desc.DefaultConfig
			cfg.Name = desc.Name

			// Fill every declared setting with a placeholder, which is
			// exactly what --set does. A plugin needing a value it never
			// declared cannot be satisfied here, which is the point: an
			// undeclared requirement is one no user can discover.
			if len(desc.Settings) > 0 {
				cfg.Settings = make(map[string]string, len(desc.Settings))
				for _, spec := range desc.Settings {
					cfg.Settings[spec.Name] = "archtest-placeholder"
				}
			}

			plugin, err := syncplugin.Open(desc, cfg, deps)
			if err != nil {
				t.Fatalf("syncplugin.Open(%q) = %v: this plugin cannot be built from the CLI path, so every `pleiades inventory sync --plugin %s` fails before it dials anything", name, err, name)
			}
			if plugin == nil {
				t.Fatalf("syncplugin.Open(%q) returned a nil plugin with no error", name)
			}
			_ = plugin.Close()

			// The same Open with no credential store. This is what keeps
			// RequiresCredentials from being a field nobody reads: a
			// plugin that declares it must be refused, and one that does
			// not must still build, so the two answers cannot both be
			// "it worked" and the assertion cannot pass vacuously.
			bare, err := syncplugin.Open(desc, cfg, syncplugin.Deps{})
			switch {
			case desc.RequiresCredentials && err == nil:
				_ = bare.Close()
				t.Errorf("sync plugin %q declares RequiresCredentials but Open built it with no store, so a composition root that forgot to wire one would only find out from a real upstream", name)
			case !desc.RequiresCredentials && err != nil:
				t.Errorf("sync plugin %q does not declare RequiresCredentials but Open refused it without a store: %v", name, err)
			case !desc.RequiresCredentials && err == nil:
				_ = bare.Close()
			}
		})
	}
}

// TestPluginsDeclareWhatTheyNeed proves the two declarations Open reads
// are answered rather than left at their zero values by accident.
//
// A plugin whose Connect resolves a credential but whose descriptor
// leaves RequiresCredentials false is the original defect in a new
// costume: Open would happily build it with a nil store, and the refusal
// would move back inside Connect where only a real upstream reaches it.
// Nothing can infer that flag from the type, so this checks the one
// thing it can check for real: that every setting a plugin declares is
// well formed enough to be listed and demanded.
func TestPluginsDeclareWhatTheyNeed(t *testing.T) {
	all := syncplugin.All()
	if len(all) == 0 {
		t.Fatal("no sync plugins are registered, so this test proved nothing")
	}

	for name, desc := range all {
		for _, spec := range desc.Settings {
			if strings.TrimSpace(spec.Name) == "" {
				t.Errorf("sync plugin %q declares a setting with no name, which nothing can supply", name)
			}
			if strings.TrimSpace(spec.Description) == "" {
				t.Errorf("sync plugin %q declares setting %q with no description, so a user must read source to supply it", name, spec.Name)
			}
			if spec.Name != strings.ToLower(spec.Name) {
				t.Errorf("sync plugin %q declares setting %q, which is not lowercase: this repository's key convention is lowercase with underscores", name, spec.Name)
			}
		}

		// A required setting with no default is the only kind Open can
		// refuse by name, so a descriptor that declares nothing required
		// and still fails to Open would be lying by omission. The Open
		// sweep above is what proves that did not happen; this only
		// pins the missing-settings report itself.
		cfg := desc.DefaultConfig
		cfg.Name = desc.Name
		missing := desc.MissingSettings(cfg)
		for _, got := range missing {
			var declared bool
			for _, spec := range desc.Settings {
				if spec.Name == got && spec.Required {
					declared = true
				}
			}
			if !declared {
				t.Errorf("sync plugin %q reports missing setting %q, which it never declared as required", name, got)
			}
		}
	}
}

// TestCompositionRootsBuildPluginsThroughTheRegistry proves no cmd/
// binary imports an individual sync plugin package.
//
// This is the structural half of the guard, and it is the one that would
// have caught the AWS defect outright. cmd/pleiades used to import
// internal/inventory/plugins/catalystcenter by name, so it could pass
// that one plugin a credential store through a type switch. The switch's
// own comment predicted its successor ("when a third plugin needs it,
// this becomes an optional interface the plugin asserts rather than a
// longer switch"), the third plugin arrived, and nobody extended it, so
// aws got the argument-less constructor and failed unconditionally.
//
// Forbidding the import forbids the switch. A composition root can only
// reach a plugin through the registry, which means every plugin gets the
// same Deps, which means a dependency one plugin needs and another does
// not cannot go missing for the one nobody remembered.
func TestCompositionRootsBuildPluginsThroughTheRegistry(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/cmd/...")
	if len(pkgs) == 0 {
		t.Fatalf("go list found no packages under %s/cmd", modulePath)
	}

	for _, pkg := range pkgs {
		for _, imp := range individualPluginImports(pkg.Imports) {
			t.Errorf("%s imports the individual sync plugin package %s: a composition root must reach a plugin through syncplugin.Lookup and syncplugin.Open, so every plugin receives the same Deps and none can be wired for one caller and unwired for the rest",
				pkg.ImportPath, imp)
		}
	}
}

// individualPluginImports returns, sorted, every import in imports that
// names one concrete sync plugin package rather than the aggregator.
//
// It is a separate function so the rule can be run against an import
// list this test controls, which is what
// TestIndividualPluginImportsDetectsADirectImport below does. A rule
// that only ever runs against a tree that already satisfies it cannot
// tell "nothing is wrong" from "the check matches nothing".
func individualPluginImports(imports []string) []string {
	var found []string
	for _, imp := range imports {
		if imp == pluginPackagePrefix {
			// The aggregator itself, blank-imported to trigger every
			// plugin's init(). That import is required, not forbidden.
			continue
		}
		if strings.HasPrefix(imp, pluginPackagePrefix+"/") {
			found = append(found, imp)
		}
	}
	sort.Strings(found)
	return found
}

// TestIndividualPluginImportsDetectsADirectImport is the negative
// control for the rule above: it proves the matcher really does separate
// the aggregator from a concrete plugin package, rather than passing
// because it recognizes neither.
func TestIndividualPluginImportsDetectsADirectImport(t *testing.T) {
	got := individualPluginImports([]string{
		"context",
		"github.com/Subject-Void-LLC/the-pleiades/internal/credential",
		pluginPackagePrefix,
		pluginPackagePrefix + "/catalystcenter",
		modulePath + "/internal/inventory/syncplugin",
	})

	want := []string{pluginPackagePrefix + "/catalystcenter"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("individualPluginImports() = %v, want %v", got, want)
	}
}
