package syncplugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// Constructor builds a fresh Plugin instance from the dependencies a
// composition root supplies. Registration stores a constructor rather
// than an instance because a Plugin holds live connection state after
// Connect, and handing every caller the same connected object would make
// two concurrent syncs share one auth token and one page cursor.
//
// It takes Deps rather than nothing so a plugin needing the project's
// credential store receives it on the one path everything constructs
// through. Before that, a plugin's dependencies arrived through
// constructor Options only its own tests passed, and the registry-built
// instance every real caller got had none of them: see deps.go's doc
// comment for the defect that shipped. A plugin that needs nothing
// simply ignores the argument.
type Constructor func(Deps) Plugin

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

	// Settings declares the per-deployment values this plugin needs
	// beyond Config's shared fields, for example an AWS region. Open
	// refuses to build the plugin when a required one is absent, and
	// `pleiades inventory plugins` lists them, so a user finds out what
	// a plugin needs before running it rather than from a Connect-time
	// error.
	Settings []SettingSpec

	// RequiresCredentials says Connect resolves Config.CredentialName
	// through a credential store, so Open refuses to build this plugin
	// without one in Deps.
	//
	// It is declared here rather than inferred, because there is nothing
	// to infer it from: a plugin that reads deps.Credentials and one
	// that ignores it are the same type from outside. Declaring it makes
	// "this composition root forgot to wire the store" a refusal at
	// construction, in shared code, instead of a per-plugin error string
	// somewhere inside Connect that only fires against a real upstream.
	RequiresCredentials bool

	// New constructs an unconnected Plugin instance from deps. Callers
	// go through Open rather than calling this directly, so the settings
	// and credential-store checks above cannot be skipped.
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

// SnapshotForTest captures the process-wide sync plugin registry and returns a
// function that puts it back, for a test that registers into it.
//
// Without this a test's registration outlives the test, so a second
// iteration under `go test -count>1` fails on a duplicate registration
// rather than starting clean. Call it once at the top of such a test:
//
//	t.Cleanup(syncplugin.SnapshotForTest())
//
// It is exported rather than living in an export_test.go because a
// _test.go file cannot be imported across package boundaries, and tests in
// other packages register here too. internal/archtest forbids production
// code from calling it.
func SnapshotForTest() func() {
	return plugins.SnapshotForTest()
}

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
	// A setting nobody can name is not a setting, and one nobody can
	// look up the meaning of is a value an operator has to read source
	// to supply. Both are refused at registration, where the mistake is,
	// rather than at the first sync that needs the value.
	seen := make(map[string]bool, len(d.Settings))
	for _, spec := range d.Settings {
		if strings.TrimSpace(spec.Name) == "" {
			return fmt.Errorf("descriptor %q declares a setting with no name", d.Name)
		}
		if strings.TrimSpace(spec.Description) == "" {
			return fmt.Errorf("descriptor %q declares setting %q with no description", d.Name, spec.Name)
		}
		if seen[spec.Name] {
			return fmt.Errorf("descriptor %q declares setting %q more than once", d.Name, spec.Name)
		}
		seen[spec.Name] = true
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
