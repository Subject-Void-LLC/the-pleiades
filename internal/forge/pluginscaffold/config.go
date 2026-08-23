package pluginscaffold

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/genutil"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/syncplugin"
)

// Config is the input to Generate: everything needed to emit one new
// inventory sync plugin package.
//
// It is deliberately smaller than collectionscaffold.Config. A Collection
// method's manifest has to carry enough for a plan-time capability and
// platform-target match, because nothing else in the system knows what that
// method needs. A sync plugin's real configuration is per-deployment (which
// Catalyst Center, whose credential, what page size), arrives at Connect
// time, and cannot be known by a generator.
type Config struct {
	// Name is the plugin's registration key, its Go package name, and its
	// directory under internal/inventory/plugins. It is a single segment,
	// not a dotted path: a plugin names an upstream system, and upstream
	// systems do not nest the way Collection namespaces do.
	Name string

	// Description is the one-line help text `pleiades inventory sync`
	// shows. It is required, because a plugin list where half the entries
	// explain themselves and half do not is worse than one that demands the
	// line up front.
	Description string

	// Endpoint is the default upstream locator baked into the generated
	// descriptor, for example "https://sandboxdnac.cisco.com". It may be
	// empty for a plugin whose endpoint is always per-deployment.
	Endpoint string

	// ReadOnly declares the upstream authoritative and never written back.
	// It lands in the generated descriptor's DefaultConfig, so the refusal
	// is the plugin's shipped default rather than something each caller has
	// to remember to set.
	ReadOnly bool

	// RequiresCredentials says this plugin's Connect resolves a
	// credential, so the generated descriptor declares
	// syncplugin.Descriptor.RequiresCredentials and the generated New
	// keeps the store out of syncplugin.Deps.
	//
	// It is here rather than left to a human to add later because
	// forgetting it is exactly how the AWS plugin shipped unreachable: a
	// plugin that needs a store and does not say so gets built with a nil
	// one, and finds out only against a real upstream. Generating the
	// declaration alongside the code that reads it means the two cannot
	// start out disagreeing.
	RequiresCredentials bool

	// Settings are the per-deployment values this plugin needs beyond
	// syncplugin.Config's shared fields, for example an AWS region. They
	// land in the generated descriptor, so `pleiades inventory plugins`
	// lists them and syncplugin.Open refuses a missing required one by
	// name.
	//
	// This is the shared runtime type rather than a generator-local copy
	// of it: two structural definitions of one concept is the drift this
	// repository has already paid for elsewhere, and a generator whose
	// idea of a setting differs from the registry's would emit code that
	// does not compile.
	Settings []syncplugin.SettingSpec
}

// TypeName returns the exported Go type name for the generated plugin, for
// example "CatalystCenter" for "catalyst_center".
func (c Config) TypeName() string {
	return genutil.ToExportedIdent(c.Name)
}

// PackageName returns the generated package's Go identifier: Name with its
// underscores removed, so "catalyst_center" becomes "catalystcenter".
//
// The registration key keeps its underscores because it is data (it is what
// a user types after --plugin, and what is stamped into every synced
// device's SourceAuthority), while a Go package name is code and the
// convention there is a single lowercase word. The existing static_yaml
// plugin already lives in package staticyaml, so this rule describes what
// the codebase does rather than introducing a new spelling.
func (c Config) PackageName() string {
	return strings.ReplaceAll(c.Name, "_", "")
}

// PackagePath returns the directory the generated package lives in,
// relative to the repository root.
func (c Config) PackagePath() string {
	return "internal/inventory/plugins/" + c.PackageName()
}

// ImportPath returns the full Go import path of the generated package, which
// is what the plugins composition root blank-imports.
func (c Config) ImportPath() string {
	return "github.com/Subject-Void-LLC/the-pleiades/" + c.PackagePath()
}

// Validate reports whether cfg is safe to generate from. It fails closed on
// everything that would otherwise produce an unbuildable package or an
// unsafe path, reusing genutil rather than reimplementing the same checks a
// third time.
func (c Config) Validate() error {
	if err := genutil.ValidateSegment(c.Name); err != nil {
		return fmt.Errorf("pluginscaffold: invalid plugin name %q: %w", c.Name, err)
	}
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("pluginscaffold: plugin %q has no description", c.Name)
	}
	for _, spec := range c.Settings {
		if err := genutil.ValidateSegment(spec.Name); err != nil {
			return fmt.Errorf("pluginscaffold: plugin %q declares an invalid setting name %q: %w", c.Name, spec.Name, err)
		}
		if strings.TrimSpace(spec.Description) == "" {
			return fmt.Errorf("pluginscaffold: plugin %q declares setting %q with no description", c.Name, spec.Name)
		}
	}
	if c.Endpoint != "" {
		parsed, err := url.Parse(c.Endpoint)
		if err != nil {
			return fmt.Errorf("pluginscaffold: plugin %q has an invalid endpoint: %w", c.Name, err)
		}
		switch parsed.Scheme {
		case "http", "https":
			if parsed.Host == "" {
				return fmt.Errorf("pluginscaffold: plugin %q endpoint has no host: %s", c.Name, c.Endpoint)
			}
		case "file":
			if parsed.Path == "" {
				return fmt.Errorf("pluginscaffold: plugin %q file endpoint has no path: %s", c.Name, c.Endpoint)
			}
		default:
			return fmt.Errorf("pluginscaffold: plugin %q endpoint scheme must be http, https, or file, got %q", c.Name, parsed.Scheme)
		}
	}
	return nil
}
