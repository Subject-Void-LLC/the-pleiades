package collectionscaffold

import (
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/genutil"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
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

	// Site feeds Manifest.ExecutionContext.Site: where the method's code
	// runs. Empty means collection.SiteTarget, and the generated file always
	// states it, since internal/archtest requires every built-in to.
	Site collection.Site

	// Device feeds Manifest.ExecutionContext.Device: whether the method
	// acts on a device. Empty means collection.DeviceRequired. An optional
	// device generates a DeviceCall stub that answers yes, to be replaced
	// by the real per-call answer.
	Device collection.DeviceUse

	// EngineVersion feeds Manifest.EngineVersion, an unparsed constraint
	// string (see pkg/collection.Manifest's own doc comment for why no
	// semver library is involved).
	EngineVersion string

	// Doc feeds Manifest.Doc, rendered in full by renderDoc: Summary,
	// Description, Params, Returns, Examples and the rest, not just the
	// one-line summary an earlier revision emitted.
	//
	// It is the one field whose CLI flag takes JSON rather than a
	// plain value (`forge new-collection --doc-json`). A Doc is a
	// nested structure carrying paragraphs of prose, and the
	// alternatives were worse: a flag per leaf field cannot express a
	// repeated Param at all, and leaving it out entirely is what used
	// to force every scaffolded method's documentation to be retyped
	// by hand before it could pass
	// internal/archtest's TestCatalogDataDocsMatchTheRegistry.
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
// own requirement), every segment must be a valid identifier, every
// capability name must already be registered, and no documented parameter
// may take a name the engine reads for itself.
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
	if err := c.validateExecutionContext(); err != nil {
		return err
	}
	for _, p := range c.Doc.Params {
		// collection.Register refuses this at process start, which for a
		// scaffolded file means every binary importing it panics. Saying
		// so here, before a file is written, is the same rule moved left.
		if collection.IsReservedParam(p.Name) {
			return fmt.Errorf("collectionscaffold: parameter %q is the engine's device selector, which no method may declare; name it something else", p.Name)
		}
	}
	return nil
}

// validateExecutionContext refuses a site or device use that is not one of
// pkg/collection's values, and a target-side method that claims an optional
// device or none, which collection.Register refuses at process start: said
// here, before a file is written, it is the same rule moved left.
func (c Config) validateExecutionContext() error {
	switch c.Site {
	case "", collection.SiteTarget, collection.SiteController, collection.SiteHybrid:
	default:
		return fmt.Errorf("collectionscaffold: site %q is not %s, %s or %s", c.Site, collection.SiteTarget, collection.SiteController, collection.SiteHybrid)
	}
	switch c.Device {
	case "", collection.DeviceRequired, collection.DeviceOptional, collection.DeviceNone:
	default:
		return fmt.Errorf("collectionscaffold: device %q is not %s, %s or %s", c.Device, collection.DeviceRequired, collection.DeviceOptional, collection.DeviceNone)
	}
	if (c.Site == "" || c.Site == collection.SiteTarget) && (c.Device == collection.DeviceOptional || c.Device == collection.DeviceNone) {
		return fmt.Errorf("collectionscaffold: a target-side method acts on a device on every call, so device %q needs --site controller or hybrid", c.Device)
	}
	return nil
}

// siteIdentifier is the pkg/collection constant naming c.Site, the target
// constant when it is empty.
func (c Config) siteIdentifier() string {
	switch c.Site {
	case collection.SiteController:
		return "SiteController"
	case collection.SiteHybrid:
		return "SiteHybrid"
	default:
		return "SiteTarget"
	}
}

// deviceIdentifier is the pkg/collection constant naming c.Device, the
// required constant when it is empty.
func (c Config) deviceIdentifier() string {
	switch c.Device {
	case collection.DeviceOptional:
		return "DeviceOptional"
	case collection.DeviceNone:
		return "DeviceNone"
	default:
		return "DeviceRequired"
	}
}

// needsDeviceCall reports whether the generated method needs a DeviceCall,
// which only an optional device does.
func (c Config) needsDeviceCall() bool {
	return c.Device == collection.DeviceOptional
}
