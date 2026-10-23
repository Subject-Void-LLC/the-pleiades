// Package external: the rules a program's capability definitions are held
// to, by the program itself (ServeProgram) and again by the loader.
package external

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// CheckCapabilities holds a program's capability definitions to the rules
// that keep them its own: each one well formed (capability.ValidateData),
// defined once, in a namespace the program's own methods use, and probed,
// when it names a probe, by one of the program's own read-only methods. A
// method requiring a capability in the data shape must require one these
// definitions hold, never another program's.
//
// It does not check a definition against the vocabulary a process already
// holds (a name another program took, a namespace a built-in method uses);
// the loader does that, since only it sees every program at once.
func CheckCapabilities(defs []capability.DataDefinition, methods []collection.Descriptor) error {
	namespaces := map[string]bool{}
	byName := map[string]collection.Descriptor{}
	for _, m := range methods {
		ns, _, _ := strings.Cut(m.Name, ".")
		namespaces[ns] = true
		byName[m.Name] = m
	}
	defined := map[capability.Name]bool{}
	for _, def := range defs {
		if err := capability.ValidateData(def); err != nil {
			return err
		}
		if defined[def.Name] {
			return fmt.Errorf("capability %q is defined twice", def.Name)
		}
		defined[def.Name] = true
		if ns := capability.DataNamespace(def.Name); !namespaces[ns] {
			return fmt.Errorf("capability %q is in the namespace %q, which none of this program's methods uses: a program defines capabilities only in its own namespaces", def.Name, ns)
		}
		if def.Probe == "" {
			continue
		}
		probe, ok := byName[def.Probe]
		switch {
		case !ok:
			return fmt.Errorf("capability %q names the probe %q, which is not one of this program's methods", def.Name, def.Probe)
		case !probe.Manifest.Reversibility.ReadOnly:
			return fmt.Errorf("capability %q names the probe %q, which does not declare that it only reads: a probe runs during onboarding and must change nothing", def.Name, def.Probe)
		case slices.Contains(probe.Manifest.RequiredCapabilities, def.Name):
			return fmt.Errorf("capability %q names the probe %q, which requires it: a probe runs before the device holds it", def.Name, def.Probe)
		}
	}
	for _, m := range methods {
		for _, req := range m.Manifest.RequiredCapabilities {
			if capability.IsDataName(req) && !defined[req] {
				return fmt.Errorf("%q requires %q, which this program does not define", m.Name, req)
			}
		}
	}
	return nil
}
