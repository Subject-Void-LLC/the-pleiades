package devicescaffold

import (
	"fmt"
	"strings"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/genutil"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/capability"
)

// Config is the input to Generate: everything needed to emit one new
// vendor device-type package.
type Config struct {
	// Vendor is both the new package's directory name and its Go package
	// name (for example "juniper"), so the generated package lives at
	// internal/inventory/devices/<Vendor>/.
	Vendor string

	// TypeKey is the full record.RegisterType registration key (for
	// example "junos_router"). Its last underscore-delimited segment (see
	// Kind) becomes the generated file name and, PascalCased, the
	// exported struct and constructor name: "junos_router" and
	// "cisco_router" both yield "Router", exactly matching the two
	// hand-written packages this generator mirrors, even when (as in that
	// example) Vendor and TypeKey's own prefix don't match.
	TypeKey string

	// Capabilities is the vendor baseline unioned into every hydrated
	// instance's declared set, exactly as NewRouter/NewServer already
	// union their own hardcoded baseline with rec.Capabilities. Each name
	// must already be registered in pkg/capability; Validate checks this
	// at generation time so a typo fails here, not the first time the
	// generated package is blank-imported.
	Capabilities []capability.Name
}

// Kind returns TypeKey's last underscore-delimited segment: the part that
// becomes the generated struct name, constructor name, and file name. If
// TypeKey has no underscore, Kind returns TypeKey unchanged.
func (c Config) Kind() string {
	idx := strings.LastIndex(c.TypeKey, "_")
	if idx == -1 {
		return c.TypeKey
	}
	return c.TypeKey[idx+1:]
}

// StructName returns the exported Go type name Generate emits, for
// example "Router" for TypeKey "junos_router".
func (c Config) StructName() string {
	return genutil.ToExportedIdent(c.Kind())
}

// Validate reports whether cfg is safe to generate from: Vendor and
// TypeKey (as a whole, and Kind, TypeKey's final segment, independently,
// since a TypeKey like "a_" is a valid segment overall but yields an empty
// Kind) must be valid identifiers, and every capability name must already
// be registered.
func (c Config) Validate() error {
	if err := genutil.ValidateSegment(c.Vendor); err != nil {
		return fmt.Errorf("devicescaffold: invalid vendor: %w", err)
	}
	if err := genutil.ValidateSegment(c.TypeKey); err != nil {
		return fmt.Errorf("devicescaffold: invalid type key %q: %w", c.TypeKey, err)
	}
	if err := genutil.ValidateSegment(c.Kind()); err != nil {
		return fmt.Errorf("devicescaffold: invalid type key %q, final segment: %w", c.TypeKey, err)
	}
	for _, name := range c.Capabilities {
		if _, known := capability.Lookup(name); !known {
			return fmt.Errorf("devicescaffold: unknown capability %q", name)
		}
	}
	return nil
}
