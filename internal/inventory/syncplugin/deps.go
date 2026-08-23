// This file holds the wiring half of the sync plugin port: the
// dependencies a composition root hands a plugin, the per-plugin
// settings a deployment supplies, and the one function that puts the two
// together.
//
// It exists because of a real, shipped defect. The AWS plugin needed two
// things the registry could not give it, a credential store and a
// region, and both arrived through constructor Options that only its own
// tests ever passed. cmd/pleiades built it through the registry's
// argument-less constructor instead, so every `pleiades inventory sync
// --plugin aws` failed unconditionally with "no region configured, use
// WithRegion" -- a plugin verified by a conformance suite, a LocalStack
// integration suite and a package suite, and reachable from nothing that
// ships. cmd/pleiades's own buildSyncPlugin carried a type switch with a
// comment predicting this ("when a third plugin needs it, this becomes
// an optional interface the plugin asserts rather than a longer switch");
// the third plugin arrived and the switch was not extended.
//
// The fix is that a dependency a plugin cannot construct for itself is
// now a parameter of every constructor, not an option one caller might
// remember to pass, and a per-deployment value is now declared data on
// the descriptor that Open refuses to proceed without. Both are checked
// in one place, so a plugin cannot be reachable in its own tests and
// unreachable from the CLI. See FAILURE_PATTERNS.md.
package syncplugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
)

// Deps carries the process-wide dependencies a plugin needs but cannot
// build for itself. A composition root fills it once and hands the same
// value to every plugin it constructs.
//
// It is a struct rather than a widening list of constructor parameters
// so a future dependency is an additive field: every existing plugin
// keeps compiling, and the ones that need the new thing read it. It is
// passed to every constructor rather than offered through an optional
// interface because an optional interface is exactly what a plugin
// author forgets to implement, which is the failure this file's own doc
// comment describes.
type Deps struct {
	// Credentials resolves Config.CredentialName at Connect time. A
	// plugin whose descriptor sets RequiresCredentials will not be built
	// at all without one, so a plugin reading this field never has to
	// re-check it for nil.
	//
	// It is injected rather than constructed because which store a
	// deployment uses is a composition-root decision: the Crawl tier's
	// file-backed store, a Controller's database-backed one, or a test's
	// in-memory one.
	Credentials credential.Store
}

// SettingSpec declares one per-deployment value a plugin needs beyond
// the fields every Config shares.
//
// Config deliberately does not grow a field per plugin (its Endpoint doc
// comment says so outright: "a shared type that grows a field per
// implementation stops being shared"), and a constructor Option cannot
// carry one either, because the value comes from a user at the command
// line rather than from the composition root. Declaring it here is what
// lets `pleiades inventory plugins` list what a plugin needs, and lets
// Open refuse a missing one by name before anything dials.
// The JSON tags are load-bearing rather than decorative: this type
// crosses a command line as `pleiades forge new-plugin --settings-json`,
// and a spec written by hand there should read in this repository's own
// lowercase key convention rather than in Go field names.
type SettingSpec struct {
	// Name is the key an operator supplies, lowercase with underscores,
	// matching this repository's YAML key convention.
	Name string `json:"name"`

	// Description is one line of help text, shown wherever a plugin's
	// settings are listed. It is required: a setting a user must supply
	// and cannot find out the meaning of is worse than no setting.
	Description string `json:"description"`

	// Required says Open refuses to build the plugin without this
	// setting. A setting that is genuinely optional (an override with a
	// real default) sets this false and documents its default in
	// Description.
	Required bool `json:"required"`
}

// MissingSettings returns the names of every required setting cfg does
// not supply, sorted, so an error message lists them in a stable order.
func (d Descriptor) MissingSettings(cfg Config) []string {
	var missing []string
	for _, spec := range d.Settings {
		if !spec.Required {
			continue
		}
		if value, ok := cfg.Setting(spec.Name); !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, spec.Name)
		}
	}
	sort.Strings(missing)
	return missing
}

// Open builds the plugin desc names, after proving the caller supplied
// everything that plugin needs in order to work.
//
// It is the ONE construction path. cmd/pleiades calls it, and so does
// every test that drives a plugin through more than its own
// constructor, which is the point: the AWS wiring defect survived three
// separate test suites precisely because each of them built the plugin
// its own way, with its own options, and none of them built it the way
// the CLI did.
//
// It deliberately does not Connect. Construction and connection fail for
// unrelated reasons (a missing region versus an unreachable account),
// and keeping them separate lets a caller check the first without a
// network and report the second with its own context.
func Open(desc Descriptor, cfg Config, deps Deps) (Plugin, error) {
	if desc.New == nil {
		return nil, fmt.Errorf("sync plugin %q has no constructor", desc.Name)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if desc.RequiresCredentials && deps.Credentials == nil {
		return nil, fmt.Errorf("sync plugin %q resolves a credential and was given no credential store: the composition root building it must supply Deps.Credentials", desc.Name)
	}
	if missing := desc.MissingSettings(cfg); len(missing) > 0 {
		return nil, fmt.Errorf("sync plugin %q needs setting(s) %s: %s", desc.Name, strings.Join(missing, ", "), desc.describeSettings(missing))
	}

	plugin := desc.New(deps)
	if plugin == nil {
		return nil, fmt.Errorf("sync plugin %q constructor returned nil", desc.Name)
	}
	return plugin, nil
}

// describeSettings renders the help text for the named settings, so a
// refusal tells an operator what the value means rather than only that
// one is missing.
func (d Descriptor) describeSettings(names []string) string {
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}

	parts := make([]string, 0, len(names))
	for _, spec := range d.Settings {
		if wanted[spec.Name] {
			parts = append(parts, fmt.Sprintf("%s is %s", spec.Name, spec.Description))
		}
	}
	return strings.Join(parts, "; ")
}
