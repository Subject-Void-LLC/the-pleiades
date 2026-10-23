//go:build unix

// Package loader: the capabilities a program defines as data, checked
// before anything registers. Unix only, as loading is (load_unix.go).
package loader

import (
	"fmt"
	"reflect"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// validateCapabilities checks the capabilities one program defines as data
// (external.Description.Capabilities) and returns them as a set for its
// methods' own check, or the problems that refuse them. The program held
// them to external.CheckCapabilities before describing itself; that is
// checked again here, since the program is not trusted to have done it,
// and so is what only the loader can see: a namespace that belongs to The
// Pleiades, a name this process already defines, a name two programs both
// define (claimedBy, across the directory), a property whose name reads as
// a secret (a grant's properties travel in the dispatch payload, and a
// secret has no business there), and text a terminal would act on.
func validateCapabilities(c candidate, reserved map[string]bool, claimedBy map[capability.Name]string) (map[capability.Name]bool, []error) {
	defined := map[capability.Name]bool{}
	if len(c.desc.Capabilities) == 0 {
		return defined, nil
	}
	methods := make([]collection.Descriptor, 0, len(c.desc.Methods))
	for _, m := range c.desc.Methods {
		methods = append(methods, collection.Descriptor{Name: m.Name, Manifest: m.Manifest})
	}
	var problems []error
	fail := func(err error) { problems = append(problems, fmt.Errorf("program %s: %w", c.path, err)) }
	if err := external.CheckCapabilities(c.desc.Capabilities, methods); err != nil {
		fail(err)
		return defined, problems
	}
	for _, def := range c.desc.Capabilities {
		if err := checkManifestText(reflect.ValueOf(def), "capability"); err != nil {
			fail(fmt.Errorf("capability %q: %w", def.Name, err))
			continue
		}
		if ns := capability.DataNamespace(def.Name); reserved[ns] {
			fail(fmt.Errorf("capability %q is in the %q namespace, which belongs to The Pleiades itself", def.Name, ns))
			continue
		}
		if _, taken := capability.Lookup(def.Name); taken {
			fail(fmt.Errorf("capability %q is already defined in this process; a program never replaces one", def.Name))
			continue
		}
		if first, dup := claimedBy[def.Name]; dup {
			fail(fmt.Errorf("capability %q is defined by both %s and %s; neither was loaded", def.Name, first, c.path))
			continue
		}
		secret := false
		for _, p := range def.Properties {
			if redact.SecretName(p.Name) {
				fail(fmt.Errorf("capability %q declares the property %q, whose name reads as a secret: a grant's properties travel to the program in the clear, so a secret belongs in the device's credential", def.Name, p.Name))
				secret = true
			}
		}
		if secret {
			continue
		}
		claimedBy[def.Name] = c.path
		defined[def.Name] = true
	}
	return defined, problems
}
