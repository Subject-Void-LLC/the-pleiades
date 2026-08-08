package collectionscaffold

import (
	"fmt"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/genutil"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

// Config is the input to Generate: everything needed to emit one new
// namespaced Collection method package.
type Config struct {
	// Name is the full dotted namespaced method name, for example
	// "pkg.apt.install". It must contain at least one dot -- exactly what
	// pkg/collection.Register itself requires. A bare-domain namespace
	// like "pkg" alone is allowed, so a two-segment name such as
	// "pkg.install" is legal here too, not just three-or-more-segment
	// names.
	Name string

	// Capabilities feeds Manifest.RequiredCapabilities. Each name must
	// already be registered in pkg/capability; Validate checks this at
	// generation time so a typo fails here, not the first time the
	// generated package is blank-imported and its init() panics via
	// MustRegister.
	Capabilities []capability.Name

	// Transports feeds Manifest.SupportedTransports.
	Transports []string

	// RequiresElevation feeds Manifest.ExecutionContext.RequiresElevation.
	RequiresElevation bool

	// EngineVersion feeds Manifest.EngineVersion, an unparsed constraint
	// string (see pkg/collection.Manifest's own doc comment for why no
	// semver library is involved).
	EngineVersion string

	// Doc feeds Manifest.Doc. Every registered method should carry at
	// least a Summary; a declared stub carries nothing more, since
	// there is no real behavior yet for Params or Returns to describe.
	// tools/gendocs reads this field directly from catalogdata, so it
	// is this scaffold's one field with no corresponding CLI flag on
	// `forge new-collection`: real Doc content is written by hand into
	// catalogdata, not typed on a command line.
	Doc collection.Doc
}

// segments splits Name on every dot.
func (c Config) segments() []string {
	return strings.Split(c.Name, ".")
}

// PackageSegments returns every segment but the last: the nested
// directory path, and Go package name, the generated file lives under.
func (c Config) PackageSegments() []string {
	segs := c.segments()
	return segs[:len(segs)-1]
}

// PackageName returns the deepest package segment, the generated file's
// own `package` clause, for example "apt" for "pkg.apt.install".
func (c Config) PackageName() string {
	pkgSegs := c.PackageSegments()
	return pkgSegs[len(pkgSegs)-1]
}

// PackagePath returns PackageSegments joined with "/", the directory path
// (relative to internal/catalog/) the generated package lives under.
func (c Config) PackagePath() string {
	return strings.Join(c.PackageSegments(), "/")
}

// MethodSegment returns Name's final segment, for example "install" for
// "pkg.apt.install". It becomes the generated file's name.
func (c Config) MethodSegment() string {
	segs := c.segments()
	return segs[len(segs)-1]
}

// FunctionName returns the exported Go function name Generate emits, for
// example "Install" for "pkg.apt.install".
func (c Config) FunctionName() string {
	return genutil.ToExportedIdent(c.MethodSegment())
}

// Validate reports whether cfg is safe to generate from: Name must have at
// least two segments (at least one dot, matching pkg/collection.Register's
// own requirement), every segment must be a valid identifier, and every
// capability name must already be registered.
func (c Config) Validate() error {
	segs := c.segments()
	if len(segs) < 2 {
		return fmt.Errorf("collectionscaffold: %q is not namespaced (requires <namespace>.<method>)", c.Name)
	}
	if err := genutil.ValidateSegments(segs); err != nil {
		return fmt.Errorf("collectionscaffold: invalid name %q: %w", c.Name, err)
	}
	for _, name := range c.Capabilities {
		if _, known := capability.Lookup(name); !known {
			return fmt.Errorf("collectionscaffold: unknown capability %q", name)
		}
	}
	for _, transport := range c.Transports {
		if transport == "" {
			return fmt.Errorf("collectionscaffold: empty transport in --transports")
		}
	}
	return nil
}
