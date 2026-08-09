package syncplugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// Constructor builds a fresh Plugin instance. Registration stores a
// constructor rather than an instance because a Plugin holds live
// connection state after Connect, and handing every caller the same
// connected object would make two concurrent syncs share one auth token and
// one page cursor.
type Constructor func() Plugin

// Status says whether a registered plugin actually talks to its upstream
// system yet. It mirrors pkg/collection.Status exactly, and for the same
// reason: a generated skeleton must be able to say out loud that it is a
// skeleton, so nothing mistakes a registration for an implementation.
type Status string

const (
	// StatusDeclared means the plugin is registered and its shape is real,
	// but its methods return an explicit not-implemented error. Every
	// plugin the Forge generates starts here.
	StatusDeclared Status = "declared"

	// StatusImplemented means the plugin has run against its real upstream
	// system. Nothing may claim this on the strength of a test that mocks
	// the transport, which is the theater RULE 0 names explicitly.
	StatusImplemented Status = "implemented"
)

// Descriptor is what a plugin package registers: its identity, the default
// configuration it ships with, whether it is real yet, and how to construct
// one.
type Descriptor struct {
	// Name is the registration key, matching Config.Name and the string
	// stamped into every synced device's SourceAuthority.
	Name string

	// Description is one line of help text for `pleiades inventory sync`.
	Description string

	// DefaultConfig is the configuration a caller starts from. A CLI or
	// project file overrides its fields; the plugin's own defaults (its
	// endpoint shape, whether it is inherently read-only) live here so a
	// caller does not have to know them.
	DefaultConfig Config

	// Status is whether this plugin has been proven against its real
	// upstream system. An empty Status is read as StatusDeclared, so a
	// descriptor that forgets the field is treated as a skeleton rather
	// than silently claiming to work.
	Status Status

	// New constructs an unconnected Plugin instance.
	New Constructor
}

// Implemented reports whether this plugin has been proven against its real
// upstream system. Callers use it rather than comparing Status directly, so
// the empty-means-declared default lives in one place.
func (d Descriptor) Implemented() bool {
	return d.Status == StatusImplemented
}

// plugins is the process-wide sync plugin table. It is the same Section 25
// shared registry primitive device types and Collection methods use, rather
// than a third hand-rolled map.
var plugins = registry.New[Descriptor]()

// MustRegister adds d to the sync plugin registry, panicking on a duplicate
// name or an invalid descriptor. Plugin packages call it from their own
// init(), so a duplicate built-in name fails at process start rather than
// silently shadowing one registration with another.
func MustRegister(d Descriptor) {
	if err := Register(d); err != nil {
		panic("syncplugin: " + err.Error())
	}
}

// Register adds d to the sync plugin registry, returning an error rather
// than panicking. It exists for a genuinely runtime registration, where a
// duplicate is a data problem the caller should handle.
func Register(d Descriptor) error {
	if strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("descriptor has no name")
	}
	if d.New == nil {
		return fmt.Errorf("descriptor %q has no constructor", d.Name)
	}
	// The descriptor's own default config must name the same plugin, or a
	// device synced through it would be stamped with a source authority
	// that does not match the name it was looked up under.
	if d.DefaultConfig.Name != "" && d.DefaultConfig.Name != d.Name {
		return fmt.Errorf("descriptor %q has default config named %q", d.Name, d.DefaultConfig.Name)
	}
	return plugins.Register(d.Name, d)
}

// Lookup returns the descriptor registered under name.
func Lookup(name string) (Descriptor, bool) {
	return plugins.Get(name)
}

// All returns a snapshot of every registered descriptor, keyed by name.
func All() map[string]Descriptor {
	return plugins.All()
}

// Names returns every registered plugin name, sorted, for help text and for
// tests that need a stable ordering.
func Names() []string {
	all := plugins.All()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
