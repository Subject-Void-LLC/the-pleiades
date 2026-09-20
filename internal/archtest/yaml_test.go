// Package archtest: that the module uses one YAML library.
package archtest

import (
	"slices"
	"testing"
)

// yamlModule is the one YAML module this repository decodes with.
const yamlModule = "go.yaml.in/yaml/v3"

// forbiddenYAML are the older module paths of the same library. Each is a
// separate module with its own yaml.Node and its own Unmarshaler
// interface, so a custom UnmarshalYAML written against one of them
// compiles cleanly and is never called by a decoder from the other. That
// is how engine.CheckModeFlag's refusal of check_mode: false was first
// written, and why the refusal silently did nothing until a test caught it
// (FAILURE_PATTERNS 255).
var forbiddenYAML = []string{"gopkg.in/yaml.v3", "gopkg.in/yaml.v2"}

// TestOneYAMLModule keeps every package, and every package's tests, on
// yamlModule. The control is the second half: something must be found
// importing yamlModule, or the listing is misaimed and the first half
// proved nothing.
func TestOneYAMLModule(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/...")
	if len(pkgs) == 0 {
		t.Fatal("go list matched no packages in the module, so this rule examined nothing")
	}
	users := 0
	for _, pkg := range pkgs {
		for _, imports := range [][]string{pkg.Imports, pkg.TestImports, pkg.XTestImports} {
			for _, bad := range forbiddenYAML {
				if slices.Contains(imports, bad) {
					t.Errorf("%s imports %s; use %s, whose decoder is the one that calls a custom UnmarshalYAML", pkg.ImportPath, bad, yamlModule)
				}
			}
			if slices.Contains(imports, yamlModule) {
				users++
			}
		}
	}
	if users == 0 {
		t.Errorf("nothing imports %s, so this query is misaimed", yamlModule)
	}
}
